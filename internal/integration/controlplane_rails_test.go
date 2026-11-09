package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"covey/internal/daemon"
)

// TestControlPlaneEnforcesRailsOnCreateTask is #703: for covey:create_task
// and covey:message only the daemon asked the guard rails, so a request sent
// without that question created work past every rule. The handlers are
// called here the way a daemon that skips its check would reach them.
func TestControlPlaneEnforcesRailsOnCreateTask(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")
	parent, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Elternaufgabe", "", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// No rule: the request goes through, as before.
	if resp := s.orch.CreateAgentTask(ctx, agent, parent.ID, daemon.RequestCreateTask{Title: "erlaubt"}); !resp.OK {
		t.Fatalf("without a rule the task has to be created: %s", resp.Error)
	}

	// A deny: refused by the control plane itself.
	if _, err := s.rails.Create(ctx, railRule(s.orgID, "deny_action", "covey:create_task")); err != nil {
		t.Fatal(err)
	}
	resp := s.orch.CreateAgentTask(ctx, agent, parent.ID, daemon.RequestCreateTask{Title: "verboten"})
	if resp.OK || !strings.Contains(resp.Error, "guard rail") {
		t.Fatalf("a deny_action rule has to hold without the daemon's check: %+v", resp)
	}
}

// TestControlPlaneWantsTheApproval: with require_approval, a request that
// carries no consumed approval is refused, and an approval works once.
func TestControlPlaneWantsTheApproval(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("support")
	parent, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Elternaufgabe", "", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.rails.Create(ctx, railRule(s.orgID, "require_approval", "covey:message")); err != nil {
		t.Fatal(err)
	}
	resp := s.orch.MessageHuman(ctx, agent, parent.ID, daemon.RequestMessage{Text: "Hallo"})
	if resp.OK || !strings.Contains(resp.Error, "approval") {
		t.Fatalf("a message without an approval has to be refused: %+v", resp)
	}

	// A granted approval the daemon's check consumed is accepted — once.
	appr, err := s.obs.CreateApproval(ctx, s.orgID, agent.ID, &parent.ID, "covey:message", json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.obs.DecideApproval(ctx, s.orgID, appr.ID, true, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE approvals SET used = TRUE WHERE id = $1`, appr.ID); err != nil {
		t.Fatal(err)
	}
	req := daemon.RequestMessage{Text: "Hallo", ApprovalID: appr.ID.String()}
	if resp := s.orch.MessageHuman(ctx, agent, parent.ID, req); strings.Contains(resp.Error, "approval") {
		t.Fatalf("the consumed approval has to be accepted: %+v", resp)
	}
	if resp := s.orch.MessageHuman(ctx, agent, parent.ID, req); !strings.Contains(resp.Error, "approval") {
		t.Fatalf("the same approval a second time has to be refused: %+v", resp)
	}
}
