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

// TestTargetActivation: Ein deaktiviertes Zielsystem nimmt keine Webhooks an
// und bekommt keine Credentials (fail-closed); Reaktivierung stellt beides
// wieder her — ohne Neustart, ohne Deploy.
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

	// Das Built-in erscheint in der Liste, ohne Zeile gilt es als aktiviert.
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
		t.Fatalf("zammad-built-in fehlt oder deaktiviert: %v", plugins)
	}

	// Deaktivieren → der Webhook-Eingang ist zu.
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
		t.Fatalf("deaktiviertes system muss 404 liefern, got %d", resp.StatusCode)
	}

	// … und der Broker verweigert Credentials: die Aktion scheitert zentral.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Direktauftrag",
		`[mock:action zammad/get_ticket {"ticket_id":1}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe scheitert am deaktivierten system", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateFailed
	})
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "nicht aktiviert") {
		t.Fatalf("fehler soll die deaktivierung nennen, got %v", got.Error)
	}

	// Reaktivieren → der Webhook geht wieder durch.
	admin.expect(http.MethodPatch, "/api/v1/targets/zammad", map[string]any{"enabled": true}, http.StatusOK)
	if outcome := postWebhookRaw(t, s, "support", body); !strings.Contains(outcome, "created") {
		t.Fatalf("nach reaktivierung erwarte ich created, got %s", outcome)
	}
}

// fakeHelpdesk ist ein Nicht-Zammad-Zielsystem für das Manifest-Plugin.
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

// TestCustomManifestPlugin: Ein hochgeladenes Manifest bindet ein neues
// Zielsystem komplett an — Webhook-Eingang, gebrokerte Credentials und
// Aktionen über die generische REST-Engine — ohne eine Zeile Go-Code.
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

	// Agent mit Zugang zum neuen System; Secrets nach der <system>_token/_url-Konvention.
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

	// Webhook des neuen Systems weckt den Agenten; die Mock-Direktiven im
	// Kommentar treiben die Aktionen über die generische Engine.
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
		t.Fatalf("helpdesk-webhook: HTTP %d %s", resp.StatusCode, buf.String())
	}

	waitFor(t, "helpdesk-aufgabe done", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		return len(tasks) == 1 && tasks[0].State == backlog.StateDone
	})

	calls := helpdesk.calls()
	if len(calls) != 2 || calls[0] != "GET /issues/7" || calls[1] != "POST /issues/7/comments" {
		t.Fatalf("engine-aufrufe falsch: %v", calls)
	}
	helpdesk.mu.Lock()
	key := helpdesk.apiKeys[0]
	helpdesk.mu.Unlock()
	if key != "hd-secret-token" {
		t.Fatalf("gebrokertes token fehlt im auth-header: %q", key)
	}

	// Custom-Plugins lassen sich löschen — danach ist der Eingang zu.
	admin.expect(http.MethodDelete, "/api/v1/targets/helpdesk", nil, http.StatusOK)
	req, _ = http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/helpdesk/helper", bytes.NewReader(payload))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("nach löschen erwarte ich 404, got %d", resp.StatusCode)
	}
}
