package runner

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/google/uuid"
)

// The services that run beside a sandbox (spec/16, "Services beside the
// sandbox"). Everything here is the runner's arm: WHICH services an agent gets
// is decided on the control plane, and by the time it arrives here the list has
// been through sandbox.ValidateServices.
//
// The construction is the one the egress proxy already uses — an internal
// docker network and containers on it under a DNS alias. Two things differ, and
// both follow from what a service is:
//
//   - The network belongs to the SANDBOX, not to the runner. The egress segment
//     is shared by an organisation on purpose; a service network must not be,
//     because two agents both asking for `db` would then put two aliases of the
//     same name into one segment and docker would answer whichever it liked.
//     A wrong database that answers is worse than none that does.
//
//   - It is `--internal`. A test database has no business on the internet, and
//     the sandbox's own way out is the egress proxy's to grant (spec/16). An
//     internal network brings no gateway with it, so joining one leaves the
//     sandbox's default route exactly as it was — which is what makes it safe
//     to attach as a SECOND network beside the egress one.

// serviceLabel marks every object belonging to an agent's services, so a sweep
// can find what a crash left behind without parsing names.
const serviceLabel = "covey.services.agent"

// servicesNetworkFor is the segment one sandbox shares with its services.
func servicesNetworkFor(agentID uuid.UUID) string {
	return "covey-services-" + short(agentID)
}

// serviceContainerName carries both halves: whose service it is, and which. The
// short agent ID keeps it inside what a terminal shows; the name is unique per
// agent because ValidateServices refuses a duplicate.
func serviceContainerName(agentID uuid.UUID, service string) string {
	return "covey-service-" + short(agentID) + "-" + service
}

// agentIDFromContainer reads the agent back out of a sandbox container name.
//
// Stop and Wait are handed a name and nothing else — that is the provider
// interface, and widening it for this would push a detail of one provider into
// every other. The name is formed in exactly one place (containerName), so
// reading it back is a local fact, not a guess.
func agentIDFromContainer(name string) (uuid.UUID, bool) {
	rest := strings.TrimPrefix(name, "covey-sandbox-")
	if rest == name {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(rest)
	return id, err == nil
}

// startServices brings the declared services up on the sandbox's own network.
//
// Called BEFORE the sandbox, so that a daemon reporting `ready` is reporting a
// workplace that is actually complete. Readiness of the services themselves is
// another question and deliberately not answered here: a database is running
// long before it accepts connections, and whoever waits for a port is the one
// that wants to talk to it. The agent retries; the platform does not pretend.
func (p *Docker) startServices(ctx context.Context, spec StartSandbox) error {
	if len(spec.Services) == 0 {
		return nil
	}
	network := servicesNetworkFor(spec.AgentID)
	if err := p.ensureServicesNetwork(ctx, spec.AgentID, network); err != nil {
		return err
	}
	for _, svc := range spec.Services {
		name := serviceContainerName(spec.AgentID, svc.Name)
		// A predecessor of the same name blocks the start. It belongs to this
		// agent either way — the name says so — so it goes.
		_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", name).Run()

		args := []string{"run", "-d",
			"--name", name,
			"--network", network,
			// The alias is the whole point: the agent reaches `db`, not a
			// container name with an ID in it.
			"--network-alias", svc.Name,
			"--label", serviceLabel + "=" + spec.AgentID.String(),
			// No restart policy: a service that dies has failed, and a loop
			// that hides it would leave the agent guessing at a database that
			// is there every second time.
			"--restart", "no",
		}
		for k, v := range svc.Env {
			args = append(args, "-e", k+"="+v)
		}
		args = append(args, svc.Image)

		if out, err := exec.CommandContext(ctx, p.docker(), args...).CombinedOutput(); err != nil {
			msg := strings.TrimSpace(string(out))
			// Everything that came up before this one goes again. Half a set of
			// services is the state in which an agent reports the wrong defect:
			// it finds the queue missing and writes that into a merge request,
			// while the real fault was a typo in an image reference.
			p.removeServices(context.WithoutCancel(ctx), spec.AgentID)
			if strings.Contains(msg, "No such image") || strings.Contains(msg, "Unable to find image") ||
				strings.Contains(msg, "pull access denied") || strings.Contains(msg, "manifest unknown") {
				return fmt.Errorf("service %q: the image %q could not be fetched — it is the project's image, not one of the workplace catalogue's, so this host needs access to the registry it comes from: %s",
					svc.Name, svc.Image, msg)
			}
			return fmt.Errorf("service %q (%s): %v: %s", svc.Name, svc.Image, err, msg)
		}
	}
	return nil
}

// ensureServicesNetwork creates the sandbox's internal segment.
func (p *Docker) ensureServicesNetwork(ctx context.Context, agentID uuid.UUID, name string) error {
	if err := exec.CommandContext(ctx, p.docker(), "network", "inspect", name).Run(); err == nil {
		return nil
	}
	out, err := exec.CommandContext(ctx, p.docker(), "network", "create", "--internal",
		"--label", serviceLabel+"="+agentID.String(), name).CombinedOutput()
	if err != nil && !strings.Contains(string(out), "already exists") {
		return fmt.Errorf("create the services network: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// joinServices attaches the running sandbox to its services network.
//
// Deliberately after `docker run` rather than as a second `--network` on it:
// the sandbox's FIRST network decides its default route (the egress segment in
// hard isolation, the bridge otherwise), and that decision must not depend on
// whether an agent happens to have declared a database. Attaching afterwards
// adds a route to the service subnet and nothing else — an internal network
// carries no gateway to compete with.
//
// It still happens inside Start, so it is done before the control plane treats
// the sandbox as up: the daemon has yet to report `ready`, and no job runs
// before it does.
func (p *Docker) joinServices(ctx context.Context, spec StartSandbox, container string) error {
	if len(spec.Services) == 0 {
		return nil
	}
	out, err := exec.CommandContext(ctx, p.docker(), "network", "connect",
		servicesNetworkFor(spec.AgentID), container).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "already exists") || strings.Contains(msg, "already connected") {
			return nil
		}
		return fmt.Errorf("attach the sandbox to its services network: %v: %s", err, msg)
	}
	return nil
}

// removeServices tears the services of one agent down: the containers first,
// then the segment they hung in.
//
// Best effort, and quiet about it. It runs on every path that ends a sandbox —
// the clean stop, the crash the watcher saw, and the start of the next sandbox
// before anything else happens. Each of those may find the work already done,
// and none of them has anybody to report it to.
//
// The lookup goes through the label rather than through the declaration: what
// has to go is what is RUNNING, and after a control plane restart with a
// changed configuration those are not the same list.
func (p *Docker) removeServices(ctx context.Context, agentID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, p.docker(), "ps", "-aq",
		"--filter", "label="+serviceLabel+"="+agentID.String()).Output()
	if err == nil {
		for _, id := range strings.Fields(string(out)) {
			_ = exec.CommandContext(ctx, p.docker(), "rm", "-f", id).Run()
		}
	}
	// The network only goes once nothing hangs in it any more. If the sandbox
	// is still attached — a stop that ran into its timeout — the removal fails,
	// and that is correct: the next start creates it again under the same name
	// and finds it usable.
	_ = exec.CommandContext(ctx, p.docker(), "network", "rm", servicesNetworkFor(agentID)).Run()
}
