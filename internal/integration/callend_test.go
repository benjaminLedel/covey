package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/llm"
)

// abschiedsModell is the triage of #517: a goodbye is answered as one and
// marked as closing the call, whether or not the message was said in one —
// the server decides where the mark stands.
type abschiedsModell struct{}

func (abschiedsModell) Name() string { return "test" }

func (abschiedsModell) Complete(_ context.Context, req llm.Request) (string, error) {
	return `{"action":"answer","text":"Gern, bis später!","spoken":"Gern, bis später!","end_call":true}`, nil
}

// TestAGoodbyeInACallEndsIt is #517 with the triage on: the agent's goodbye
// to a person who closed the call carries end_call in its meta; the same
// goodbye typed, or the answer to a question said in the call, does not.
func TestAGoodbyeInACallEndsIt(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("abschied")
	s.ohneLaeufe(agent.ID)
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return abschiedsModell{}, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	path := "/api/v1/conversations/" + c["id"].(string) + "/messages"
	sagen := func(text string, call bool) map[string]string {
		t.Helper()
		body := map[string]any{"text": text}
		if call {
			body["meta"] = map[string]any{"via": "call"}
		}
		out := admin.expect(http.MethodPost, path, body, http.StatusAccepted)
		id, _ := uuid.Parse(out["message"].(map[string]any)["id"].(string))
		wartenAuf(t, "the goodbye is written", func() bool {
			var n int
			_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE reply_to = $1`, id).Scan(&n)
			return n == 1
		})
		_, meta := antwortAuf(t, s, id)
		return meta
	}

	if meta := sagen("Danke dir, das war's. Tschüss!", true); meta[chat.MetaEndCall] != "true" || meta[chat.MetaSpoken] != "Gern, bis später!" {
		t.Fatalf("the goodbye in a call: %v", meta)
	}
	if meta := sagen("Danke dir, das war's. Tschüss!", false); meta[chat.MetaEndCall] != "" {
		t.Fatalf("a typed goodbye closes a call: %v", meta)
	}
	if meta := sagen("Danke! Und ist die Gutschrift schon verbucht?", true); meta[chat.MetaEndCall] != "" {
		t.Fatalf("the answer to a question closes the call: %v", meta)
	}
}

// TestAChatAnswerSaysGoodbyeInItsTag is #517 without the triage (#483): the
// run of a message said in a call marks its goodbye in the spoken tag, and
// covey keeps the mark in the reply's meta.
func TestAChatAnswerSaysGoodbyeInItsTag(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("abschied-ohne-triage")
	admin := teamLogin(t, s)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	path := "/api/v1/conversations/" + c["id"].(string) + "/messages"
	out := admin.expect(http.MethodPost, path, map[string]any{
		"text": "Danke, das war's, tschüss!\n[mock:result Gern, bis später!\n\n<spoken end_call=\"true\">Gern, bis später!</spoken>]",
		"meta": map[string]any{"via": "call"}}, http.StatusCreated)
	tasks, _ := out["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("one task per message: %v", out)
	}
	id, _ := uuid.Parse(tasks[0].(map[string]any)["id"].(string))
	waitFor(t, "the answer run finishes", 40*time.Second, func() bool { return s.taskState(id) == backlog.StateDone })
	s.srv.Nacherzaehlen(ctx)
	var text string
	var meta map[string]string
	if err := s.pool.QueryRow(ctx, `SELECT text, coalesce(meta, '{}'::jsonb) FROM conversation_messages
		WHERE task_id = $1 AND author_kind = 'agent'`, id).Scan(&text, &meta); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "<spoken") || meta[chat.MetaSpoken] != "Gern, bis später!" || meta[chat.MetaEndCall] != "true" {
		t.Fatalf("the goodbye of a chat answer: %q %v", text, meta)
	}
}
