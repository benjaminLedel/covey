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

// TestTeamsSendFileConsentFlow ist der Durchstich für ausgehende Dateien
// (spec/15): Der Agent fragt mit einer Karte um Zustimmung (send_file) und
// parkt; der Klick des Empfängers kommt als fileConsent/invoke zurück, weckt
// ihn über die Konversation und er lädt die Bytes hoch (upload_file). Ohne
// Zustimmung gibt es keine Upload-URL — das ist Teams-Vorgabe, der Zwei-Schritt
// also nicht abkürzbar.
func TestTeamsSendFileConsentFlow(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret")
	s.secrets.Put(ctx, s.orgID, "teams_url", teams.srv.URL+"/token")
	agent := s.newTeamsAgent("teams-versand")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "teams_url", agent.ID)

	// Die zu sendende Datei liegt im persistenten Home des Agenten — genau
	// deshalb übersteht sie die Wartezeit bis zur Zustimmung.
	home := filepath.Join(s.homeBase, agent.ID.String())
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bericht.pdf"), []byte("PDF-INHALT"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Das Skript des Mock-Agenten: erst um Zustimmung fragen und parken, nach
	// dem Wecken hochladen. Die Direktiven hinter [mock:block] laufen erst beim
	// Resume — der Mock spielt den Body dann erneut ab (und postet dabei auch
	// die Consent-Karte ein zweites Mal; deshalb prüft der Test die Karte
	// VOR der Zustimmung).
	svc := teams.srv.URL
	ask := fmt.Sprintf(
		"[mock:action teams/send_file {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"path\":\"bericht.pdf\",\"description\":\"Quartalsbericht\"}]\n"+
			"[mock:block key=teams:conversation:19:c question=Warte auf Zustimmung]\n"+
			"[mock:action teams/upload_file {\"upload_url\":\"%s/upload/bericht.pdf\",\"path\":\"bericht.pdf\",\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"content_url\":\"%s/files/bericht.pdf\",\"unique_id\":\"u-1\",\"file_type\":\"pdf\",\"name\":\"bericht.pdf\"}]\n"+
			"[mock:result Datei gesendet]", svc, svc, svc, svc)
	if out := postTeamsWebhook(t, s, "teams-versand", teamsActivity("a1", "19:c", "29:user", "28:bot", ask, nil)); out != `{"outcome":"created"}` {
		t.Fatalf("erste Zustellung muss eine Aufgabe anlegen, got %s", out)
	}

	// Die Zustimmungs-Karte liegt im Chat, die Aufgabe parkt.
	waitFor(t, "zustimmungs-karte gepostet", 15*time.Second, func() bool {
		return len(teams.cardsOfType("card.file.consent")) == 1
	})
	card := teams.cardsOfType("card.file.consent")[0]
	if card["name"] != "bericht.pdf" {
		t.Fatalf("falscher Dateiname in der Karte: %+v", card)
	}
	if content, _ := card["content"].(map[string]any); content == nil || content["sizeInBytes"] == nil {
		t.Fatalf("karte ohne sizeInBytes: %+v", card["content"])
	}
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

	// Der Empfänger klickt „Annehmen": Teams schickt die Upload-URL als
	// invoke-Activity — sie korreliert über die Konversation.
	uploadURL := svc + "/upload/bericht.pdf"
	invoke, _ := json.Marshal(map[string]any{
		"type": "invoke", "id": "inv-1", "name": "fileConsent/invoke",
		"serviceUrl": svc, "channelId": "msteams",
		"from":         map[string]any{"id": "29:user", "name": "Alice"},
		"recipient":    map[string]any{"id": "28:bot", "name": "Covey"},
		"conversation": map[string]any{"id": "19:c", "conversationType": "personal", "tenantId": "t1"},
		"value": map[string]any{
			"type": "fileUpload", "action": "accept",
			"context": map[string]any{"key": "bericht.pdf"},
			"uploadInfo": map[string]any{
				"uploadUrl": uploadURL, "contentUrl": svc + "/files/bericht.pdf",
				"name": "bericht.pdf", "uniqueId": "u-1", "fileType": "pdf",
			},
		},
	})
	if out := postTeamsWebhook(t, s, "teams-versand", invoke); out != `{"outcome":"correlated"}` {
		t.Fatalf("zustimmung muss korrelieren, got %s", out)
	}

	// Der geweckte Agent lädt hoch und schließt ab.
	waitFor(t, "datei hochgeladen", 15*time.Second, func() bool {
		return teams.lastUpload() != nil
	})
	up := teams.lastUpload()
	if up["body"] != "PDF-INHALT" {
		t.Fatalf("falscher Upload-Inhalt: %q", up["body"])
	}
	if up["range"] != "bytes 0-9/10" {
		t.Fatalf("Content-Range falsch: %q", up["range"])
	}
	if up["auth"] != "" {
		t.Fatalf("Upload-URL trägt ihre Autorisierung selbst — kein Bearer erwartet, got %q", up["auth"])
	}
	waitFor(t, "abschluss-karte gepostet", 15*time.Second, func() bool {
		return len(teams.cardsOfType("card.file.info")) == 1
	})
	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
}

// TestTeamsSendFileAbgelehnt: lehnt der Empfänger ab, wird der Agent trotzdem
// geweckt — sonst bliebe er auf einer Zustimmung hängen, die nie kommt. Es darf
// nichts hochgeladen werden.
func TestTeamsSendFileAbgelehnt(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret")
	s.secrets.Put(ctx, s.orgID, "teams_url", teams.srv.URL+"/token")
	agent := s.newTeamsAgent("teams-abgelehnt")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "teams_url", agent.ID)

	home := filepath.Join(s.homeBase, agent.ID.String())
	os.MkdirAll(home, 0o755)
	os.WriteFile(filepath.Join(home, "bericht.pdf"), []byte("PDF-INHALT"), 0o644)

	svc := teams.srv.URL
	ask := fmt.Sprintf(
		"[mock:action teams/send_file {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"path\":\"bericht.pdf\"}]\n"+
			"[mock:block key=teams:conversation:19:c question=Warte auf Zustimmung]\n"+
			"[mock:result Zustimmung angefragt]", svc)
	postTeamsWebhook(t, s, "teams-abgelehnt", teamsActivity("a1", "19:c", "29:user", "28:bot", ask, nil))

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

	decline, _ := json.Marshal(map[string]any{
		"type": "invoke", "id": "inv-2", "name": "fileConsent/invoke",
		"serviceUrl": svc, "channelId": "msteams",
		"from":         map[string]any{"id": "29:user", "name": "Alice"},
		"recipient":    map[string]any{"id": "28:bot", "name": "Covey"},
		"conversation": map[string]any{"id": "19:c", "conversationType": "personal", "tenantId": "t1"},
		"value": map[string]any{
			"type": "fileUpload", "action": "decline",
			"context": map[string]any{"key": "bericht.pdf"},
		},
	})
	if out := postTeamsWebhook(t, s, "teams-abgelehnt", decline); out != `{"outcome":"correlated"}` {
		t.Fatalf("ablehnung muss den Agenten wecken, got %s", out)
	}
	waitFor(t, "aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if up := teams.lastUpload(); up != nil {
		t.Fatalf("nach Ablehnung darf nichts hochgeladen werden, got %+v", up)
	}
}

// TestTeamsSendFileOhneWartenden: eine Zustimmung ist die Fortsetzung einer
// angefangenen Arbeit, nicht ihr Anfang. Trifft sie ein, ohne dass jemand
// darauf parkt (Aufgabe längst beendet, verspätete Zustellung), darf keine
// neue Aufgabe entstehen — sonst bekäme ein ahnungsloser Agent den Auftrag,
// eine Datei hochzuladen, von der er nichts weiß.
func TestTeamsSendFileOhneWartenden(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret")
	agent := s.newTeamsAgent("teams-verwaist")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)

	consent, _ := json.Marshal(map[string]any{
		"type": "invoke", "id": "inv-3", "name": "fileConsent/invoke",
		"serviceUrl": teams.srv.URL, "channelId": "msteams",
		"from":         map[string]any{"id": "29:user", "name": "Alice"},
		"recipient":    map[string]any{"id": "28:bot", "name": "Covey"},
		"conversation": map[string]any{"id": "19:leer", "conversationType": "personal", "tenantId": "t1"},
		"value": map[string]any{
			"type": "fileUpload", "action": "accept",
			"context": map[string]any{"key": "bericht.pdf"},
			"uploadInfo": map[string]any{
				"uploadUrl": teams.srv.URL + "/upload/bericht.pdf", "name": "bericht.pdf",
			},
		},
	})
	if out := postTeamsWebhook(t, s, "teams-verwaist", consent); out != `{"outcome":"ignored"}` {
		t.Fatalf("zustimmung ohne wartende Aufgabe darf keine Arbeit anlegen, got %s", out)
	}
	if tasks, err := s.backlog.ListByAgent(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 0 {
		t.Fatalf("es darf keine Aufgabe entstanden sein, got %d: %+v", len(tasks), tasks)
	}
	if up := teams.lastUpload(); up != nil {
		t.Fatalf("nichts darf hochgeladen worden sein, got %+v", up)
	}
}

// TestTeamsReplyOhneURLSecret: teams_url ist ein Override für Single-Tenant-
// Bots, kein Pflicht-Secret — das Plugin kennt seinen Token-Endpoint selbst
// (BaseURLOptional). Ohne das Secret muss der Broker das Credential trotzdem
// gewähren und die Antwort rausgehen; vorher verweigerte er fail-closed und ein
// sauber eingerichteter Agent scheiterte an einem Wert, den niemand braucht.
func TestTeamsReplyOhneURLSecret(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	// Instanz-weiter Default statt Agent-Secret — hier auf das Double gelenkt.
	t.Setenv("COVEY_TEAMS_TOKEN_URL", teams.srv.URL+"/token")

	// Nur der Token, KEIN teams_url.
	if err := s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret"); err != nil {
		t.Fatal(err)
	}
	agent := s.newTeamsAgent("teams-ohne-url")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)

	text := fmt.Sprintf(
		"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"reply_to_activity_id\":\"a1\",\"text\":\"Hallo\"}]\n"+
			"[mock:result Erledigt]", teams.srv.URL)
	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", text, nil)
	if out := postTeamsWebhook(t, s, "teams-ohne-url", activity); out != `{"outcome":"created"}` {
		t.Fatalf("webhook muss eine Aufgabe anlegen, got %s", out)
	}

	waitFor(t, "antwort beim connector", 15*time.Second, func() bool {
		return teams.replyCount() == 1
	})
	if teams.tokenHits == 0 {
		t.Fatal("der Default-Token-Endpoint muss aufgerufen worden sein")
	}
}

// TestWebhookAddressedByAgentID: der Webhook-Endpoint adressiert den Agenten
// per Slug — ersatzweise auch per Agent-ID. Die ID steht in der URL der
// Agenten-Seite und wandert beim Einrichten eines Zielsystems leicht statt des
// Slugs in die Endpoint-Konfiguration des Fremdsystems; da sie denselben
// Agenten eindeutig bezeichnet, wäre ein 404 nur eine Stolperfalle. Unbekannte
// Adressen bleiben fail-closed.
func TestWebhookAddressedByAgentID(t *testing.T) {
	s := newStack(t)
	agent := s.newTeamsAgent("teams-per-id")

	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", "[mock:result Erledigt]", nil)
	if out := postTeamsWebhook(t, s, agent.ID.String(), activity); out != `{"outcome":"created"}` {
		t.Fatalf("ID-adressierte Zustellung muss eine Aufgabe anlegen, got %s", out)
	}

	// Eine ID, die es nicht gibt, darf nichts wecken.
	req, _ := http.NewRequest(http.MethodPost,
		s.http.URL+"/api/webhooks/teams/"+uuid.NewString(), bytes.NewReader(activity))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unbekannte agent-id: HTTP %d (erwartet 404)", resp.StatusCode)
	}
}
