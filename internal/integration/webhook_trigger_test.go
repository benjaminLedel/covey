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

// triggerPost fires the public trigger endpoint without session auth — the way a
// foreign system (CI, cron, Zapier) would.
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

// TestAgentWebhookTrigger checks the optional generic webhook trigger from
// spec/03: management only for manager roles, triggering by token, idempotency
// through dedup_key, the raw-text fallback, token rotation and fail-closed on a
// kill.
func TestAgentWebhookTrigger(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "hook", "Hook-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/agents/" + agent.ID.String() + "/webhook"

	// A second user with a read-only role: must not manage the trigger.
	hash, _ := identbuiltin.HashPassword("auditor-passwort")
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'auditor@test.local','Auditor',$3,'auditor')`, uuid.New(), s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	auditor := login(t, s, "auditor@test.local", "auditor-passwort")

	// Default: disabled; the auditor must not manage it.
	got := admin.expect(http.MethodGet, base, nil, http.StatusOK)
	if got["enabled"] != false {
		t.Fatalf("the webhook has to be disabled by default: %+v", got)
	}
	auditor.expect(http.MethodPost, base, nil, http.StatusForbidden)

	// Enabling delivers token + trigger URL.
	got = admin.expect(http.MethodPost, base, nil, http.StatusOK)
	token, _ := got["token"].(string)
	if got["enabled"] != true || token == "" {
		t.Fatalf("an enabled webhook without a token: %+v", got)
	}

	// Triggering with a JSON payload creates a backlog task …
	code, out := triggerPost(t, s.http.URL+"/api/trigger/"+token,
		`{"title":"Build rot","body":"Pipeline 123 ist fehlgeschlagen.","dedup_key":"p-123"}`)
	if code != http.StatusOK || out["outcome"] != "created" {
		t.Fatalf("trigger: HTTP %d, %+v", code, out)
	}
	// … the retry with the same dedup_key does not (idempotency).
	if _, out := triggerPost(t, s.http.URL+"/api/trigger/"+token,
		`{"title":"Build rot","dedup_key":"p-123"}`); out["outcome"] != "duplicate" {
		t.Fatalf("the retry has to be deduplicated: %+v", out)
	}

	// Raw text (no JSON) lands as the task body, the title is the default.
	if code, out := triggerPost(t, s.http.URL+"/api/trigger/"+token, "disk almost full"); code != http.StatusOK || out["outcome"] != "created" {
		t.Fatalf("raw-text trigger: HTTP %d, %+v", code, out)
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
		if task.Title == "Webhook trigger" && task.Body == "disk almost full" {
			sawRaw = true
		}
	}
	if !sawJSON || !sawRaw {
		t.Fatalf("the expected trigger tasks are missing (json=%v, raw=%v): %+v", sawJSON, sawRaw, tasks)
	}

	// Rotation: a new token, the old one becomes invalid.
	got = admin.expect(http.MethodPost, base, nil, http.StatusOK)
	rotated, _ := got["token"].(string)
	if rotated == "" || rotated == token {
		t.Fatalf("the rotation has to deliver a new token: %+v", got)
	}
	if code, _ := triggerPost(t, s.http.URL+"/api/trigger/"+token, "{}"); code != http.StatusNotFound {
		t.Fatalf("the old token has to return 404, got %d", code)
	}

	// Fail-closed: a stopped agent rejects the trigger.
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if code, _ := triggerPost(t, s.http.URL+"/api/trigger/"+rotated, "{}"); code != http.StatusConflict {
		t.Fatalf("a stopped agent has to return 409, got %d", code)
	}
	if err := s.registry.SetKilled(ctx, agent.ID, false); err != nil {
		t.Fatal(err)
	}

	// Disabling: the endpoint disappears.
	admin.expect(http.MethodDelete, base, nil, http.StatusOK)
	if code, _ := triggerPost(t, s.http.URL+"/api/trigger/"+rotated, "{}"); code != http.StatusNotFound {
		t.Fatalf("a disabled webhook has to return 404, got %d", code)
	}
}
