package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestTargetActivation: a disabled target system accepts no webhooks and gets no
// credentials (fail-closed); re-enabling restores both — without a restart,
// without a deploy.
func TestTargetActivation(t *testing.T) {
	s := newStack(t)
	zammad := newFakeZammad(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "zammad_token", "tok")
	s.secrets.Put(ctx, s.orgID, "zammad_url", zammad.srv.URL)
	agent := s.newSupportAgent("support")
	s.secrets.Assign(ctx, s.orgID, "zammad_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "zammad_url", agent.ID)

	admin := login(t, s, "admin@test.local", "admin-passwort")

	// The built-in appears in the list; the test org enabled it explicitly (as a
	// real organization would in the UI) — activation is opt-in.
	var plugins []map[string]any
	respList := admin.do(http.MethodGet, "/api/v1/targets", nil)
	json.NewDecoder(respList.Body).Decode(&plugins)
	respList.Body.Close()
	found := false
	for _, p := range plugins {
		if p["name"] == "zammad" && p["kind"] == "builtin" && p["enabled"] == true {
			found = true
		}
	}
	if !found {
		t.Fatalf("the zammad built-in is missing or disabled: %v", plugins)
	}

	// Disable it → the webhook entrance is closed.
	admin.expect(http.MethodPatch, "/api/v1/targets/zammad", map[string]any{"enabled": false}, http.StatusOK)
	body := []byte(`{"ticket":{"id":1,"number":"1","title":"x","state":"open"},"article":{"id":1,"sender":"Customer","body":"hi","internal":false}}`)
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/zammad/support", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature", signWebhook(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("a disabled system has to return 404, got %d", resp.StatusCode)
	}

	// … and the broker refuses credentials: the action fails centrally.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Direct assignment",
		`[mock:action zammad/get_ticket {"ticket_id":1}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task fails on the disabled system", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "not enabled") {
		t.Fatalf("the error should name the deactivation, got %v", got.Error)
	}

	// Re-enable it → the webhook gets through again.
	admin.expect(http.MethodPatch, "/api/v1/targets/zammad", map[string]any{"enabled": true}, http.StatusOK)
	if outcome := postWebhookRaw(t, s, "support", body); !strings.Contains(outcome, "created") {
		t.Fatalf("after re-enabling I expect created, got %s", outcome)
	}

	// Opt-in semantics: a fresh organization starts without enabled target
	// systems — built-ins are on only after an explicit activation.
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]any{
		"name": "Frisch-Org", "admin_email": "frisch@test.local",
		"admin_name": "Frisch-Admin", "admin_password": "frisch-passwort",
	}, http.StatusCreated)
	fresh := login(t, s, "frisch@test.local", "frisch-passwort")
	var freshPlugins []map[string]any
	respFresh := fresh.do(http.MethodGet, "/api/v1/targets", nil)
	json.NewDecoder(respFresh.Body).Decode(&freshPlugins)
	respFresh.Body.Close()
	for _, p := range freshPlugins {
		if p["kind"] == "builtin" && p["enabled"] == true {
			t.Fatalf("a fresh organization must not have an enabled built-in: %v", p)
		}
	}
}

// fakeHelpdesk is a non-Zammad target system for the manifest plugin.
type fakeHelpdesk struct {
	mu       sync.Mutex
	requests []string
	apiKeys  []string
	srv      *httptest.Server
}

func newFakeHelpdesk(t *testing.T) *fakeHelpdesk {
	f := &fakeHelpdesk{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r.Method+" "+r.URL.Path)
		f.apiKeys = append(f.apiKeys, r.Header.Get("X-API-Key"))
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"id": 7, "title": "Drucker brennt"})
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeHelpdesk) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// TestCustomManifestPlugin: an uploaded manifest wires up a new target system
// completely — webhook entrance, brokered credentials and actions through the
// generic REST engine — without a line of Go code.
func TestCustomManifestPlugin(t *testing.T) {
	s := newStack(t)
	helpdesk := newFakeHelpdesk(t)
	ctx := context.Background()

	admin := login(t, s, "admin@test.local", "admin-passwort")
	manifest := map[string]any{
		"name":  "helpdesk",
		"label": "Helpdesk",
		"auth":  map[string]any{"header": "X-API-Key", "format": "{token}"},
		"webhook": map[string]any{
			"id_field":       "issue.id",
			"event_id_field": "comment.id",
			"title_field":    "issue.title",
			"body_field":     "comment.text",
			"ignore_when":    []map[string]any{{"field": "comment.author_type", "equals": "agent"}},
		},
		"actions": map[string]any{
			"get_issue": map[string]any{"method": "GET", "path": "/issues/{issue_id}"},
			"comment":   map[string]any{"method": "POST", "path": "/issues/{issue_id}/comments"},
		},
	}
	admin.expect(http.MethodPost, "/api/v1/targets", manifest, http.StatusOK)

	// An agent with access to the new system; secrets follow the <system>_token/_url convention.
	agent, err := s.registry.Create(ctx, s.orgID, "helper", "Helpdesk-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Helpdesk-Agent",
		"ACCESS.md": "- system: helpdesk scope: read,write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	s.secrets.Put(ctx, s.orgID, "helpdesk_token", "hd-secret-token")
	s.secrets.Put(ctx, s.orgID, "helpdesk_url", helpdesk.srv.URL)
	s.secrets.Assign(ctx, s.orgID, "helpdesk_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "helpdesk_url", agent.ID)

	// The new system's webhook wakes the agent; the mock directives inside the
	// comment drive the actions through the generic engine.
	payload := []byte(`{"issue":{"id":7,"title":"Drucker brennt"},
		"comment":{"id":3,"author_type":"customer","text":"Es qualmt! [mock:action helpdesk/get_issue {\"issue_id\":7}] [mock:action helpdesk/comment {\"issue_id\":7,\"text\":\"Feuerwehr ist unterwegs.\"}] [mock:result Brand gemeldet]"}}`)
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/helpdesk/helper", bytes.NewReader(payload))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(buf.String(), "created") {
		t.Fatalf("helpdesk webhook: HTTP %d %s", resp.StatusCode, buf.String())
	}

	waitFor(t, "helpdesk task done", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		return len(tasks) == 1 && tasks[0].State == backlog.StateDone
	})

	calls := helpdesk.calls()
	if len(calls) != 2 || calls[0] != "GET /issues/7" || calls[1] != "POST /issues/7/comments" {
		t.Fatalf("wrong engine calls: %v", calls)
	}
	helpdesk.mu.Lock()
	key := helpdesk.apiKeys[0]
	helpdesk.mu.Unlock()
	if key != "hd-secret-token" {
		t.Fatalf("the brokered token is missing from the auth header: %q", key)
	}

	// Custom plugins can be deleted — after that the entrance is closed.
	admin.expect(http.MethodDelete, "/api/v1/targets/helpdesk", nil, http.StatusOK)
	req, _ = http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/helpdesk/helper", bytes.NewReader(payload))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("after the deletion I expect 404, got %d", resp.StatusCode)
	}
}

// TestAgentSystemsView checks the agent view of the target systems
// (GET /agents/{id}/systems, spec/02): access from ACCESS.md, org-wide
// activation and the action list in the wording of the system prompt, all in one
// place — the answer to "what can this agent do where?".
func TestAgentSystemsView(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("systemsicht") // ACCESS.md: zammad read,write
	admin := login(t, s, "admin@test.local", "admin-passwort")

	var systems []map[string]any
	resp := admin.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/systems", nil)
	json.NewDecoder(resp.Body).Decode(&systems)
	resp.Body.Close()

	var zammad, teams map[string]any
	for _, sys := range systems {
		switch sys["name"] {
		case "zammad":
			zammad = sys
		case "teams":
			teams = sys
		}
	}
	if zammad == nil || teams == nil {
		t.Fatalf("zammad and teams have to be listed: %v", systems)
	}

	// The access from ACCESS.md is in there — including scopes and actions.
	if zammad["access"] != true || zammad["enabled"] != true {
		t.Fatalf("zammad has to show access and activation: %v", zammad)
	}
	scopes, _ := zammad["scopes"].([]any)
	if len(scopes) != 2 {
		t.Fatalf("the scopes from ACCESS.md are missing: %v", zammad["scopes"])
	}
	doc, _ := zammad["doc"].(string)
	if !strings.Contains(doc, "get_ticket") {
		t.Fatalf("the action list has to match the prompt documentation: %q", doc)
	}

	// Enabled but without access: visible, and the difference is readable.
	if teams["access"] == true || teams["enabled"] != true {
		t.Fatalf("teams is enabled but has no access in ACCESS.md: %v", teams)
	}
	// Systems with access come first — the UI shows them first.
	if systems[0]["access"] != true {
		t.Fatalf("systems with access belong at the front: %v", systems[0])
	}

	// A disabled target system has no actions: fail-closed applies to the display
	// as well, otherwise something disabled reads like something available.
	admin.expect(http.MethodPatch, "/api/v1/targets/zammad", map[string]any{"enabled": false}, http.StatusOK)
	// A fresh target slice: decoded into a filled one, encoding/json mixes the old
	// map keys with the new ones — the old state would then stand in the "new"
	// result.
	var nachher []map[string]any
	resp = admin.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/systems", nil)
	json.NewDecoder(resp.Body).Decode(&nachher)
	resp.Body.Close()
	for _, sys := range nachher {
		if sys["name"] == "zammad" && sys["doc"] != nil {
			t.Fatalf("a disabled system must not show any actions: %v", sys)
		}
	}
}
