package runner

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/sandbox"
)

// liveImage is a container that stays up and carries a resolver — enough to ask
// it, from inside, whether it can reach the service standing beside it.
const liveImage = "pgvector/pgvector:pg16"

// The services against a REAL docker.
//
// The tests next door check the command lines the provider builds, and they
// would go on passing if docker refused every one of them. The three facts this
// construction rests on are not statements about our own code: that an internal
// network can be attached to a running container at all, that docker's embedded
// DNS then answers the alias, and that the sandbox's default route survives it.
// A fake binary cannot say anything about any of them.
//
// Skipped without docker, like the integration suite is without its database.
func TestLiveServicesAreReachableFromTheSandbox(t *testing.T) {
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("no docker on this host")
	}
	if err := exec.Command("docker", "image", "inspect", liveImage).Run(); err != nil {
		t.Skipf("%s is not on this host", liveImage)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	agentID := uuid.New()
	p := &Docker{Image: liveImage, DataDir: t.TempDir()}
	spec := StartSandbox{
		AgentID: agentID,
		Image:   liveImage,
		Env:     map[string]string{"POSTGRES_PASSWORD": "test"},
		Services: []sandbox.Service{
			{Name: "db", Image: liveImage, Env: map[string]string{"POSTGRES_PASSWORD": "test"}},
		},
	}
	container, started, err := p.Start(ctx, spec)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	// Whatever happens below, nothing of this may outlive the test.
	t.Cleanup(func() { _ = p.Stop(context.Background(), container) })

	// What came up, in the host's own words. The declaration says
	// `pgvector/pgvector:pg16`; only the host can say which bytes that
	// resolved to, and that is the half a recording needs six months later.
	if len(started) != 1 || started[0].Name != "db" {
		t.Fatalf("the host did not report the service it started: %+v", started)
	}
	if !strings.HasPrefix(started[0].ImageID, "sha256:") {
		t.Errorf("no image id for the started service: %+v", started[0])
	}

	// The service answers to its name. This is the whole promise: the agent
	// writes `db`, not an address and not a container name with an ID in it.
	out, err := exec.CommandContext(ctx, "docker", "exec", container, "getent", "hosts", "db").CombinedOutput()
	if err != nil || !strings.Contains(string(out), "db") {
		t.Fatalf("the sandbox does not resolve `db`: %v: %s", err, out)
	}

	// And the default route is still the one the sandbox started with. This is
	// the failure the whole "attach afterwards, and internal" design is built
	// to avoid, and it would be invisible until an agent tried to reach the
	// control plane.
	// Read from /proc rather than through `ip`: not every image carries
	// iproute2, and the kernel's own table is the authority anyway. A default
	// route is the line whose destination is 00000000.
	out, err = exec.CommandContext(ctx, "docker", "exec", container, "cat", "/proc/net/route").CombinedOutput()
	if err != nil {
		t.Fatalf("the routing table is not readable: %v: %s", err, out)
	}
	if !hasDefaultRoute(string(out)) {
		t.Fatalf("the sandbox lost its default route when it joined its services:\n%s", out)
	}

	// Stop ends both halves. A service that outlived its sandbox would hold the
	// state of a run that is over.
	if err := p.Stop(ctx, container); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	left, _ := exec.CommandContext(ctx, "docker", "ps", "-aq",
		"--filter", "label="+serviceLabel+"="+agentID.String()).Output()
	if strings.TrimSpace(string(left)) != "" {
		t.Errorf("a service outlived its sandbox: %s", left)
	}
	if err := exec.CommandContext(ctx, "docker", "network", "inspect",
		servicesNetworkFor(agentID)).Run(); err == nil {
		t.Errorf("the segment %s stayed behind", servicesNetworkFor(agentID))
	}
}

// hasDefaultRoute reads /proc/net/route: iface, destination, gateway, … — a
// destination of all zeroes is the default route.
func hasDefaultRoute(table string) bool {
	for _, line := range strings.Split(table, "\n")[1:] {
		if f := strings.Fields(line); len(f) >= 3 && f[1] == "00000000" {
			return true
		}
	}
	return false
}
