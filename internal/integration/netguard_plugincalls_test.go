package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"covey/internal/netguard"
)

// refuseLoopback puts netguard back on the production default — no operator
// exception — for the rest of the test. The stack allows loopback for its
// fake target systems; that is restored afterwards so other tests keep it.
func refuseLoopback(t *testing.T) {
	t.Helper()
	if err := netguard.SetAllow(""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = netguard.SetAllow("127.0.0.0/8,::1") })
}

// TestControlPlanePluginCallsRefuseHostDestinations pins #692 for the plugin
// calls the control plane makes on its own: a workflow action step (ActAs), a
// gate or nur-wenn heartbeat (HasWork) and a platform issue. Each uses the
// organisation's <system>_url, so each has to refuse a destination on the
// host itself the way the connection probe does — before, only the probe and
// the daily credential check carried the guard, and the others reached a
// loopback service with the brokered token.
func TestControlPlanePluginCallsRefuseHostDestinations(t *testing.T) {
	trackerState.Lock()
	trackerState.filed = nil
	trackerState.Unlock()

	s := newStack(t)
	refuseLoopback(t)
	ctx := context.Background()

	var hits atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"key": "ACME-17", "issues": []any{map[string]any{"key": "ACME-17"}}})
	}))
	t.Cleanup(host.Close)

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPatch, "/api/v1/targets/jira", map[string]any{"enabled": true}, http.StatusOK)
	agent, err := s.registry.Create(ctx, s.orgID, "wf-actor", "Workflow actor", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md": "# Actor", "ACCESS.md": "- system: jira scope: read",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"jira_url":   host.URL + ` project="ACME"`,
		"jira_token": "bot@example.org:dummy-token",
	} {
		if err := s.secrets.Put(ctx, s.orgID, k, v); err != nil {
			t.Fatal(err)
		}
		if err := s.secrets.Assign(ctx, s.orgID, k, agent.ID); err != nil {
			t.Fatal(err)
		}
	}

	// A workflow action step.
	params, _ := json.Marshal(map[string]string{"issue_key": "ACME-17"})
	if _, err := s.orch.ActAs(ctx, agent, "jira", "get_issue", params, ""); err == nil ||
		!strings.Contains(err.Error(), "destination refused") {
		t.Fatalf("a workflow action step must refuse a loopback base URL, got %v", err)
	}

	// A workflow gate — the same check as a nur-wenn heartbeat. A refused
	// check fires, as every failed check does; it just never reaches the host.
	s.orch.HasWork(ctx, s.orgID, agent.ID, "jira")

	if n := hits.Load(); n != 0 {
		t.Fatalf("the control plane reached a loopback destination %d time(s)", n)
	}

	// A platform issue, filed with the organisation's own account.
	admin.expect(http.MethodPatch, "/api/v1/targets/trackertest", map[string]any{"enabled": true}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/org/platform-repo",
		map[string]any{"system": "trackertest", "project": "gruppe/covey"}, http.StatusOK)
	if err := s.secrets.Put(ctx, s.orgID, "trackertest_token", "bot-token"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "trackertest_url", "http://127.0.0.1:9"); err != nil {
		t.Fatal(err)
	}
	doctor := reviewAgent(t, s, "covey-doctor", "agents:review")
	_, msg := laufLassen(t, s, doctor, "Befund melden",
		`[mock:action covey/create_issue {"title":"Turn limit kills partial results","body":"Eleven runs."}]
[mock:result gemeldet]`)
	if n := len(filedIssues()); n != 0 {
		t.Fatalf("a platform issue must not be filed against a loopback base URL (%d filed): %s", n, msg)
	}
	if !strings.Contains(msg, "destination refused") {
		t.Fatalf("the agent is told why the issue was not filed: %s", msg)
	}
}
