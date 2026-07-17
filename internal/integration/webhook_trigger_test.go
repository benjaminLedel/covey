package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

// triggerPost feuert den öffentlichen Trigger-Endpoint ohne Session-Auth —
// so wie ein Fremdsystem (CI, Cron, Zapier) es täte.
func triggerPost(t *testing.T, url, body string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestAgentWebhookTrigger prüft den optionalen generischen Webhook-Trigger aus
// spec/03: Verwaltung nur für Manager-Rollen, Auslösen per Token, Idempotenz
// über dedup_key, Roh-Text-Fallback, Token-Rotation und fail-closed bei Kill.
func TestAgentWebhookTrigger(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "hook", "Hook-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/agents/" + agent.ID.String() + "/webhook"

	// Zweitnutzer mit read-only-Rolle: darf den Trigger nicht verwalten.
	hash, _ := identbuiltin.HashPassword("auditor-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'auditor@test.local','Auditor',$3,'auditor')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")

	// Default: deaktiviert; Auditor darf nicht verwalten.
	got := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if got["enabled"] != false {
		t.Fatalf("webhook muss per Default deaktiviert sein: %+v", got)
	}
	auditor.expect(http.MethodPost, base, nil, http.StatusForbidden)

	// Aktivieren liefert Token + Trigger-URL.
	got = admin.expect(http.MethodPost, base, nil, http.StatusOK)
	token, _ := got["token"].(string)
	if got["enabled"] != true || token == "" {
		t.Fatalf("aktivierter webhook ohne token: %+v", got)
	}

	// Auslösen mit JSON-Payload legt eine Backlog-Aufgabe an …
	code, out := triggerPost(t, s.http.URL+"/api/trigger/"+token,
		`{"title":"Build rot","body":"Pipeline 123 ist fehlgeschlagen.","dedup_key":"p-123"}`)
	if code != http.StatusOK || out["outcome"] != "created" {
		t.Fatalf("trigger: HTTP %d, %+v", code, out)
	}
	// … der Retry mit gleichem dedup_key nicht (Idempotenz).
	if _, out := triggerPost(t, s.http.URL+"/api/trigger/"+token,
		`{"title":"Build rot","dedup_key":"p-123"}`); out["outcome"] != "duplicate" {
		t.Fatalf("retry muss dedupliziert werden: %+v", out)
	}

	// Roh-Text (kein JSON) landet als Aufgaben-Body, Titel ist der Default.
	if code, out := triggerPost(t, s.http.URL+"/api/trigger/"+token, "disk almost full"); code != http.StatusOK || out["outcome"] != "created" {
		t.Fatalf("roh-text-trigger: HTTP %d, %+v", code, out)
	}

	tasks, err := s.backlog.ListByAgent(ctx, agent.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	var sawJSON, sawRaw bool
	for _, task := range tasks {
		if task.Origin != "webhook:trigger" {
			continue
		}
		if task.Title == "Build rot" && task.Body == "Pipeline 123 ist fehlgeschlagen." {
			sawJSON = true
		}
		if task.Title == "Webhook-Trigger" && task.Body == "disk almost full" {
			sawRaw = true
		}
	}
	if !sawJSON || !sawRaw {
		t.Fatalf("erwartete trigger-aufgaben fehlen (json=%v, raw=%v): %+v", sawJSON, sawRaw, tasks)
	}

	// Rotation: neues Token, altes wird ungültig.
	got = admin.expect(http.MethodPost, base, nil, http.StatusOK)
	rotated, _ := got["token"].(string)
	if rotated == "" || rotated == token {
		t.Fatalf("rotation muss ein neues token liefern: %+v", got)
	}
	if code, _ := triggerPost(t, s.http.URL+"/api/trigger/"+token, "{}"); code != http.StatusNotFound {
		t.Fatalf("altes token muss 404 liefern, got %d", code)
	}

	// Fail-closed: gestoppter Agent lehnt den Trigger ab.
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if code, _ := triggerPost(t, s.http.URL+"/api/trigger/"+rotated, "{}"); code != http.StatusConflict {
		t.Fatalf("gestoppter agent muss 409 liefern, got %d", code)
	}
	if err := s.registry.SetKilled(ctx, agent.ID, false); err != nil {
		t.Fatal(err)
	}

	// Deaktivieren: Endpoint verschwindet.
	admin.expect(http.MethodDelete, base, nil, http.StatusOK)
	if code, _ := triggerPost(t, s.http.URL+"/api/trigger/"+rotated, "{}"); code != http.StatusNotFound {
		t.Fatalf("deaktivierter webhook muss 404 liefern, got %d", code)
	}
}
