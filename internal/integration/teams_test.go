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

// TestTeamsInboundReplyAttachment is the Teams vertical slice (spec/15):
// messaging-endpoint webhook → backlog task → the agent downloads the attachment
// into the sandbox with brokered credentials (download_attachment) and answers
// through the Bot Connector (reply). Plus idempotency against repeated
// delivery.
func TestTeamsInboundReplyAttachment(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	// Brokered credentials: teams_token = appId:appPassword, teams_url = token endpoint.
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
			"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"reply_to_activity_id\":\"a1\",\"text\":\"Thanks, got the file\"}]\n"+
			"[mock:result Done]", fileURL, svc)

	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", text, map[string]any{
		"contentType": "application/vnd.microsoft.teams.file.download.info",
		"name":        "notiz.txt",
		"content":     map[string]any{"downloadUrl": fileURL},
	})

	if out := postTeamsWebhook(t, s, "teams-support", activity); out != `{"outcome":"created"}` {
		t.Fatalf("the first delivery has to create a task, got %s", out)
	}

	// The agent works the task off.
	var taskID uuid.UUID
	waitFor(t, "task done", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, task := range tasks {
			if task.State == backlog.StateDone {
				taskID = task.ID
				return true
			}
		}
		return false
	})

	// The attachment really is materialized into the sandbox (bytes, not just metadata).
	attach := filepath.Join(s.homeBase, agent.ID.String(), "attachments", "notiz.txt")
	raw, err := os.ReadFile(attach)
	if err != nil || !strings.Contains(string(raw), "notiz.txt") {
		t.Fatalf("the attachment was not materialized in the sandbox: %q (err=%v)", raw, err)
	}
	if teams.fileHits() == 0 {
		t.Fatal("the file endpoint has to have been called")
	}

	// The answer went to the Bot Connector with brokered credentials (right conversation/activity, bearer token).
	if teams.replyCount() != 1 {
		t.Fatalf("exactly one answer was expected, got %d", teams.replyCount())
	}
	last := teams.lastReply()
	if last["cid"] != "19:c" || last["aid"] != "a1" {
		t.Fatalf("answer to the wrong conversation/activity: %+v", last)
	}
	if !strings.Contains(last["text"], "got the file") {
		t.Fatalf("wrong answer text: %q", last["text"])
	}
	if last["auth"] != "Bearer connector-token" {
		t.Fatalf("connector auth missing/wrong: %q", last["auth"])
	}
	if teams.tokenHits == 0 {
		t.Fatal("the OAuth2 token endpoint has to have been called")
	}

	// Recording: the broker access and the action are in the history.
	events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	kinds := map[string]int{}
	for _, e := range events {
		kinds[e.Kind]++
	}
	for _, want := range []string{"action", "credential"} {
		if kinds[want] == 0 {
			t.Fatalf("recording without %s events: %+v", want, kinds)
		}
	}
	_ = taskID

	// Idempotency: the same activity (retry from the Bot Service) must not trigger anything new.
	if out := postTeamsWebhook(t, s, "teams-support", activity); out != `{"outcome":"duplicate"}` {
		t.Fatalf("the retry has to be deduplicated, got %s", out)
	}
}

// TestTeamsBlockedCorrelation checks blocked ↔ conversation: the agent parks the
// task with the correlation key teams:conversation:<id>; the follow-up message in
// the same conversation wakes it (wake-on-correlation) and continues it.
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
		"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:conv\",\"reply_to_activity_id\":\"a1\",\"text\":\"One moment, I am checking\"}]\n"+
			"[mock:block key=teams:conversation:19:conv question=Waiting for an answer]\n"+
			"[mock:result Completed]", svc)

	first := teamsActivity("a1", "19:conv", "29:user", "28:bot", text, nil)
	if out := postTeamsWebhook(t, s, "teams-support", first); out != `{"outcome":"created"}` {
		t.Fatalf("the first delivery has to create a task, got %s", out)
	}

	// The task parks with the conversation correlation key.
	var task backlog.Task
	waitFor(t, "task blocked", 15*time.Second, func() bool {
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
		t.Fatalf("wrong correlation key: %+v", task.CorrelationKey)
	}
	if task.RuntimeSessionID == nil || *task.RuntimeSessionID == "" {
		t.Fatal("runtime_session_id (for --resume) is missing")
	}
	if teams.replyCount() != 1 {
		t.Fatalf("exactly one follow-up question was expected, got %d", teams.replyCount())
	}

	// A follow-up message in the same conversation → correlation, no new task.
	answer := teamsActivity("a2", "19:conv", "29:user", "28:bot", "Passt, danke!", nil)
	if out := postTeamsWebhook(t, s, "teams-support", answer); out != `{"outcome":"correlated"}` {
		t.Fatalf("the follow-up message has to correlate, got %s", out)
	}

	waitFor(t, "task done after the correlation", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	done, _ := s.backlog.Get(ctx, task.ID)
	if done.Result == nil || *done.Result != "Completed" {
		t.Fatalf("the result after the resume is missing: %+v", done.Result)
	}
}

// --- Helpers ---

// newTeamsAgent creates a test agent with the mock runtime and Teams access.
func (s *stack) newTeamsAgent(slug string) agents.Agent {
	s.t.Helper()
	agent, err := s.registry.Create(context.Background(), s.orgID, slug, "Teams-Agent", "mock", &s.adminID)
	if err != nil {
		s.t.Fatal(err)
	}
	_, err = s.registry.SaveConfig(context.Background(), agent.ID, map[string]string{
		"SOUL.md":   "# Teams-Agent\n\n## Role\nChat support.",
		"ACCESS.md": "- system: teams scope: read,write",
	}, &s.adminID)
	if err != nil {
		s.t.Fatal(err)
	}
	return agent
}

// teamsActivity builds a Bot Framework activity as JSON bytes.
func teamsActivity(id, convID, fromID, botID, text string, attachment map[string]any) []byte {
	a := map[string]any{
		"type":       "message",
		"id":         id,
		"text":       text,
		"serviceUrl": "", // taken per test from the text (service_url); the field stays informative
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

// postTeamsWebhook posts an activity to the messaging endpoint. No JWT signature
// needed: COVEY_TEAMS_WEBHOOK_SECRET is empty in the test, which turns the
// verification off (dev mode, as with faketeams).
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

// TestTeamsSendFileConsentFlow is the vertical slice for outgoing files
// (spec/15): the agent asks for consent with a card (send_file) and parks; the
// recipient's click comes back as fileConsent/invoke, wakes the agent through the
// conversation, and it uploads the bytes (upload_file). Without consent there is
// no upload URL — that is Teams' rule, so the two-step cannot be shortened.
func TestTeamsSendFileConsentFlow(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret")
	s.secrets.Put(ctx, s.orgID, "teams_url", teams.srv.URL+"/token")
	agent := s.newTeamsAgent("teams-versand")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)
	s.secrets.Assign(ctx, s.orgID, "teams_url", agent.ID)

	// The file to be sent lies in the agent's persistent home — that is exactly why
	// it survives the wait for the consent.
	home := filepath.Join(s.homeBase, agent.ID.String())
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "bericht.pdf"), []byte("PDF-INHALT"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The mock agent's script: first ask for consent and park, upload after being
	// woken. The directives after [mock:block] only run on the resume — the mock
	// then replays the body (and posts the consent card a second time in the
	// process; that is why the test checks the card BEFORE the consent).
	svc := teams.srv.URL
	ask := fmt.Sprintf(
		"[mock:action teams/send_file {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"path\":\"bericht.pdf\",\"description\":\"Quarterly report\"}]\n"+
			"[mock:block key=teams:conversation:19:c question=Waiting for consent]\n"+
			"[mock:action teams/upload_file {\"upload_url\":\"%s/upload/bericht.pdf\",\"path\":\"bericht.pdf\",\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"content_url\":\"%s/files/bericht.pdf\",\"unique_id\":\"u-1\",\"file_type\":\"pdf\",\"name\":\"bericht.pdf\"}]\n"+
			"[mock:result File sent]", svc, svc, svc, svc)
	if out := postTeamsWebhook(t, s, "teams-versand", teamsActivity("a1", "19:c", "29:user", "28:bot", ask, nil)); out != `{"outcome":"created"}` {
		t.Fatalf("the first delivery has to create a task, got %s", out)
	}

	// The consent card is in the chat, the task parks.
	waitFor(t, "consent card posted", 15*time.Second, func() bool {
		return len(teams.cardsOfType("card.file.consent")) == 1
	})
	card := teams.cardsOfType("card.file.consent")[0]
	if card["name"] != "bericht.pdf" {
		t.Fatalf("wrong file name in the card: %+v", card)
	}
	if content, _ := card["content"].(map[string]any); content == nil || content["sizeInBytes"] == nil {
		t.Fatalf("card without sizeInBytes: %+v", card["content"])
	}
	var task backlog.Task
	waitFor(t, "task blocked", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, tk := range tasks {
			if tk.State == backlog.StateBlocked {
				task = tk
				return true
			}
		}
		return false
	})

	// The recipient clicks "accept": Teams sends the upload URL as an invoke
	// activity — it correlates through the conversation.
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
		t.Fatalf("the consent has to correlate, got %s", out)
	}

	// The woken agent uploads and finishes.
	waitFor(t, "file uploaded", 15*time.Second, func() bool {
		return teams.lastUpload() != nil
	})
	up := teams.lastUpload()
	if up["body"] != "PDF-INHALT" {
		t.Fatalf("wrong upload content: %q", up["body"])
	}
	if up["range"] != "bytes 0-9/10" {
		t.Fatalf("wrong Content-Range: %q", up["range"])
	}
	if up["auth"] != "" {
		t.Fatalf("the upload URL carries its own authorization — no bearer expected, got %q", up["auth"])
	}
	waitFor(t, "completion card posted", 15*time.Second, func() bool {
		return len(teams.cardsOfType("card.file.info")) == 1
	})
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
}

// TestTeamsSendFileAbgelehnt: if the recipient declines, the agent is woken all
// the same — otherwise it would hang on a consent that never comes. Nothing may
// be uploaded.
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
			"[mock:block key=teams:conversation:19:c question=Waiting for consent]\n"+
			"[mock:result Consent requested]", svc)
	postTeamsWebhook(t, s, "teams-abgelehnt", teamsActivity("a1", "19:c", "29:user", "28:bot", ask, nil))

	var task backlog.Task
	waitFor(t, "task blocked", 15*time.Second, func() bool {
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
		t.Fatalf("the decline has to wake the agent, got %s", out)
	}
	waitFor(t, "task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if up := teams.lastUpload(); up != nil {
		t.Fatalf("after a decline nothing may be uploaded, got %+v", up)
	}
}

// TestTeamsSendFileOhneWartenden: a consent is the continuation of work already
// started, not its beginning. If it arrives without anyone parking on it (task
// long finished, delayed delivery), no new task may come into being — otherwise
// an unsuspecting agent would get the assignment to upload a file it knows
// nothing about.
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
		t.Fatalf("a consent without a waiting task must not create work, got %s", out)
	}
	if tasks, err := s.backlog.ListByAgent(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	} else if len(tasks) != 0 {
		t.Fatalf("no task may have come into being, got %d: %+v", len(tasks), tasks)
	}
	if up := teams.lastUpload(); up != nil {
		t.Fatalf("nothing may have been uploaded, got %+v", up)
	}
}

// TestTeamsReplyOhneURLSecret: teams_url is an override for single-tenant bots,
// not a mandatory secret — the plugin knows its token endpoint itself
// (BaseURLOptional). Without the secret the broker still has to grant the
// credential and the answer has to go out; before, it refused fail-closed and a
// properly set-up agent failed on a value nobody needs.
func TestTeamsReplyOhneURLSecret(t *testing.T) {
	s := newStack(t)
	teams := newFakeTeams(t)
	ctx := context.Background()

	// Instance-wide default instead of an agent secret — pointed at the double here.
	t.Setenv("COVEY_TEAMS_TOKEN_URL", teams.srv.URL+"/token")

	// Only the token, NO teams_url.
	if err := s.secrets.Put(ctx, s.orgID, "teams_token", "bot-app-id:bot-app-secret"); err != nil {
		t.Fatal(err)
	}
	agent := s.newTeamsAgent("teams-ohne-url")
	s.secrets.Assign(ctx, s.orgID, "teams_token", agent.ID)

	text := fmt.Sprintf(
		"[mock:action teams/reply {\"service_url\":\"%s\",\"conversation_id\":\"19:c\",\"reply_to_activity_id\":\"a1\",\"text\":\"Hello\"}]\n"+
			"[mock:result Done]", teams.srv.URL)
	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", text, nil)
	if out := postTeamsWebhook(t, s, "teams-ohne-url", activity); out != `{"outcome":"created"}` {
		t.Fatalf("the webhook has to create a task, got %s", out)
	}

	waitFor(t, "answer at the connector", 15*time.Second, func() bool {
		return teams.replyCount() == 1
	})
	if teams.tokenHits == 0 {
		t.Fatal("the default token endpoint has to have been called")
	}
}

// TestWebhookAddressedByAgentID: the webhook endpoint addresses the agent by
// slug — alternatively by agent ID as well. The ID is in the URL of the agent
// page and easily ends up instead of the slug in the foreign system's endpoint
// configuration while a target system is being set up; since it denotes the same
// agent unambiguously, a 404 would only be a stumbling block. Unknown addresses
// stay fail-closed.
func TestWebhookAddressedByAgentID(t *testing.T) {
	s := newStack(t)
	agent := s.newTeamsAgent("teams-per-id")

	activity := teamsActivity("a1", "19:c", "29:user", "28:bot", "[mock:result Done]", nil)
	if out := postTeamsWebhook(t, s, agent.ID.String(), activity); out != `{"outcome":"created"}` {
		t.Fatalf("an ID-addressed delivery has to create a task, got %s", out)
	}

	// An ID that does not exist must not wake anything.
	req, _ := http.NewRequest(http.MethodPost,
		s.http.URL+"/api/webhooks/teams/"+uuid.NewString(), bytes.NewReader(activity))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown agent id: HTTP %d (expected 404)", resp.StatusCode)
	}
}
