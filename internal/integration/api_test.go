package integration

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// apiClient is a logged-in HTTP client (session cookie).
type apiClient struct {
	t    *testing.T
	base string
	http *http.Client
}

func login(t *testing.T, s *stack, email, password string) *apiClient {
	t.Helper()
	jar := newJar()
	c := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: jar}}
	resp := c.do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": email, "password": password})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login failed: HTTP %d", resp.StatusCode)
	}
	resp.Body.Close()
	return c
}

func (c *apiClient) do(method, path string, body any) *http.Response {
	c.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = bytes.NewReader(raw)
	} else {
		reader = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, c.base+path, reader)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	return resp
}

func (c *apiClient) expect(method, path string, body any, wantStatus int) map[string]any {
	c.t.Helper()
	resp := c.do(method, path, body)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		c.t.Fatalf("%s %s: HTTP %d (expected %d): %s", method, path, resp.StatusCode, wantStatus, buf.String())
	}
	var out map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// expectList is expect for endpoints that answer with a list.
func (c *apiClient) expectList(method, path string, body any, wantStatus int) []map[string]any {
	c.t.Helper()
	resp := c.do(method, path, body)
	defer resp.Body.Close()
	if resp.StatusCode != wantStatus {
		buf := new(bytes.Buffer)
		buf.ReadFrom(resp.Body)
		c.t.Fatalf("%s %s: HTTP %d (expected %d): %s", method, path, resp.StatusCode, wantStatus, buf.String())
	}
	var out []map[string]any
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

func TestAPIAndRBAC(t *testing.T) {
	s := newStack(t)

	// Second user with a read-only role (auditor).
	s.mitglied(t, "auditor@test.local", "Auditor", "auditor", "auditor-passwort")

	// Health without auth.
	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(s.http.URL + path)
		if err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("%s must be ok without auth: %v %d", path, err, resp.StatusCode)
		}
		resp.Body.Close()
	}

	// API without login → 401.
	resp, _ := http.Get(s.http.URL + "/api/v1/agents")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Wrong password → 401.
	bad := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: newJar()}}
	r := bad.do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "admin@test.local", "password": "falsch"})
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 for a wrong password, got %d", r.StatusCode)
	}
	r.Body.Close()

	admin := login(t, s, "admin@test.local", "admin-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")

	// The admin creates agent + config + task (M0: visible in the list).
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "api-agent", "display_name": "API-Agent", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)

	admin.expect(http.MethodPut, "/api/v1/agents/"+agentID+"/config",
		map[string]any{"files": map[string]string{"SOUL.md": "# X\n\n## Rolle\nTest."}}, http.StatusOK)

	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/tasks",
		map[string]any{"title": "Test", "body": "[mock:result ok]"}, http.StatusCreated)

	// Auditor: reading yes, writing no (role-scoped views, spec/09).
	auditor.expect(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil, http.StatusOK)
	auditor.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "x", "display_name": "X"}, http.StatusForbidden)
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/kill", nil, http.StatusForbidden)
	auditor.expect(http.MethodPut, "/api/v1/secrets/foo", map[string]string{"value": "bar"}, http.StatusForbidden)

	// Secrets are write-only: PUT ok, the list shows only names plus a limited prefix.
	admin.expect(http.MethodPut, "/api/v1/secrets/zammad_token", map[string]string{"value": "geheim"}, http.StatusOK)
	respKeys := admin.do(http.MethodGet, "/api/v1/secrets", nil)
	var keys []struct {
		Key    string `json:"key"`
		Prefix string `json:"prefix"`
	}
	json.NewDecoder(respKeys.Body).Decode(&keys)
	respKeys.Body.Close()
	if len(keys) != 1 || keys[0].Key != "zammad_token" {
		t.Fatalf("expected secret keys [zammad_token], got %v", keys)
	}
	// "geheim" (6 characters ≤ 12) → fully masked, no prefix.
	if keys[0].Prefix != "" {
		t.Fatalf("a short secret must not show a prefix, got %q", keys[0].Prefix)
	}

	// Custom stages: create, list, move a task (overlay on top of state).
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/stages",
		map[string]string{"name": "Recherche"}, http.StatusForbidden) // read-only role
	stage := admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/stages",
		map[string]string{"name": "Recherche", "color": "var(--text-accent)"}, http.StatusCreated)
	stageID := stage["id"].(string)

	respStages := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/stages", nil)
	var stages []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	json.NewDecoder(respStages.Body).Decode(&stages)
	respStages.Body.Close()
	// The agent starts with the default board (Backlog/In Arbeit/Erledigt);
	// the new column "Recherche" is appended at the end.
	if len(stages) != 4 || stages[len(stages)-1].Name != "Recherche" {
		t.Fatalf("expected stage list [default board + Recherche], got %v", stages)
	}

	// Move one of the agent's tasks into the stage.
	respBacklog := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil)
	var backlogTasks []struct {
		ID      string  `json:"id"`
		StageID *string `json:"stage_id"`
	}
	json.NewDecoder(respBacklog.Body).Decode(&backlogTasks)
	respBacklog.Body.Close()
	if len(backlogTasks) == 0 {
		t.Fatal("expected at least one task in the backlog")
	}
	taskID := backlogTasks[0].ID
	admin.expect(http.MethodPost, "/api/v1/tasks/"+taskID+"/stage",
		map[string]string{"stage_id": stageID}, http.StatusOK)

	respBacklog2 := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/backlog", nil)
	json.NewDecoder(respBacklog2.Body).Decode(&backlogTasks)
	respBacklog2.Body.Close()
	var moved bool
	for _, tk := range backlogTasks {
		if tk.ID == taskID {
			if tk.StageID == nil || *tk.StageID != stageID {
				t.Fatalf("task should be in stage %s, got %v", stageID, tk.StageID)
			}
			moved = true
		}
	}
	if !moved {
		t.Fatal("the moved task could not be found again")
	}

	// Deleting the stage → the task falls back to stage=NULL (nothing is lost).
	admin.expect(http.MethodDelete, "/api/v1/stages/"+stageID, nil, http.StatusNoContent)

	// Create a guardrail (in the MVP RBAC the admin has security rights).
	admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "deny_system", "pattern": "hr*"}, http.StatusCreated)
	auditor.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "deny_system", "pattern": "x"}, http.StatusForbidden)
}

// TestMemoryAdministration: feed memory in manually, change it, delete it —
// stock phrases are rejected, write access only for manage roles.
func TestMemoryAdministration(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "mem-agent", "display_name": "Mem-Agent", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)

	// Feed in manually; content without substance → 400.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"content": "Kunde Meier ist Bestandskunde und wird geduzt"}, http.StatusCreated)
	admin.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"content": "Keine neuen Erkenntnisse"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/agents/"+uuid.NewString()+"/memories",
		map[string]string{"content": "Wissen für einen Geister-Agenten"}, http.StatusNotFound)

	listMemories := func() []map[string]any {
		t.Helper()
		resp := admin.do(http.MethodGet, "/api/v1/agents/"+agentID+"/memories", nil)
		defer resp.Body.Close()
		var entries []map[string]any
		json.NewDecoder(resp.Body).Decode(&entries)
		return entries
	}
	entries := listMemories()
	if len(entries) != 1 {
		t.Fatalf("expected 1 episode, got %d", len(entries))
	}
	memID := entries[0]["id"].(string)

	// Change (incl. re-embedding); a stock-phrase change → 400, unknown ID → 404.
	admin.expect(http.MethodPatch, "/api/v1/memories/"+memID,
		map[string]string{"content": "Kunde Meier wird gesiezt"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/memories/"+memID,
		map[string]string{"content": "nichts neues"}, http.StatusBadRequest)
	admin.expect(http.MethodPatch, "/api/v1/memories/"+uuid.NewString(),
		map[string]string{"content": "gültiger inhalt ohne ziel"}, http.StatusNotFound)
	if entries = listMemories(); entries[0]["content"] != "Kunde Meier wird gesiezt" {
		t.Fatalf("the change did not arrive: %v", entries[0]["content"])
	}

	// A read-only role may read, but not write or delete.
	s.mitglied(t, "auditor@test.local", "Auditor", "auditor", "auditor-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")
	auditor.expect(http.MethodPost, "/api/v1/agents/"+agentID+"/memories",
		map[string]string{"content": "Auditor darf das nicht"}, http.StatusForbidden)
	auditor.expect(http.MethodPatch, "/api/v1/memories/"+memID,
		map[string]string{"content": "Auditor darf das nicht"}, http.StatusForbidden)
	auditor.expect(http.MethodDelete, "/api/v1/memories/"+memID, nil, http.StatusForbidden)

	// Delete; a second delete → 404.
	admin.expect(http.MethodDelete, "/api/v1/memories/"+memID, nil, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/memories/"+memID, nil, http.StatusNotFound)
	if entries = listMemories(); len(entries) != 0 {
		t.Fatalf("memory should be empty, got %d", len(entries))
	}
}

// TestGuardrailAdministration: validate rules, pause them, test them dry —
// the rule tester and the event feed are part of the policy surface.
func TestGuardrailAdministration(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Validation: fail-closed also means not storing rules that have no effect.
	admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "budget_limit"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "yolo", "pattern": "*"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "deny_action", "pattern": "x", "scope_level": "agent"}, http.StatusBadRequest)

	// A budget cap needs no pattern — the default is "*".
	budget := admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "budget_limit", "params": map[string]any{"usd": 12.5}}, http.StatusCreated)
	if budget["pattern"] != "*" {
		t.Fatalf("the budget rule should get pattern *, got %v", budget["pattern"])
	}

	created := admin.expect(http.MethodPost, "/api/v1/guardrails",
		map[string]any{"rule_type": "deny_action", "pattern": "zammad:close_ticket"}, http.StatusCreated)
	ruleID := created["id"].(string)

	// Rule tester: the deny takes hold, the budget cap is reported along with it.
	verdict := admin.expect(http.MethodPost, "/api/v1/guardrails/test",
		map[string]any{"subject": "zammad:close_ticket"}, http.StatusOK)
	if verdict["decision"] != "deny" {
		t.Fatalf("the tester should return deny, got %v", verdict["decision"])
	}
	if verdict["budget_limit_usd"] != 12.5 {
		t.Fatalf("the tester should report budget cap 12.5, got %v", verdict["budget_limit_usd"])
	}

	// Pause instead of delete: the rule stays, but no longer takes hold.
	updated := admin.expect(http.MethodPatch, "/api/v1/guardrails/"+ruleID,
		map[string]any{"enabled": false}, http.StatusOK)
	if updated["enabled"] != false {
		t.Fatalf("the rule should be paused, got %v", updated["enabled"])
	}
	verdict = admin.expect(http.MethodPost, "/api/v1/guardrails/test",
		map[string]any{"subject": "zammad:close_ticket"}, http.StatusOK)
	if verdict["decision"] != "allow" {
		t.Fatalf("a paused rule must not take hold, got %v", verdict["decision"])
	}

	// The event feed answers (empty is fine — nothing has triggered yet).
	resp := admin.do(http.MethodGet, "/api/v1/guardrails/events", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("events feed: HTTP %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Only security roles may toggle; the PATCH lacks the mandatory field → 400.
	admin.expect(http.MethodPatch, "/api/v1/guardrails/"+ruleID, map[string]any{}, http.StatusBadRequest)
}
