package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/guardrails"
	"covey/internal/observability"
	"covey/internal/reqlog"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Workflows (#566) start from the triggers agents already have and act
// through the actions agents already use. These two methods are the doors:
// the heartbeat's gate, and an action run as a colleague.

// HasWork evaluates a gate (`nur-wenn` / `only_if`) exactly as for an
// agent's heartbeat — the plugin's work check under the colleague's
// credential, fail-open on a broken check, and the work signature.
func (o *Orchestrator) HasWork(ctx context.Context, orgID, agentID uuid.UUID, condition string) (bool, string) {
	return o.heartbeatHasWork(ctx, agentID, orgID, condition)
}

// ActAs runs a target-system action as agent, under its rules: the tool
// assignment, the guard rails on the action's subject, the credential
// broker (ACCESS.md, activation, the system deny) and the audit. A rule that
// requires approval creates the same approval an agent's action would;
// ActAs then returns its id, and is called again with it until the approval
// is decided — granted, it is used and the action runs; refused, ActAs
// says so.
//
// The style gate stays with agents: it measures text an agent wrote, and a
// workflow only carries out what its definition says.
func (o *Orchestrator) ActAs(ctx context.Context, agent agents.Agent, system, action string, params json.RawMessage, pending string) (string, error) {
	sys, err := o.system(ctx, agent.OrgID, system)
	if err != nil {
		return "", fmt.Errorf("target system %s: %w", system, err)
	}
	subject := sys.ActionSubject(action, params)
	if o.Targets != nil {
		allowed, err := o.Targets.AgentToolAllowed(ctx, agent.ID, system, action)
		if err != nil {
			return "", errors.New("tool assignment not readable (fail-closed)")
		}
		if !allowed {
			_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindGuardrail,
				map[string]any{"rule": "tool_not_assigned", "action": subject, "decision": "denied", "via": "workflow"})
			return "", fmt.Errorf("tool %s is not assigned to %s", action, agent.Slug)
		}
	}
	rules, err := o.Rails.List(ctx, agent.OrgID)
	if err != nil {
		return "", errors.New("guard rails not readable (fail-closed)")
	}
	switch v := guardrails.Evaluate(rules, agent.ID, subject); v.Decision {
	case guardrails.Deny:
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindGuardrail,
			map[string]any{"rule": v.Rule.RuleType, "pattern": v.Rule.Pattern, "action": subject, "decision": "denied", "via": "workflow"})
		return "", fmt.Errorf("forbidden by guard rail %s", v.Rule.Pattern)
	case guardrails.RequireApproval:
		// An approval asked for earlier is answered first: still open, the
		// workflow waits; refused, it stops. Only a granted one goes on to
		// the gate, which uses it.
		if id, err := uuid.Parse(pending); err == nil {
			if appr, err := o.Obs.GetApproval(ctx, agent.OrgID, id); err == nil {
				switch appr.Status {
				case "pending":
					return pending, nil
				case "denied":
					return "", fmt.Errorf("the approval for %s was refused", subject)
				}
			}
		}
		g := o.approvalGate(ctx, agent, nil, subject, params, "")
		if g.Error != "" {
			return "", errors.New(g.Error)
		}
		if !g.Approved {
			return g.ApprovalID, nil
		}
	}
	cred := o.brokerCredential(ctx, agent, daemon.RequestCredential{RequestID: uuid.NewString(), System: system})
	if !cred.Granted {
		return "", fmt.Errorf("no credential for %s as %s: %s", system, agent.Slug, cred.Reason)
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if o.ReqLog != nil {
		cctx = reqlog.WithSink(cctx, o.ReqLog.Sink(&agent.OrgID, &agent.ID, nil))
	}
	_, err = sys.Execute(cctx, action, params, target.Credential{BaseURL: cred.BaseURL, Token: cred.Token, CA: cred.CA})
	record := map[string]any{"action": subject, "via": "workflow", "ok": err == nil}
	if err != nil {
		record["error"] = err.Error()
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindAction, record)
	return "", err
}
