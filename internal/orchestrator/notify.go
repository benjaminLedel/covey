package orchestrator

// What the orchestrator hands to the notification mails (#169).
//
// The emission sits here and not in the stores it happens in, because the
// stores are shared with paths that must not send mail — a migration, a test,
// the CLI. What the orchestrator does is by definition operation, and
// operation is what a person wants to hear about.
//
// Every call is best effort. A notification that cannot be written must not
// fail the run that produced the event: the platform's job is the work, and
// telling somebody about it comes second.

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/notify"
	"covey/internal/observability"
)

// emit writes one event, logging what it cannot write.
func (o *Orchestrator) emit(ctx context.Context, ev notify.Event) {
	if o.Notify == nil {
		return
	}
	if err := o.Notify.Emit(ctx, ev); err != nil {
		o.Log.Warn("notification not recorded", "class", ev.Class, "kind", ev.Kind, "err", err)
	}
}

// notifyApproval: a guard rail caught an action, and the agent is standing
// still until somebody decides. This is the event this whole package was built
// for — its cost is measured in an agent's held-open session.
func (o *Orchestrator) notifyApproval(ctx context.Context, agent agents.Agent, appr observability.Approval) {
	id := appr.ID
	o.emit(ctx, notify.Event{
		OrgID: agent.OrgID, AgentID: agent.ID,
		Class: notify.ClassDecision, Kind: notify.KindApproval, SubjectID: &id,
		Title: fmt.Sprintf("%s is waiting for a release: %s", agent.DisplayName, appr.Action),
		Link:  "/inbox",
	})
}

// notifyImprovement: a review produced an open point. Nothing is blocked by
// it, which is why it shares a mail with the approvals rather than getting one
// of its own.
func (o *Orchestrator) notifyImprovement(ctx context.Context, item agents.ImprovementItem) {
	id := item.ID
	name := o.agentName(ctx, item.AgentID)
	o.emit(ctx, notify.Event{
		OrgID: item.OrgID, AgentID: item.AgentID,
		Class: notify.ClassDecision, Kind: notify.KindImprovement, SubjectID: &id,
		Title: fmt.Sprintf("%s: %s", name, item.Title),
		Link:  "/improvements",
	})
}

// NotifyTaskEnded is the backlog's OnComplete hook. It is a method rather than
// a closure in main.go because it needs the agent's name, and the orchestrator
// is what has the registry at hand.
func (o *Orchestrator) NotifyTaskEnded(ctx context.Context, task backlog.Task) {
	kind := notify.KindTaskDone
	verb := "finished"
	if task.State == backlog.StateFailed {
		kind = notify.KindTaskFailed
		verb = "failed at"
	}
	id := task.ID
	o.emit(ctx, notify.Event{
		OrgID: task.OrgID, AgentID: task.AgentID,
		Class: notify.ClassTask, Kind: kind, SubjectID: &id,
		Title: fmt.Sprintf("%s %s: %s", o.agentName(ctx, task.AgentID), verb, task.Title),
		Link:  "/agents/" + task.AgentID.String(),
	})
}

// notifyBudget: an agent was paused because it had spent its cap. Money, and
// therefore a class of its own — controlling reads this, the agent's owner
// does not necessarily.
func (o *Orchestrator) notifyBudget(ctx context.Context, agent agents.Agent, spent, limit float64) {
	id := agent.ID
	o.emit(ctx, notify.Event{
		OrgID: agent.OrgID, AgentID: agent.ID,
		Class: notify.ClassCost, Kind: notify.KindBudget, SubjectID: &id,
		Title: fmt.Sprintf("%s was paused: %.2f of %.2f USD spent", agent.DisplayName, spent, limit),
		Link:  "/costs",
	})
}

// notifyFleetKilled: somebody pulled the emergency stop for a whole
// organisation. Whoever administers it should not learn that from a quiet
// dashboard.
func (o *Orchestrator) notifyFleetKilled(ctx context.Context, orgID uuid.UUID) {
	o.emit(ctx, notify.Event{
		OrgID: orgID,
		Class: notify.ClassCost, Kind: notify.KindFleetKilled,
		Title: "The emergency stop was pulled: every agent of this organisation is paused",
		Link:  "/administration",
	})
}

// agentName is the display name, falling back to the id. A mail line that says
// "00000000-…" is worse than none, but not worth a failed notification.
func (o *Orchestrator) agentName(ctx context.Context, agentID uuid.UUID) string {
	if o.Registry == nil {
		return agentID.String()
	}
	agent, err := o.Registry.Get(ctx, agentID)
	if err != nil || agent.DisplayName == "" {
		return agentID.String()
	}
	return agent.DisplayName
}
