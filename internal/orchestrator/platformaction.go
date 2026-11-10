package orchestrator

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
)

// platformAction runs a target-system action in the control plane (#710).
//
// The daemon has asked the guard rails already, and that is a courtesy: a
// message on this protocol can come from whatever speaks it. So everything
// that decides is checked again here — the system is switched on for the
// organisation, ACCESS.md grants it with the action's scope, and the rails
// allow the subject (an approval the daemon's check consumed is redeemed once,
// as for create_task).
func (o *Orchestrator) platformAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestPlatformAction) daemon.InjectPlatformAction {
	fail := func(msg string) daemon.InjectPlatformAction {
		return daemon.InjectPlatformAction{RequestID: req.RequestID, Error: msg}
	}
	impl, ok := o.Platform[req.System]
	if !ok || impl == nil {
		return fail(req.System + " is not available on this installation")
	}
	sys, err := o.system(ctx, agent.OrgID, req.System)
	if err != nil {
		return fail(req.System + " is not switched on for this organisation")
	}
	if has, err := o.Registry.HasAccess(ctx, agent.ID, req.System, ""); err != nil || !has {
		return fail("this agent has no access to " + req.System + " (`- system: " + req.System + "` in ACCESS.md)")
	}
	if need, ok := o.scopeAllows(ctx, agent, req.System, req.Action); !ok {
		return fail(req.System + ":" + req.Action + " needs scope " + need + " in ACCESS.md")
	}
	subject := sys.ActionSubject(req.Action, req.Params)
	if reason := o.enforceRails(ctx, agent, taskID, subject, req.ApprovalID); reason != "" {
		return fail(reason)
	}
	out, err := impl.Execute(ctx, agent, taskID, req.Action, req.Params)
	if err != nil {
		return fail(err.Error())
	}
	data, err := json.Marshal(out)
	if err != nil {
		return fail(err.Error())
	}
	return daemon.InjectPlatformAction{RequestID: req.RequestID, OK: true, Data: data}
}
