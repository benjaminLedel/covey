package orchestrator

import (
	"context"
	"slices"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/sandbox"
)

// The agent brings up the services its project needs (spec/16, #121).
//
// This is the half that makes the mechanism usable rather than merely correct.
// A declaration typed by a manager assumes somebody knew, before the run, which
// database this project wants. For a QA agent accepting merge requests across
// several projects that assumption is wrong on the second project — and the
// answer has been in the repository all along, in its own `docker-compose.yml`.
//
// So the agent reads that file (it is in the checkout it has just made, on the
// machine only it can see) and sends the CONTENT. The control plane parses the
// subset that may run beside a sandbox, asks the organisation's allowlist, and
// tells the host holding this sandbox to bring up what is left.
//
// Three lines are drawn deliberately:
//
//   - The file is READ, never executed. `docker compose up` on a runner would
//     mean the project decides what runs on somebody else's machine.
//   - The agent does not choose IMAGES, it chooses among images the
//     organisation has allowed. That is what makes the whole thing safe to
//     hand over: the privileged act is extending the allowlist, not naming a
//     reference.
//   - Partial is normal here, and it is the difference from the declared set at
//     the wake. A compose file legitimately contains services that must not run
//     beside a sandbox — the project's own application above all, which the
//     agent builds INSIDE its sandbox. Refusing the file because of it would
//     make the mechanism useless for the files it exists for. So it starts what
//     it may and reports the rest, service by service, with the reason.

// serviceResult is what the agent gets back. Three lists, because the three
// mean different things to it: what it may now talk to, what this file does not
// offer it, and what its organisation does not allow. Only the third is
// somebody else's decision to change.
type serviceResult struct {
	Started []sandbox.ServiceRun  `json:"started"`
	Skipped []sandbox.ComposeSkip `json:"skipped,omitempty"`
	Refused []serviceRefusal      `json:"refused,omitempty"`
}

type serviceRefusal struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Reason string `json:"reason"`
}

// startServices serves covey/start_services.
func (o *Orchestrator) startServices(
	ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestHiring,
	ok func(any) daemon.InjectHiring, fail func(string, ...any) daemon.InjectHiring,
) daemon.InjectHiring {
	raw := strings.TrimSpace(req.Compose)
	if raw == "" {
		return fail("send the content of the project's docker-compose.yml as `compose` — read it out of your checkout")
	}
	parsed, err := sandbox.ParseCompose([]byte(raw))
	if err != nil {
		return fail("%v", err)
	}

	wanted := parsed.Services
	skipped := parsed.Skipped
	// An agent that only wants the database says so. The ones it did not ask
	// for are reported as skipped rather than silently dropped — otherwise a
	// typo in the filter looks like a compose file that does not contain what
	// it plainly contains.
	if len(req.Only) > 0 {
		var keep []sandbox.Service
		for _, svc := range wanted {
			if slices.Contains(req.Only, svc.Name) {
				keep = append(keep, svc)
				continue
			}
			skipped = append(skipped, sandbox.ComposeSkip{
				Name: svc.Name, Reason: "you did not ask for it (`only`)",
			})
		}
		wanted = keep
	}

	// The organisation's allowlist. Per service, not for the whole file: see
	// the note above about partial being normal here.
	var allowed []sandbox.Service
	var refused []serviceRefusal
	if o.Workplaces != nil {
		patterns, err := o.Workplaces.ServicePatterns(ctx, agent.OrgID)
		if err != nil {
			return fail("the organisation's allowlist is not readable: %v", err)
		}
		for _, svc := range wanted {
			if sandbox.ImageAllowed(patterns, svc.Image) {
				allowed = append(allowed, svc)
				continue
			}
			// The remedy travels with the refusal — but to the HUMAN, not to
			// the agent: it cannot add the pattern itself, and telling it how
			// would only invite it to try.
			refused = append(refused, serviceRefusal{
				Name:  svc.Name,
				Image: svc.Image,
				Reason: "this organisation does not allow the image " + svc.Image +
					" beside a sandbox. Somebody with administrative rights has to allow it; " +
					"report it and carry on without this service, or say that you cannot.",
			})
		}
	} else {
		allowed = wanted
	}

	result := serviceResult{Skipped: skipped, Refused: refused}
	if len(allowed) > 0 {
		starter, canStart := o.sandboxOf(agent.ID).(ServiceStarter)
		if !canStart {
			return fail("this sandbox cannot take services while it runs")
		}
		started, err := starter.StartServices(ctx, allowed)
		if err != nil {
			// Recorded even in failure: an agent that could not get its
			// database is the most common reason for a run that reads like the
			// project is broken.
			o.recordServiceRequest(ctx, agent, taskID, "failed", nil, refused, err)
			return fail("%v", err)
		}
		result.Started = started
		o.mu.Lock()
		if s := o.sessions[agent.ID]; s != nil {
			s.services = append(s.services, started...)
		}
		o.mu.Unlock()
	}
	o.recordServiceRequest(ctx, agent, taskID, "started", result.Started, refused, nil)
	return ok(result)
}

// sandboxOf is the sandbox of a running session, or nil.
func (o *Orchestrator) sandboxOf(agentID uuid.UUID) Sandbox {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s := o.sessions[agentID]; s != nil {
		return s.sandbox
	}
	return nil
}

// recordServiceRequest writes what the agent asked for and what came of it —
// against the TASK, because this happened during a run and belongs to it.
func (o *Orchestrator) recordServiceRequest(
	ctx context.Context, agent agents.Agent, taskID uuid.UUID, status string,
	started []sandbox.ServiceRun, refused []serviceRefusal, err error,
) {
	if o.Obs == nil {
		return
	}
	payload := map[string]any{"status": status, "source": "compose"}
	if len(started) > 0 {
		payload["services"] = started
	}
	if len(refused) > 0 {
		payload["refused"] = refused
	}
	if err != nil {
		payload["error"] = err.Error()
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindService, payload)
}

// mayStartServices: the scope that unlocks this, and nothing else. Deliberately
// not `agents:write` — that one lets an agent draft colleagues, and a QA agent
// that wants a database has no business with it.
func (o *Orchestrator) mayStartServices(ctx context.Context, agent agents.Agent) bool {
	return o.mayUseCovey(ctx, agent, scopeServices)
}
