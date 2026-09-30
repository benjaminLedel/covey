package integration

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/llm"
)

const (
	anrufGeschrieben = "Doch, ist drin – DLES-273, MR !475, alle Tests grün, wartet auf Gertruds Review."
	anrufGesprochen  = "Ja, ist drin. Der Merge Request ist fertig, alle Tests sind grün, und er wartet noch auf Gertruds Review."
)

// hoerendesModell answers every turn by what it is: the triage (with a
// spoken form only when it is asked for one), the retelling of a result,
// and the spoken form of a result. It keeps the system prompts it saw.
type hoerendesModell struct {
	mu      sync.Mutex
	systeme []string
}

func (m *hoerendesModell) Name() string { return "test" }

func (m *hoerendesModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	m.systeme = append(m.systeme, req.System)
	m.mu.Unlock()
	prompt := req.Messages[0].Content
	switch {
	case strings.Contains(req.System, "Write what the voice says"):
		return `{"spoken":"Die Rechnung war doppelt gebucht, und ich habe die zweite Buchung storniert.","details_in_chat":true}`, nil
	case strings.Contains(req.System, "It is finished now"):
		return "Die Rechnung 2026-114 war doppelt gebucht; ich habe die zweite Buchung storniert.", nil
	case strings.Contains(prompt, "Bitte prüf"):
		if strings.Contains(req.System, `"spoken"`) {
			return `{"action":"task","title":"Rechnung 2026-114 prüfen","body":"Prüfen.","text":"Mach ich, ich schau mir 2026-114 an.","spoken":"Mach ich, ich schau mir die Rechnung an."}`, nil
		}
		return `{"action":"task","title":"Rechnung 2026-114 prüfen","body":"Prüfen.","text":"Mach ich, ich schau mir 2026-114 an."}`, nil
	case strings.Contains(req.System, `"spoken"`):
		return `{"action":"answer","text":"` + anrufGeschrieben + `","spoken":"` + anrufGesprochen + `","details_in_chat":true}`, nil
	}
	return `{"action":"answer","text":"` + anrufGeschrieben + `","spoken":"Das darf nie gespeichert werden."}`, nil
}

func (m *hoerendesModell) anzahl() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.systeme)
}

// antwortAuf is the agent's message that replies to a person's message.
func antwortAuf(t *testing.T, s *stack, msgID uuid.UUID) (string, map[string]string) {
	t.Helper()
	var text string
	var meta map[string]string
	if err := s.pool.QueryRow(context.Background(), `SELECT text, coalesce(meta, '{}'::jsonb) FROM conversation_messages
		WHERE reply_to = $1 AND author_kind = 'agent'`, msgID).Scan(&text, &meta); err != nil {
		t.Fatalf("the reply to %s: %v", msgID, err)
	}
	return text, meta
}

// TestInACallTheAgentAnswersForTheEar is #502 with the triage on: a message
// said in a call is answered in writing as ever, and its spoken form stands
// beside it in the meta — for an answer and for a task's acknowledgement. A
// typed message is triaged with the prompt it always had and gets none. The
// task's result, when it is reported, gets its spoken form from one turn.
func TestInACallTheAgentAnswersForTheEar(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("hoerer")
	s.ohneLaeufe(agent.ID)
	modell := &hoerendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	path := "/api/v1/conversations/" + c["id"].(string) + "/messages"
	id := func(out map[string]any) uuid.UUID {
		u, _ := uuid.Parse(out["message"].(map[string]any)["id"].(string))
		return u
	}

	gesagt := id(admin.expect(http.MethodPost, path, map[string]any{"text": "Der Login-Fix ist noch nicht drin, oder?", "meta": map[string]any{"via": "call"}}, http.StatusAccepted))
	wartenAuf(t, "the call's line is answered", func() bool { return modell.anzahl() == 1 })
	wartenAuf(t, "the answer is written", func() bool {
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE reply_to = $1`, gesagt).Scan(&n)
		return n == 1
	})
	text, meta := antwortAuf(t, s, gesagt)
	if text != anrufGeschrieben || meta[chat.MetaSpoken] != anrufGesprochen || meta[chat.MetaDetailsInChat] != "true" {
		t.Fatalf("the answer in a call: %q %v", text, meta)
	}

	getippt := id(admin.expect(http.MethodPost, path, map[string]any{"text": "Und getippt?"}, http.StatusAccepted))
	wartenAuf(t, "the typed line is answered", func() bool {
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE reply_to = $1`, getippt).Scan(&n)
		return n == 1
	})
	text, meta = antwortAuf(t, s, getippt)
	if text != anrufGeschrieben || meta[chat.MetaSpoken] != "" || meta[chat.MetaDetailsInChat] != "" {
		t.Fatalf("a typed line got a spoken form: %q %v", text, meta)
	}
	modell.mu.Lock()
	if strings.Contains(modell.systeme[1], "said aloud in a call") || !strings.Contains(modell.systeme[0], "said aloud in a call") {
		modell.mu.Unlock()
		t.Fatal("only the triage of the line said in the call is asked for a spoken form")
	}
	modell.mu.Unlock()

	// Work said in a call: the acknowledgement is heard, and the task knows it
	// came from a call.
	auftrag := id(admin.expect(http.MethodPost, path, map[string]any{"text": "Bitte prüf die Rechnung 2026-114.", "meta": map[string]any{"via": "call"}}, http.StatusAccepted))
	var taskID uuid.UUID
	wartenAuf(t, "the task is opened", func() bool {
		return s.pool.QueryRow(ctx, `SELECT task_id FROM conversation_messages WHERE id = $1 AND task_id IS NOT NULL`, auftrag).Scan(&taskID) == nil
	})
	wartenAuf(t, "the acknowledgement is written", func() bool {
		var n int
		_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE reply_to = $1`, auftrag).Scan(&n)
		return n == 1
	})
	text, meta = antwortAuf(t, s, auftrag)
	if !strings.Contains(text, "2026-114") || meta[chat.MetaSpoken] != "Mach ich, ich schau mir die Rechnung an." {
		t.Fatalf("the acknowledgement in a call: %q %v", text, meta)
	}
	task, err := s.backlog.Get(ctx, taskID)
	if err != nil || !task.SaidInCall || task.ChatAnswer {
		t.Fatalf("the task from a call: %+v %v", task, err)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, taskID, backlog.StateDone, "## Befund\n- 2026-114 doppelt gebucht\n- zweite Buchung storniert", ""); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)
	var said string
	if err := s.pool.QueryRow(ctx, `SELECT text, coalesce(meta, '{}'::jsonb) FROM conversation_messages
		WHERE task_id = $1 AND kind = 'result'`, taskID).Scan(&said, &meta); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(said, "2026-114") || !strings.Contains(meta[chat.MetaSpoken], "doppelt gebucht") || meta[chat.MetaDetailsInChat] != "true" {
		t.Fatalf("the result of a task from a call: %q %v", said, meta)
	}
}

// TestAResultFromACallIsPostedWithoutASpokenFormWhenNoModelAnswers: the
// spoken form of a result is a nicety — without a model the result is
// posted as ever, and the call speaks the written message.
func TestAResultFromACallIsPostedWithoutASpokenFormWhenNoModelAnswers(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("ohne-modell")
	s.ohneLaeufe(agent.ID)
	conv := direkt(t, s, s.adminID, agent.ID)
	task, err := s.backlog.CreateFromMessage(ctx, s.orgID, agent.ID, "Prüfen", "Prüfen", "chat:admin@test.local", conv, false, true)
	if err != nil || !task.SaidInCall {
		t.Fatal(task, err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, task.ID, backlog.StateDone, "Alles in Ordnung.", ""); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)
	var text string
	var meta map[string]string
	if err := s.pool.QueryRow(ctx, `SELECT text, coalesce(meta, '{}'::jsonb) FROM conversation_messages
		WHERE task_id = $1 AND kind = 'result'`, task.ID).Scan(&text, &meta); err != nil {
		t.Fatal(err)
	}
	if text != "Alles in Ordnung." || meta[chat.MetaSpoken] != "" {
		t.Fatalf("the result without a model: %q %v", text, meta)
	}
}

// TestAChatAnswerSaidInACallEndsWithItsSpokenForm is #502 without the
// triage (#483): the run of a message said in a call is told to end its
// reply with its spoken form; covey takes the tag off the reply and keeps
// the spoken form in the meta. A typed message's run is told nothing of it.
func TestAChatAnswerSaidInACallEndsWithItsSpokenForm(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("anruf-ohne-triage")
	admin := teamLogin(t, s)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	path := "/api/v1/conversations/" + c["id"].(string) + "/messages"
	taskAus := func(out map[string]any) uuid.UUID {
		t.Helper()
		tasks, _ := out["tasks"].([]any)
		if len(tasks) != 1 {
			t.Fatalf("one task per message: %v", out)
		}
		id, _ := uuid.Parse(tasks[0].(map[string]any)["id"].(string))
		return id
	}
	fertig := func(id uuid.UUID) backlog.Task {
		t.Helper()
		waitFor(t, "the answer run finishes", 40*time.Second, func() bool { return s.taskState(id) == backlog.StateDone })
		got, err := s.backlog.Get(ctx, id)
		if err != nil || got.Result == nil {
			t.Fatalf("result: %v %v", got.Result, err)
		}
		return got
	}

	// The prompt a run from a call gets, and one from a typed message.
	imAnruf := fertig(taskAus(admin.expect(http.MethodPost, path, map[string]any{"text": "Zeig den Prompt.\n[mock:prompt]", "meta": map[string]any{"via": "call"}}, http.StatusCreated)))
	if !imAnruf.SaidInCall || !imAnruf.ChatAnswer || !strings.Contains(*imAnruf.Result, "It was said in a call") {
		t.Fatalf("the run of a chat answer from a call was not asked for a spoken form: %+v", imAnruf)
	}
	getippt := fertig(taskAus(admin.expect(http.MethodPost, path, map[string]any{"text": "Zeig den Prompt.\n[mock:prompt]"}, http.StatusCreated)))
	if getippt.SaidInCall || strings.Contains(*getippt.Result, "It was said in a call") {
		t.Fatal("the run of a typed chat answer was asked for a spoken form")
	}

	/* A colleague of its own for the reply: the body of a run carries the
	   end of its conversation, and the mock runs every directive in it. */
	zweiter := s.newSupportAgent("anruf-antwort")
	c2 := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": zweiter.ID}}, http.StatusCreated)
	path = "/api/v1/conversations/" + c2["id"].(string) + "/messages"
	antwort := fertig(taskAus(admin.expect(http.MethodPost, path, map[string]any{
		"text": "Der Login-Fix ist noch nicht drin, oder?\n[mock:result " + anrufGeschrieben + "\n\n<spoken details_in_chat=\"true\">" + anrufGesprochen + "</spoken>]",
		"meta": map[string]any{"via": "call"}}, http.StatusCreated)))
	s.srv.Nacherzaehlen(ctx)
	var text string
	var meta map[string]string
	if err := s.pool.QueryRow(ctx, `SELECT text, coalesce(meta, '{}'::jsonb) FROM conversation_messages
		WHERE task_id = $1 AND author_kind = 'agent'`, antwort.ID).Scan(&text, &meta); err != nil {
		t.Fatal(err)
	}
	if text != anrufGeschrieben || meta[chat.MetaSpoken] != anrufGesprochen || meta[chat.MetaDetailsInChat] != "true" || meta[chat.ReportsMeta] != "result" {
		t.Fatalf("the reply of a chat answer from a call: %q %v", text, meta)
	}
}
