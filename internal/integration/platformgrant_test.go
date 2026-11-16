package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestPlatformAccessIsASecurityGrant: giving an agent the system covey is a
// security role's decision, like a tool allowlist or an egress host. An agent
// holding `covey agents:write` switches target systems on and widens a
// colleague's egress, and both direct routes are org_admin/security only — so
// a manage seat may not reach the same state by writing the grant into
// ACCESS.md, by importing a bundle that carries it, or by creating the People
// department that ships with it.
func TestPlatformAccessIsASecurityGrant(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	s.mitglied(t, "owner@test.local", "Owner", "agent_owner", "owner-passwort")
	owner := login(t, s, "owner@test.local", "owner-passwort")

	created := owner.expect(http.MethodPost, "/api/v1/agents",
		map[string]any{"slug": "helfer", "display_name": "Helfer", "runtime": "mock"}, http.StatusCreated)
	aid := created["id"].(string)
	grant := map[string]string{
		"SOUL.md":   "# Helfer",
		"ACCESS.md": "- system: covey scope: agents:write",
	}

	// The text editor.
	owner.expect(http.MethodPut, "/api/v1/agents/"+aid+"/config",
		map[string]any{"files": grant}, http.StatusForbidden)
	// The config import of an existing agent.
	bundle := func(slug string) map[string]any {
		return map[string]any{"kind": "covey.agent-config", "version": 1,
			"agent": map[string]any{"slug": slug, "display_name": "Import", "runtime": "mock"},
			"files": grant}
	}
	owner.expect(http.MethodPost, "/api/v1/agents/"+aid+"/config/import", bundle("helfer"), http.StatusForbidden)
	// The bundle import that creates an agent.
	owner.expect(http.MethodPost, "/api/v1/agents/import", bundle("import-helfer"), http.StatusForbidden)
	if _, err := s.registry.GetBySlug(ctx, s.orgID, "import-helfer"); err == nil {
		t.Fatal("a refused import must not leave an agent behind")
	}
	if ok, _ := s.registry.HasAccess(ctx, mustUUID(t, aid), "covey", "agents:write"); ok {
		t.Fatal("the refused writes gave the agent the system covey anyway")
	}

	// org_admin grants it as before.
	admin.expect(http.MethodPut, "/api/v1/agents/"+aid+"/config",
		map[string]any{"files": grant}, http.StatusOK)
	if ok, _ := s.registry.HasAccess(ctx, mustUUID(t, aid), "covey", "agents:write"); !ok {
		t.Fatal("org_admin has to be able to grant the system covey")
	}

	// The owner still edits the agent: an unchanged grant is no grant…
	owner.expect(http.MethodPut, "/api/v1/agents/"+aid+"/config", map[string]any{"files": map[string]string{
		"SOUL.md":   "# Helfer, neu",
		"ACCESS.md": "- system: covey scope: agents:write",
	}}, http.StatusOK)
	// …but a further scope on it is.
	owner.expect(http.MethodPut, "/api/v1/agents/"+aid+"/config", map[string]any{"files": map[string]string{
		"SOUL.md":   "# Helfer, neu",
		"ACCESS.md": "- system: covey scope: agents:write, agents:review",
	}}, http.StatusForbidden)

	// The People department carries the grant, so creating it is the same
	// decision. Once it exists, the card reads it for every manage seat.
	owner.expect(http.MethodPost, "/api/v1/setup/people", map[string]any{"onboard": false}, http.StatusForbidden)
	if _, err := s.registry.GetBySlug(ctx, s.orgID, "people"); err == nil {
		t.Fatal("a refused setup must not leave a People department behind")
	}
	admin.expect(http.MethodPost, "/api/v1/setup/people", map[string]any{"onboard": false}, http.StatusCreated)
	if again := owner.expect(http.MethodPost, "/api/v1/setup/people", map[string]any{}, http.StatusOK); again["existed"] != true {
		t.Fatalf("an existing People department is returned to a manage seat: %v", again)
	}
}

// TestSecurityOwnedApprovalIsDecidedBySecurity: a require_approval rule on
// covey:add_egress_host keeps the decision with the roles that may add the
// host directly. The decide route is open to the manage roles; for this
// subject an agent_owner is refused, and org_admin decides as before.
func TestSecurityOwnedApprovalIsDecidedBySecurity(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	ownerID := s.mitglied(t, "owner@test.local", "Owner", "agent_owner", "owner-passwort")
	owner := login(t, s, "owner@test.local", "owner-passwort")

	drafter, err := s.registry.Create(ctx, s.orgID, "personal", "Personal", "mock", &ownerID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, drafter.ID, map[string]string{
		"SOUL.md":   "# Personal",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	kollege := s.newSupportAgent("kollege")
	if _, err := s.rails.Create(ctx, guardrails.Rule{
		OrgID: s.orgID, ScopeLevel: "global", RuleType: guardrails.RuleRequireApproval,
		Pattern: "covey:add_egress_host", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, drafter.ID, "Host freigeben",
		`[mock:action covey/add_egress_host {"agent":"kollege","host":"docs.example.org","note":"Handbuch"}]`,
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the task waits for the approval", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateBlocked
	})
	approvals := admin.expectList(http.MethodGet, "/api/v1/approvals?status=pending", nil, http.StatusOK)
	if len(approvals) != 1 || approvals[0]["action"] != "covey:add_egress_host" {
		t.Fatalf("exactly one covey:add_egress_host approval expected: %v", approvals)
	}
	apprID := approvals[0]["id"].(string)

	owner.expect(http.MethodPost, "/api/v1/approvals/"+apprID+"/decide",
		map[string]any{"approve": true}, http.StatusForbidden)
	owner.expect(http.MethodPost, "/api/v1/approvals/"+apprID+"/decide",
		map[string]any{"approve": false}, http.StatusForbidden)
	if cfg, _ := s.egress.AgentConfig(ctx, kollege.ID); len(cfg.Hosts) != 0 {
		t.Fatalf("the host arrived without a security role's decision: %+v", cfg.Hosts)
	}

	admin.expect(http.MethodPost, "/api/v1/approvals/"+apprID+"/decide",
		map[string]any{"approve": true}, http.StatusOK)
	waitFor(t, "the task finishes after the approval", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	cfg, err := s.egress.AgentConfig(ctx, kollege.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Hosts) != 1 || cfg.Hosts[0].Pattern != "docs.example.org" {
		t.Fatalf("after org_admin's approval the host belongs on the allowlist: %+v", cfg.Hosts)
	}
}
