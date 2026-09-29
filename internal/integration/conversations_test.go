package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/llm"
	"covey/internal/push"
)

// direkt opens the direct conversation between a person and an agent.
func direkt(t *testing.T, s *stack, human, agent uuid.UUID) uuid.UUID {
	t.Helper()
	id, err := chat.New(s.pool).Direct(context.Background(), s.orgID, chat.Human(human), chat.Agent(agent), &human)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sagt writes a line into a conversation, as the person or the agent.
func sagt(ctx context.Context, store *chat.Store, conv uuid.UUID, wer chat.Ref, text string) (chat.Message, error) {
	m, _, err := store.Post(ctx, chat.Message{ConversationID: conv, AuthorKind: wer.Kind, AuthorID: &wer.ID, Text: text})
	return m, err
}

// antwortendesModell answers every triage and keeps what it was shown.
type antwortendesModell struct {
	mu      sync.Mutex
	gesehen []string
}

func (m *antwortendesModell) Name() string { return "test" }

func (m *antwortendesModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gesehen = append(m.gesehen, req.Messages[0].Content)
	return `{"action":"answer","text":"Gern, mach ich."}`, nil
}

func (m *antwortendesModell) prompts() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.gesehen...)
}

// eintraege reads the per-agent thread as "kind: text" lines.
func eintraege(t *testing.T, c *apiClient, agentID uuid.UUID) []string {
	t.Helper()
	verlauf := c.expect(http.MethodGet, "/api/v1/agents/"+agentID.String()+"/thread", nil, http.StatusOK)
	var out []string
	for _, roh := range verlauf["entries"].([]any) {
		e := roh.(map[string]any)
		out = append(out, e["kind"].(string)+": "+e["text"].(string))
	}
	return out
}

// nachrichten reads a conversation's messages as texts.
func nachrichten(t *testing.T, c *apiClient, conv string) []map[string]any {
	t.Helper()
	page := c.expect(http.MethodGet, "/api/v1/conversations/"+conv+"/messages", nil, http.StatusOK)
	var out []map[string]any
	for _, roh := range page["messages"].([]any) {
		out = append(out, roh.(map[string]any))
	}
	return out
}

func enthaelt(zeilen []string, teil string) bool {
	for _, z := range zeilen {
		if strings.Contains(z, teil) {
			return true
		}
	}
	return false
}

// TestEachPersonHasTheirOwnConversationWithAnAgent is #440: two people who
// write to the same agent each have a direct conversation of their own, one
// per pair, and neither reads the other's — in the thread, in the list, in
// the API. The agent's triage answers each from that conversation alone.
func TestEachPersonHasTheirOwnConversationWithAnAgent(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("zwei-leute")
	s.ohneLaeufe(agent.ID)
	modell := &antwortendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Das Passwort des Tresors ist Kolibri."}, http.StatusAccepted)
	wartenAuf(t, "the admin's message is answered", func() bool { return len(modell.prompts()) == 1 })
	ada.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Wie geht es dir?"}, http.StatusAccepted)
	wartenAuf(t, "Ada's message is answered", func() bool { return len(modell.prompts()) == 2 })

	if p := modell.prompts()[1]; strings.Contains(p, "Kolibri") {
		t.Fatalf("Ada's triage read the admin's conversation:\n%s", p)
	}
	wartenAuf(t, "both answers stand", func() bool {
		return enthaelt(eintraege(t, admin, agent.ID), "answer: Gern") && enthaelt(eintraege(t, ada, agent.ID), "answer: Gern")
	})
	if z := eintraege(t, ada, agent.ID); enthaelt(z, "Kolibri") || !enthaelt(z, "message: Wie geht es dir?") {
		t.Fatalf("Ada's thread: %v", z)
	}
	if z := eintraege(t, admin, agent.ID); enthaelt(z, "Wie geht es dir") || !enthaelt(z, "message: Das Passwort") {
		t.Fatalf("the admin's thread: %v", z)
	}

	// One per pair: opening it again is the same conversation.
	eins := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	zwei := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	if eins["id"] != zwei["id"] {
		t.Fatalf("two direct conversations for one pair: %v %v", eins["id"], zwei["id"])
	}
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM conversations c JOIN conversation_members cm ON cm.conversation_id = c.id
		WHERE c.kind = 'direct' AND cm.member_kind = 'agent' AND cm.member_id = $1`, agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("%d direct conversations with the agent, want 2 (one per person)", n)
	}
	// The admin's conversation is not Ada's to read, not even by its id.
	ada.expect(http.MethodGet, "/api/v1/conversations/"+eins["id"].(string)+"/messages", nil, http.StatusNotFound)
	ada.expect(http.MethodPost, "/api/v1/conversations/"+eins["id"].(string)+"/messages", map[string]any{"text": "hallo"}, http.StatusNotFound)
	liste := ada.expect(http.MethodGet, "/api/v1/conversations", nil, http.StatusOK)
	for _, roh := range liste["conversations"].([]any) {
		if roh.(map[string]any)["id"] == eins["id"] {
			t.Fatal("the admin's conversation is in Ada's list")
		}
	}
	// The alias and the conversation are the same thing.
	msgs := nachrichten(t, admin, eins["id"].(string))
	if len(msgs) != 2 || msgs[0]["text"] != "Das Passwort des Tresors ist Kolibri." || msgs[1]["author_kind"] != "agent" {
		t.Fatalf("the conversation behind the alias: %v", msgs)
	}
}

// TestInAGroupAnAgentAnswersWhenAddressed: a group of people and an agent is
// people talking until somebody addresses the agent — by @slug, by @name, or
// by replying to a line of its own. Every member reads everything.
func TestInAGroupAnAgentAnswersWhenAddressed(t *testing.T) {
	s := newStack(t)
	agent := s.newSupportAgent("gruppen-agent")
	s.ohneLaeufe(agent.ID)
	modell := &antwortendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	adaID := s.mitglied(t, "ada@test.local", "Ada", "auditor", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")

	g := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "group", "title": "Rechnungen",
		"members": []any{map[string]any{"kind": "human", "id": adaID}, map[string]any{"kind": "agent", "id": agent.ID}},
	}, http.StatusCreated)
	gid := g["id"].(string)
	pfad := "/api/v1/conversations/" + gid + "/messages"
	if len(g["members"].([]any)) != 3 {
		t.Fatalf("members: %v", g["members"])
	}

	// Not addressed: no turn, no answer.
	r := admin.expect(http.MethodPost, pfad, map[string]any{"text": "Ada, hast du die Globex-Rechnung gesehen?"}, http.StatusCreated)
	if r["pending"] != false {
		t.Fatalf("a message to nobody waits for a triage: %v", r)
	}
	// Membership decides, not the role: an auditor writes in a group she is in.
	ada.expect(http.MethodPost, pfad, map[string]any{"text": "Ja, liegt bei mir."}, http.StatusCreated)
	if len(modell.prompts()) != 0 {
		t.Fatalf("the agent was asked although nobody addressed it: %v", modell.prompts())
	}

	// Addressed by its slug: it answers, and it saw the group.
	admin.expect(http.MethodPost, pfad, map[string]any{"text": "@gruppen-agent prüf sie bitte"}, http.StatusAccepted)
	wartenAuf(t, "the agent answers in the group", func() bool {
		for _, m := range nachrichten(t, ada, gid) {
			if m["author_kind"] == "agent" {
				return true
			}
		}
		return false
	})
	p := modell.prompts()[0]
	if !strings.Contains(p, "group conversation \"Rechnungen\"") || !strings.Contains(p, "Ada: Ja, liegt bei mir.") {
		t.Fatalf("the agent did not see the group:\n%s", p)
	}
	// "@gruppen-agenten" is somebody else.
	admin.expect(http.MethodPost, pfad, map[string]any{"text": "@gruppen-agenten kennt ihr das?"}, http.StatusCreated)
	// A reply to its own line addresses it.
	var seine string
	for _, m := range nachrichten(t, admin, gid) {
		if m["author_kind"] == "agent" {
			seine = m["id"].(string)
		}
	}
	admin.expect(http.MethodPost, pfad, map[string]any{"text": "Danke!", "reply_to": seine}, http.StatusAccepted)
	wartenAuf(t, "the reply is answered", func() bool { return len(modell.prompts()) == 2 })
}

// TestPeopleTalkToEachOther: a direct conversation between two people is a
// chat without a triage; what one writes is unread for the other until read.
func TestPeopleTalkToEachOther(t *testing.T) {
	s := newStack(t)
	modell := &antwortendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	adaID := s.mitglied(t, "ada@test.local", "Ada", "controlling", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")

	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "human", "id": adaID}}, http.StatusCreated)
	id := c["id"].(string)
	admin.expect(http.MethodPost, "/api/v1/conversations/"+id+"/messages", map[string]any{"text": "Mittag um zwölf?"}, http.StatusCreated)

	unread := func(cl *apiClient) float64 {
		t.Helper()
		for _, roh := range cl.expect(http.MethodGet, "/api/v1/conversations", nil, http.StatusOK)["conversations"].([]any) {
			if k := roh.(map[string]any); k["id"] == id {
				return k["unread"].(float64)
			}
		}
		t.Fatal("the conversation is not in the list")
		return 0
	}
	if unread(ada) != 1 || unread(admin) != 0 {
		t.Fatalf("unread: Ada %v, admin %v — want 1 and 0", unread(ada), unread(admin))
	}
	msgs := nachrichten(t, ada, id)
	ada.expect(http.MethodPost, "/api/v1/conversations/"+id+"/read", map[string]any{"at": msgs[0]["created_at"]}, http.StatusNoContent)
	if unread(ada) != 0 {
		t.Fatal("read, and still unread")
	}
	if len(modell.prompts()) != 0 {
		t.Fatal("a conversation of people went through a triage")
	}
	// Nobody leaves a direct conversation, and nobody joins it.
	admin.expect(http.MethodPost, "/api/v1/conversations/"+id+"/members", map[string]any{"kind": "human", "id": s.adminID}, http.StatusConflict)
}

// TestATaskReportsWhereItCameFrom is the other half of #440: a task opened in
// a conversation reports its outcome there and nowhere else; a task from the
// backlog reports into no conversation, and its question goes to the
// agent's supervisor — or nowhere, when there is none.
func TestATaskReportsWhereItCameFrom(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("melder")
	s.ohneLaeufe(agent.ID)
	admin := teamLogin(t, s)
	adaID := s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	base := "/api/v1/agents/" + agent.ID.String()

	// Without the triage a message is a task, and it knows where it answers.
	created := admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Globex-Rechnung prüfen"}, http.StatusCreated)
	aus := created["task"].(map[string]any)
	if aus["conversation_id"] == nil {
		t.Fatalf("the task does not know its conversation: %v", aus)
	}
	chatTask, _ := uuid.Parse(aus["id"].(string))
	ada.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Hallo"}, http.StatusCreated)

	fertig := func(id uuid.UUID, ergebnis string) {
		t.Helper()
		if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := s.backlog.Complete(ctx, id, backlog.StateDone, ergebnis, ""); err != nil {
			t.Fatal(err)
		}
	}
	fertig(chatTask, "Rechnung 4711 ist doppelt gebucht.")
	// A task from the backlog, done, and one that parks with a question.
	vomBacklog, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Webhook: Ticket 12", "", "webhook:zammad", 0)
	if err != nil {
		t.Fatal(err)
	}
	fertig(vomBacklog.ID, "Ticket 12 beantwortet.")
	fragt, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Webhook: Ticket 13", "", "webhook:zammad", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, fragt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Block(ctx, fragt.ID, "", "", "Ohne Aufsicht: Darf ich Ticket 13 schließen?"); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)

	// Nobody supervises the agent yet: the question stands on the task only.
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE task_id = ANY($1)`,
		[]uuid.UUID{vomBacklog.ID, fragt.ID}).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("%d messages from backlog tasks in conversations, want none", n)
	}
	if z := eintraege(t, admin, agent.ID); !enthaelt(z, "result: Rechnung 4711") || enthaelt(z, "Ticket 12") {
		t.Fatalf("the admin's thread: %v", z)
	}
	if z := eintraege(t, ada, agent.ID); enthaelt(z, "Rechnung 4711") || enthaelt(z, "Ticket") {
		t.Fatalf("Ada's thread carries what is not hers: %v", z)
	}

	// With Ada as the agent's supervisor, a question from the backlog goes to
	// her direct conversation with the agent — and to nobody else.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET supervisor_id=$2 WHERE id=$1`, agent.ID, adaID); err != nil {
		t.Fatal(err)
	}
	zweite, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Webhook: Ticket 14", "", "webhook:zammad", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, zweite.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Block(ctx, zweite.ID, "", "", "Darf ich Ticket 14 schließen?"); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)
	s.srv.Nacherzaehlen(ctx) // twice: delivered once
	/* The question parked while nobody supervised is still waiting, and now
	   there is somebody to ask: it arrives too. */
	z := eintraege(t, ada, agent.ID)
	if len(z) != 3 || !enthaelt(z, "question: Darf ich Ticket 14 schließen?") || !enthaelt(z, "question: Ohne Aufsicht") {
		t.Fatalf("the supervisor's thread: %v", z)
	}
	if z := eintraege(t, admin, agent.ID); enthaelt(z, "Ticket 1") {
		t.Fatalf("a question reached somebody who is not the supervisor: %v", z)
	}
}

// TestPushGoesToTheMembers (#440): a message is pushed to the conversation's
// people — not to its author, not to who muted it, not to anybody outside.
func TestPushGoesToTheMembers(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := teamLogin(t, s)
	adaID := s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	bobID := s.mitglied(t, "bob@test.local", "Bob", "agent_owner", "bob-passwort")
	bob := login(t, s, "bob@test.local", "bob-passwort")
	s.mitglied(t, "eve@test.local", "Eve", "agent_owner", "eve-passwort")
	eve := login(t, s, "eve@test.local", "eve-passwort")
	for tok, c := range map[string]*apiClient{"tok-admin": admin, "tok-ada": ada, "tok-bob": bob, "tok-eve": eve} {
		c.expect(http.MethodPost, "/api/v1/me/push/devices", map[string]any{
			"token": tok, "platform": "android", "environment": "production", "lang": "en"}, http.StatusNoContent)
	}
	sender := &fakeSender{gone: map[string]bool{}}
	n := &push.Notifier{Pool: s.pool, Sender: sender, Lag: time.Millisecond}
	round := func() map[string]bool {
		t.Helper()
		time.Sleep(20 * time.Millisecond)
		if _, err := n.Round(ctx); err != nil {
			t.Fatal(err)
		}
		out := map[string]bool{}
		for _, m := range sender.take() {
			out[m.Token] = true
		}
		return out
	}
	round()

	g := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "group", "title": "Mittag",
		"members": []any{map[string]any{"kind": "human", "id": adaID}, map[string]any{"kind": "human", "id": bobID}},
	}, http.StatusCreated)
	gid := g["id"].(string)
	admin.expect(http.MethodPost, "/api/v1/conversations/"+gid+"/messages", map[string]any{"text": "Um zwölf?"}, http.StatusCreated)
	if got := round(); len(got) != 2 || !got["tok-ada"] || !got["tok-bob"] {
		t.Fatalf("pushed to %v, want Ada and Bob", got)
	}
	bob.expect(http.MethodPatch, "/api/v1/conversations/"+gid+"/me", map[string]any{"muted": true}, http.StatusOK)
	ada.expect(http.MethodPost, "/api/v1/conversations/"+gid+"/messages", map[string]any{"text": "Gern."}, http.StatusCreated)
	if got := round(); len(got) != 1 || !got["tok-admin"] {
		t.Fatalf("pushed to %v, want the admin only (Ada wrote, Bob muted)", got)
	}
}

// TestTheAuditReadsConversations (#440): auditor and org admin read and export
// every conversation, and the shared threads from before, which are in no
// conversation; other roles do not reach them.
func TestTheAuditReadsConversations(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("akte")
	s.ohneLaeufe(agent.ID)
	admin := teamLogin(t, s)
	adaID := s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	s.mitglied(t, "aud@test.local", "Aud", "auditor", "aud-passwort")
	aud := login(t, s, "aud@test.local", "aud-passwort")
	s.mitglied(t, "sec@test.local", "Sec", "security", "sec-passwort")
	sec := login(t, s, "sec@test.local", "sec-passwort")

	// A line of the shared thread from before #440.
	if _, err := s.pool.Exec(ctx, `INSERT INTO chat_messages (id, org_id, agent_id, author, text)
		VALUES ($1, $2, $3, 'chat:admin@test.local', '=Altes Gespräch')`, uuid.New(), s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}
	ada.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "Adas Frage"}, http.StatusCreated)

	// The shared thread is in nobody's conversation.
	for _, c := range []*apiClient{admin, ada} {
		if z := eintraege(t, c, agent.ID); enthaelt(z, "Altes Gespräch") {
			t.Fatalf("the shared thread came back: %v", z)
		}
	}
	threads := admin.expect(http.MethodGet, "/api/v1/me/threads", nil, http.StatusOK)
	if len(threads["threads"].([]any)) != 0 {
		t.Fatalf("the admin has no conversation yet, and nothing unread: %v", threads)
	}

	ada.expect(http.MethodGet, "/api/v1/audit/conversations", nil, http.StatusForbidden)
	sec.expect(http.MethodGet, "/api/v1/audit/conversations", nil, http.StatusForbidden)
	for _, c := range []*apiClient{aud, admin} {
		liste := c.expect(http.MethodGet, "/api/v1/audit/conversations?agent_id="+agent.ID.String(), nil, http.StatusOK)
		convs, legacy := liste["conversations"].([]any), liste["legacy"].([]any)
		if len(convs) != 1 || len(legacy) != 1 {
			t.Fatalf("audit list: %v", liste)
		}
		id := convs[0].(map[string]any)["id"].(string)
		voll := c.expect(http.MethodGet, "/api/v1/audit/conversations/"+id, nil, http.StatusOK)
		if msgs := voll["messages"].([]any); len(msgs) != 1 || msgs[0].(map[string]any)["text"] != "Adas Frage" {
			t.Fatalf("audit read: %v", voll)
		}
		alt := c.expect(http.MethodGet, "/api/v1/audit/legacy-threads/"+agent.ID.String(), nil, http.StatusOK)
		if alt["legacy"] != true || len(alt["messages"].([]any)) != 1 {
			t.Fatalf("legacy thread: %v", alt)
		}
	}
	// Narrowed to a person: their conversations, and no shared thread.
	perPerson := aud.expect(http.MethodGet, "/api/v1/audit/conversations?human_id="+adaID.String(), nil, http.StatusOK)
	if len(perPerson["conversations"].([]any)) != 1 || len(perPerson["legacy"].([]any)) != 0 {
		t.Fatalf("per person: %v", perPerson)
	}
	// The export is a file, and a cell never computes.
	resp := aud.do(http.MethodGet, "/api/v1/audit/legacy-threads/"+agent.ID.String()+"?format=csv", nil)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Disposition"), "attachment") ||
		!strings.Contains(string(body), "'=Altes Gespräch") {
		t.Fatalf("csv export: %d %q\n%s", resp.StatusCode, resp.Header.Get("Content-Disposition"), body)
	}
	ada.expect(http.MethodGet, "/api/v1/audit/legacy-threads/"+agent.ID.String(), nil, http.StatusForbidden)
}

// TestTheHotQueriesUseTheirIndexes holds the list of conversations and a page
// of messages to the indexes they were built for, on a table of some
// thousand rows: neither may read conversation_messages from end to end.
func TestTheHotQueriesUseTheirIndexes(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("viel")
	store := chat.New(s.pool)
	var konv []uuid.UUID
	for i := 0; i < 40; i++ {
		h := s.mitglied(t, fmt.Sprintf("p%d@test.local", i), fmt.Sprintf("P%d", i), "agent_owner", "p-passwort-123")
		konv = append(konv, direkt(t, s, h, agent.ID))
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO conversation_messages (id, conversation_id, org_id, author_kind, author_id, text, created_at)
		SELECT gen_random_uuid(), c, $2, 'agent', $3, 'Zeile ' || g, now() - make_interval(secs => g)
		  FROM unnest($1::uuid[]) c, generate_series(1, 100) g`, konv, s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}
	// And many conversations of others, so that the members are a table
	// worth an index.
	if _, err := s.pool.Exec(ctx, `WITH c AS (
		INSERT INTO conversations (id, org_id, kind, title)
		SELECT gen_random_uuid(), $1, 'group', 'Gruppe ' || g FROM generate_series(1, 3000) g RETURNING id)
		INSERT INTO conversation_members (conversation_id, member_kind, member_id)
		SELECT c.id, 'human', gen_random_uuid() FROM c, generate_series(1, 2)`, s.orgID); err != nil {
		t.Fatal(err)
	}
	for _, tab := range []string{"conversation_messages", "conversation_members", "conversations"} {
		if _, err := s.pool.Exec(ctx, "ANALYZE "+tab); err != nil {
			t.Fatal(err)
		}
	}
	plan := func(q string, args ...any) string {
		t.Helper()
		rows, err := s.pool.Query(ctx, "EXPLAIN "+q, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var zeile string
			if err := rows.Scan(&zeile); err != nil {
				t.Fatal(err)
			}
			b.WriteString(zeile + "\n")
		}
		return b.String()
	}
	var eine uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT member_id FROM conversation_members WHERE conversation_id=$1 AND member_kind='human'`, konv[0]).Scan(&eine); err != nil {
		t.Fatal(err)
	}
	for name, p := range map[string]string{
		"page": plan(chat.PageQuery, konv[0], nil, nil, 50),
		"list": plan(chat.ListQuery, eine, 200, nil),
	} {
		if strings.Contains(p, "Seq Scan on conversation_messages") {
			t.Errorf("%s reads all messages:\n%s", name, p)
		}
		if !strings.Contains(p, "idx_conversation_messages_page") {
			t.Errorf("%s does not use the page index:\n%s", name, p)
		}
	}
	if p := plan(chat.ListQuery, eine, 200, nil); !strings.Contains(p, "idx_conversation_members_member") && !strings.Contains(p, "conversation_members_pkey") {
		t.Errorf("the list does not start from the member index:\n%s", p)
	}
	if _, err := store.List(ctx, eine, 0); err != nil {
		t.Fatal(err)
	}
}

// TestAContinuationReportsWhereItsTaskWould: a run cut off at the turn limit
// goes on as a continuation (#440), and it answers into the same
// conversation; a subtask is new work and answers nowhere.
func TestAContinuationReportsWhereItsTaskWould(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("weiter")
	s.ohneLaeufe(agent.ID)
	conv := direkt(t, s, s.adminID, agent.ID)
	task, err := s.backlog.CreateIn(ctx, s.orgID, agent.ID, "Lang", "", "chat:admin@test.local", 0, &conv)
	if err != nil {
		t.Fatal(err)
	}
	weiter, err := s.backlog.CreateChild(ctx, task.ID, backlog.ChildSpec{Title: "Lang", Origin: "continuation:" + task.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	teil, err := s.backlog.CreateChild(ctx, task.ID, backlog.ChildSpec{Title: "Teil", Origin: "subtask:" + task.ID.String()})
	if err != nil {
		t.Fatal(err)
	}
	if weiter.ConversationID == nil || *weiter.ConversationID != conv {
		t.Fatalf("the continuation lost its conversation: %v", weiter.ConversationID)
	}
	if teil.ConversationID != nil {
		t.Fatalf("a subtask reports into the conversation: %v", teil.ConversationID)
	}
}

// TestRefreshingIsCheap (#447): the list answers only what changed since its
// cursor and 304 to an ETag it gave; the messages answer only what came
// after a message.
func TestRefreshingIsCheap(t *testing.T) {
	s := newStack(t)
	admin := teamLogin(t, s)
	adaID := s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	bobID := s.mitglied(t, "bob@test.local", "Bob", "agent_owner", "bob-passwort")
	mitAda := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "human", "id": adaID}}, http.StatusCreated)["id"].(string)
	mitBob := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "human", "id": bobID}}, http.StatusCreated)["id"].(string)
	erste := admin.expect(http.MethodPost, "/api/v1/conversations/"+mitAda+"/messages", map[string]any{"text": "eins"}, http.StatusCreated)
	admin.expect(http.MethodPost, "/api/v1/conversations/"+mitBob+"/messages", map[string]any{"text": "an Bob"}, http.StatusCreated)

	holen := func(pfad, etag string) (*http.Response, map[string]any) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodGet, admin.base+pfad, nil)
		if etag != "" {
			req.Header.Set("If-None-Match", etag)
		}
		resp, err := admin.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var out map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&out)
		return resp, out
	}
	// The cursor overlaps by two seconds; past that, nothing is new.
	time.Sleep(2100 * time.Millisecond)
	resp, alles := holen("/api/v1/conversations", "")
	etag := resp.Header.Get("ETag")
	if len(alles["conversations"].([]any)) != 2 || etag == "" || alles["cursor"] == nil {
		t.Fatalf("the whole list with a cursor and an ETag: %v %q", alles, etag)
	}
	if resp, _ := holen("/api/v1/conversations", etag); resp.StatusCode != http.StatusNotModified {
		t.Fatalf("unchanged list: HTTP %d, want 304", resp.StatusCode)
	}

	cursor := alles["cursor"].(string)
	if _, d := holen("/api/v1/conversations?since="+url.QueryEscape(cursor), ""); len(d["conversations"].([]any)) != 0 {
		t.Fatalf("nothing changed, and the delta has: %v", d)
	}
	admin.expect(http.MethodPost, "/api/v1/conversations/"+mitAda+"/messages", map[string]any{"text": "zwei"}, http.StatusCreated)
	_, d := holen("/api/v1/conversations?since="+url.QueryEscape(cursor), "")
	if cs := d["conversations"].([]any); len(cs) != 1 || cs[0].(map[string]any)["id"] != mitAda {
		t.Fatalf("the delta: %v", d)
	}
	if resp, _ := holen("/api/v1/conversations", etag); resp.StatusCode != http.StatusOK {
		t.Fatalf("a changed list answered %d to an old ETag", resp.StatusCode)
	}

	// Only what came after the first message.
	nach := admin.expect(http.MethodGet, "/api/v1/conversations/"+mitAda+"/messages?after="+erste["message"].(map[string]any)["id"].(string), nil, http.StatusOK)
	if ms := nach["messages"].([]any); len(ms) != 1 || ms[0].(map[string]any)["text"] != "zwei" {
		t.Fatalf("after: %v", nach)
	}
	admin.expect(http.MethodGet, "/api/v1/conversations/"+mitAda+"/messages?after="+uuid.NewString(), nil, http.StatusBadRequest)
}
