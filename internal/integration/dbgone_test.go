package integration

import (
	"context"
	"io"
	"net/http"
	"testing"

	"covey/internal/db"
)

// What the API does when the database goes away.
//
// This is an operator's question, not a theoretical one: a failover, a
// connection limit reached, a network partition between the control plane and
// its Postgres. Every handler has a branch for it — `if err != nil { mapErr }`
// — and until now not one of them had ever run. What this test holds is that
// the branch exists everywhere and behaves: an error, not a panic, not a hang,
// and not a 200 with an empty list that would read as "there is nothing here".
//
// The last one is the reason it is worth a test at all. A listing endpoint that
// swallowed the error and answered `[]` would tell an operator their agents are
// gone.
func TestTheAPIWhenTheDatabaseIsGone(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("waise")
	a := "/api/v1/agents/" + agent.ID.String()
	_, runnerToken := s.builtinToken(t, s.orgID)

	// From here on nothing can read or write. The database is taken away
	// UNDER the running server — its backends are terminated and it refuses
	// new connections — which is as close to a failover or a network partition
	// as a test gets without a second machine.
	//
	// Deliberately not pool.Close(): that waits for every acquired connection
	// to come back, and the orchestrator holds one.
	takeTheDatabaseAway(t, s)

	paths := []string{
		"/api/v1/agents",
		"/api/v1/audit",
		"/api/v1/departments",
		"/api/v1/org",
		"/api/v1/org/chart",
		"/api/v1/org/profile-fields",
		"/api/v1/users",
		"/api/v1/runtime-instances",
		"/api/v1/runners",
		"/api/v1/templates",
		"/api/v1/skills",
		"/api/v1/secrets",
		"/api/v1/guardrails",
		"/api/v1/guardrails/events",
		"/api/v1/egress",
		"/api/v1/egress/templates",
		"/api/v1/egress/log",
		"/api/v1/egress/stats",
		"/api/v1/egress/builtin",
		"/api/v1/improvements",
		"/api/v1/inbox",
		"/api/v1/approvals",
		"/api/v1/cost/org",
		"/api/v1/cost/runs",
		"/api/v1/cost/indicators",
		"/api/v1/fleet",
		"/api/v1/targets",
		"/api/v1/workplaces",
		"/api/v1/onboarding",
		"/api/v1/setup/state",
		"/api/v1/auth/sessions",
		"/api/v1/auth/api-keys",
		"/api/v1/auth/notifications",
		"/api/v1/auth/me/profile",
		a,
		a + "/config",
		a + "/backlog",
		a + "/stages",
		a + "/heartbeats",
		a + "/memories",
		a + "/wiki/log",
		a + "/wiki/health",
		a + "/dreams",
		a + "/recording",
		a + "/cost",
		a + "/cost/series",
		a + "/cost/runs",
		a + "/secrets",
		a + "/skills",
		a + "/systems",
		a + "/egress",
		a + "/export",
		a + "/lint",
		a + "/work-record",
		a + "/reviews",
		a + "/diagnostics",
		a + "/webhook",
		a + "/placement",
	}

	writes := []struct {
		method, path string
		body         any
	}{
		{http.MethodPost, "/api/v1/agents", map[string]any{"slug": "neu", "display_name": "Neu", "runtime": "mock"}},
		{http.MethodDelete, a, nil},
		{http.MethodPatch, a + "/name", map[string]any{"display_name": "Anders"}},
		{http.MethodPatch, a + "/effort", map[string]any{"effort": ""}},
		{http.MethodPatch, a + "/max-turns", map[string]any{"max_turns": 10}},
		{http.MethodPatch, a + "/recording-level", map[string]any{"level": "full"}},
		{http.MethodPatch, a + "/warm-sandbox", map[string]any{"warm": true}},
		{http.MethodPatch, a + "/runner-tags", map[string]any{"runner_tags": []string{}}},
		{http.MethodPatch, a + "/services", map[string]any{"services": []any{}}},
		{http.MethodPatch, a + "/slug", map[string]any{"slug": "anders"}},
		{http.MethodPost, a + "/budget", map[string]any{"budget_usd": 1}},
		{http.MethodPost, a + "/tasks", map[string]any{"title": "Etwas"}},
		{http.MethodPost, a + "/stages", map[string]any{"name": "Neu"}},
		{http.MethodPost, a + "/memories", map[string]any{"slug": "x", "title": "X", "content": "Ein ganzer Satz dazu."}},
		{http.MethodPost, a + "/wiki/consolidate", nil},
		{http.MethodPost, a + "/kill", map[string]any{"killed": true}},
		{http.MethodPost, a + "/resume", nil},
		{http.MethodPut, a + "/files/content", map[string]any{"path": "x.md", "content": "x"}},
		{http.MethodPost, "/api/v1/departments", map[string]any{"name": "Neu"}},
		{http.MethodPost, "/api/v1/org/profile-fields", map[string]any{"label": "Ort"}},
		{http.MethodPatch, "/api/v1/org/description", map[string]any{"description": "x"}},
		{http.MethodPost, "/api/v1/users", map[string]any{"email": "x@test.local", "display_name": "X", "role": "auditor", "password": "ein-langes-passwort"}},
		{http.MethodPost, "/api/v1/runtime-instances", map[string]any{"engine": "mock", "display_name": "Sitz"}},
		{http.MethodPost, "/api/v1/egress/templates", map[string]any{"name": "T"}},
		{http.MethodPost, "/api/v1/egress/defaults", map[string]any{"pattern": "*.example.test"}},
		{http.MethodPut, "/api/v1/secrets/irgendein_token", map[string]any{"value": "v"}},
		{http.MethodPost, "/api/v1/runners/registration-tokens", map[string]any{"description": "x"}},
		{http.MethodPatch, "/api/v1/org/recording-level", map[string]any{"level": "full"}},
		{http.MethodPost, "/api/v1/fleet/kill", map[string]any{"killed": true}},
	}
	for _, w := range writes {
		resp := admin.do(w.method, w.path, w.body)
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		if resp.StatusCode < 400 {
			t.Errorf("%s %s answered HTTP %d without a database: %s",
				w.method, w.path, resp.StatusCode, truncate(string(raw), 120))
		}
	}

	// The runner API is the data plane's own way in, and a runner that reaches
	// a control plane without a database must be told so rather than handed an
	// empty allowlist — which would read as "this agent may reach nothing" and
	// is a different statement from "I cannot say".
	for _, path := range []string{
		"/api/runner/v1/whoami",
		"/api/runner/v1/egress/allowlist?agent=" + agent.ID.String(),
	} {
		code, body := s.runnerGET(t, path, runnerToken)
		if code < 400 {
			t.Errorf("runner GET %s answered HTTP %d without a database: %s", path, code, truncate(string(body), 120))
		}
	}
	if code, body := s.runnerPOST(t, "/api/runner/v1/register", "", map[string]any{"token": "x"}); code < 400 {
		t.Errorf("runner register answered HTTP %d without a database: %s", code, truncate(string(body), 120))
	}

	// And the public endpoints, which are the ones a stranger reaches: they
	// must not answer "this address is free" because the database is away.
	for _, path := range []string{
		"/api/v1/public/signup", "/api/v1/public/password-reset",
		"/api/v1/public/verify/resend",
	} {
		resp := s.postJSON(t, path, map[string]any{
			"email": "jemand@example.test", "display_name": "Jemand",
			"password": "ein-langes-passwort",
		})
		code := resp.StatusCode
		resp.Body.Close()
		if code < 400 {
			t.Errorf("public POST %s answered HTTP %d without a database", path, code)
		}
	}

	for _, path := range paths {
		resp := admin.do(http.MethodGet, path, nil)
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		switch {
		case resp.StatusCode >= 500:
			// The expected answer: something is broken here, and it is.
		case resp.StatusCode == http.StatusServiceUnavailable:
			// Also honest: a part that is not wired.
		case resp.StatusCode == http.StatusUnauthorized:
			// The session itself is read from the database, so an endpoint
			// behind it may fail at the door rather than in the handler.
		default:
			t.Errorf("GET %s answered HTTP %d without a database: %s",
				path, resp.StatusCode, truncate(string(raw), 120))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// takeTheDatabaseAway makes this test's database unreachable for the server
// that is still running against it.
func takeTheDatabaseAway(t *testing.T, s *stack) {
	t.Helper()
	ctx := context.Background()
	admin, err := db.Connect(ctx, adminDBURL)
	if err != nil {
		t.Skipf("no admin connection to take the database away: %v", err)
	}
	defer admin.Close()

	var name string
	if err := s.pool.QueryRow(ctx, "SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	// Dropped, not merely blocked: a CONNECTION LIMIT does not apply to the
	// superuser this test connects as, so the pool would simply reconnect and
	// the database would never have been away at all.
	if _, err := admin.Exec(ctx, `DROP DATABASE "`+name+`" WITH (FORCE)`); err != nil {
		t.Fatal(err)
	}
}
