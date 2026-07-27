package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
)

// TestTeamsInboundReplyAttachment ist der Teams-Durchstich (spec/15):
// Messaging-Endpoint-Webhook → Backlog-Task → der Agent lädt den Anhang
// gebrokert in die Sandbox (download_attachment) und antwortet über den Bot
// Connector (reply). Plus Idempotenz gegen wiederholte Zustellung.
func TestTeamsInboundReplyAttachment(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	// Gebrokerte Credentials: teams_token = appId:appPassword, teams_url = Token-Endpoint.
	if err := s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Put(ctx, s.orgID, "teams_url", teams.srv.URL+"/token"); err != nil {
		t.Fatal(err)
	}
	agent := s.newTeamsAgent("teams-support")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "teams_url", agent.ID)

	svc := teams.srv.URL
	fileURL := svc + "/files/notiz.txt"
	text := fmt.Sprintf(
		"[mock:action teams/download_attachment {\"url\":\"%s\",\"name\":\"notiz.txt\"}]\n"+
			"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"reply_to_activity_id\":\"a1\",\"text\":\"Danke, hab die Datei\"}]\n"+
			"[mock:result Erledigt]", fileURL, svc)

	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", text, map[string]any{
		"contentType": "application/vnd.microsoft.teams.file.download.info",
		"name":        "notiz.txt",
		"content":     map[string]any{"downloadUrl": fileURL},
	})

	if out := postTeamsWebhook(t, s, "teams-support", activity); out != `{"outcome":"created"}` {
		t.Fatalf("erste Zustellung muss eine Aufgabe anlegen, got %s", out)
	}

	// Der Agent arbeitet die Aufgabe ab.
	var taskID uuid.UUID
	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, task := range tasks {
			if task.State == backlog.StateDone {
				taskID = task.ID
				return true
			}
		}
		return false
	})

	// Anhang ist tatsächlich in die Sandbox materialisiert (Bytes, nicht nur Metadaten).
	attach := filepath.Join(s.homeBase, agent.ID.String(), "attachments", "notiz.txt")
	raw, err := os.ReadFile(attach)
	if err != nil || !strings.Contains(string(raw), "Anhang-Inhalt für notiz.txt") {
		t.Fatalf("Anhang nicht in der Sandbox materialisiert: %q (err=%v)", raw, err)
	}
	if teams.fileHits() == 0 {
		t.Fatal("der Datei-Endpunkt muss abgerufen worden sein")
	}

	// Antwort ging gebrokert an den Bot Connector (richtige Konversation/Activity, Bearer-Token).
	if teams.replyCount() != 1 {
		t.Fatalf("genau eine Antwort erwartet, got %d", teams.replyCount())
	}
	last := teams.lastReply()
	if last["cid"] != "19:c" || last["aid"] != "a1" {
		t.Fatalf("Antwort an falsche Konversation/Activity: %+v", last)
	}
	if !strings.Contains(last["text"], "hab die Datei") {
		t.Fatalf("Antwort-Text falsch: %q", last["text"])
	}
	if last["auth"] != "Bearer connector-token" {
		t.Fatalf("Connector-Auth fehlt/falsch: %q", last["auth"])
	}
	if teams.tokenHits == 0 {
		t.Fatal("OAuth2-Token-Endpoint muss aufgerufen worden sein")
	}

	// Recording: der Broker-Zugriff und die Aktion stehen im Verlauf.
	events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	for _, want := range []string{"action", "credential"} {
		if kinds[want] == 0 {
			t.Fatalf("recording ohne %s-Events: %+v", want, kinds)
		}
	}
	_ = taskID

	// Idempotenz: dieselbe Activity (Retry des Bot Service) darf nichts Neues auslösen.
	if out := postTeamsWebhook(t, s, "teams-support", activity); out != `{"outcome":"duplicate"}` {
		t.Fatalf("Retry muss dedupliziert werden, got %s", out)
	}
}

// TestTeamsBlockedCorrelation prüft blocked ↔ Konversation: der Agent parkt die
// Aufgabe mit Korrelations-Key teams:conversation:<id>; die Folgenachricht in
// derselben Konversation weckt sie (Wake-on-correlation) und setzt sie fort.
func TestTeamsBlockedCorrelation(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret")
	s.secrets.Put(ctx, s.orgID, "teams_url", teams.srv.URL+"/token")
	agent := s.newTeamsAgent("teams-support")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "teams_url", agent.ID)

	svc := teams.srv.URL
	text := fmt.Sprintf(
		"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:conv\",\"reply_to_activity_id\":\"a1\",\"text\":\"Moment, ich prüfe das\"}]\n"+
			"[mock:block key=teams:conversation:19:conv question=Warte auf Antwort]\n"+
			"[mock:result Abgeschlossen]", svc)

	first := teamsActivity("a1", "19:conv", "29:user", "28:bot", text, nil)
	if out := postTeamsWebhook(t, s, "teams-support", first); out != `{"outcome":"created"}` {
		t.Fatalf("erste Zustellung muss eine Aufgabe anlegen, got %s", out)
	}

	// Aufgabe parkt mit dem Konversations-Korrelations-Key.
	var task backlog.Task
	waitFor(t, "aufgabe blocked", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, tk := range tasks {
			if tk.State == backlog.StateBlocked {
				task = tk
				return true
			}
		}
		return false
	})
	if task.CorrelationKey == nil || *task.CorrelationKey != "teams:conversation:19:conv" {
		t.Fatalf("korrelations-key falsch: %+v", task.CorrelationKey)
	}
	if task.RuntimeSessionID == nil || *task.RuntimeSessionID == "" {
		t.Fatal("runtime_session_id (für --resume) fehlt")
	}
	if teams.replyCount() != 1 {
		t.Fatalf("genau eine Rückfrage erwartet, got %d", teams.replyCount())
	}

	// Folgenachricht in derselben Konversation → Korrelation, kein neuer Task.
	answer := teamsActivity("a2", "19:conv", "29:user", "28:bot", "Passt, danke!", nil)
	if out := postTeamsWebhook(t, s, "teams-support", answer); out != `{"outcome":"correlated"}` {
		t.Fatalf("Folgenachricht muss korrelieren, got %s", out)
	}

	waitFor(t, "aufgabe done nach korrelation", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	done, _ := s.backlog.Get(ctx, task.ID)
	if done.Result == nil || *done.Result != "Abgeschlossen" {
		t.Fatalf("ergebnis nach resume fehlt: %+v", done.Result)
	}
}

// --- Hilfen ---

// newTeamsAgent legt einen Testagenten mit mock-Runtime und Teams-Zugang an.
func (s *stack) newTeamsAgent(slug string) agents.Agent {
	s.t.Helper()
	agent, err := s.registry.Create(context.Background(), s.orgID, slug, "Teams-Agent", "mock", &s.adminID)
	if err != nil {
		s.t.Fatal(err)
	}
	_, err = s.registry.SaveConfig(context.Background(), agent.ID, map[string]string{
		"SOUL.md":   "# Teams-Agent\n\n## Rolle\nChat-Support.",
		"ACCESS.md": "- system: teams scope: read,write",
	}, &s.adminID)
	if err != nil {
		s.t.Fatal(err)
	}
	return agent
}

// teamsActivity baut eine Bot-Framework-Activity als JSON-Bytes.
func teamsActivity(id, convID, fromID, botID, text string, attachment map[string]any) []byte {
	a := map[string]any{
		"type":       "message",
		"id":         id,
		"text":       text,
		"serviceUrl": "", // wird pro Test aus dem Text (service_url) genutzt; Feld bleibt informativ
		"channelId":  "msteams",
		"from":       map[string]any{"id": fromID, "name": "Alice"},
		"recipient":  map[string]any{"id": botID, "name": "Covey"},
		"conversation": map[string]any{
			"id": convID, "conversationType": "personal", "tenantId": "t1",
		},
	}
	if attachment != nil {
		a["attachments"] = []map[string]any{attachment}
	}
	body, _ := json.Marshal(a)
	return body
}

// postTeamsWebhook postet eine Activity an den Messaging-Endpoint. Keine
// JWT-Signatur nötig: COVEY_TEAMS_WEBHOOK_SECRET ist im Test leer, damit ist die
// Verifikation aus (Dev-Modus, wie bei faketeams).
func postTeamsWebhook(t *testing.T, s *stack, slug string, body []byte) string {
	t.Helper()
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/teams/"+slug, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := new(bytes.Buffer)
	buf.ReadFrom(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("webhook HTTP %d: %s", resp.StatusCode, buf.String())
	}
	return strings.TrimSpace(buf.String())
}
