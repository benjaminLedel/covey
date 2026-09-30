package integration

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/chat"
	"covey/internal/llm"
)

// begruessendesModell answers the greeting's turn with what it is given,
// or blocks until the turn's deadline when slow. It keeps the prompt.
type begruessendesModell struct {
	mu      sync.Mutex
	antwort string
	slow    bool
	req     llm.Request
}

func (m *begruessendesModell) Name() string { return "test" }

func (m *begruessendesModell) Complete(ctx context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	m.req = req
	antwort, slow := m.antwort, m.slow
	m.mu.Unlock()
	if slow {
		<-ctx.Done()
		return "", ctx.Err()
	}
	return antwort, nil
}

func (m *begruessendesModell) prompt() llm.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.req
}

// TestACallGreetingFitsTheSituation is #513: the greeting is written from
// the caller's clock, the agent's tasks in progress and finished today, and
// the conversation's last lines; an answer the guard refuses, a slow model
// and no credential are 204, so the app greets from its templates; and only
// a member of the conversation may ask, about an agent in it.
func TestACallGreetingFitsTheSituation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("begruesser")
	s.ohneLaeufe(agent.ID)
	modell := &begruessendesModell{antwort: "Guten Morgen, Admin! Wie steht's mit der Umfrage, sollen wir weitermachen?"}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	s.srv.CallGreetingTimeout = 200 * time.Millisecond
	admin := teamLogin(t, s)
	c := admin.expect(http.MethodPost, "/api/v1/conversations", map[string]any{
		"kind": "direct", "member": map[string]any{"kind": "agent", "id": agent.ID}}, http.StatusCreated)
	convID, _ := uuid.Parse(c["id"].(string))

	laeuft, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Kundenumfrage auswerten", "", "test", 5)
	if err != nil {
		t.Fatal(err)
	}
	fertig, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Newsletter verschicken", "", "test", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Noch offen", "", "test", 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state = CASE WHEN id = $1 THEN 'in_progress' ELSE 'done' END
		WHERE id IN ($1, $2)`, laeuft.ID, fertig.ID); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Kannst du die Umfrage auswerten?", "Mach ich, ich melde mich."} {
		m := chat.Message{ConversationID: convID, OrgID: s.orgID, AuthorKind: "human", AuthorID: &s.adminID, Text: text}
		if strings.HasPrefix(text, "Mach") {
			m.AuthorKind, m.AuthorID = "agent", &agent.ID
		}
		if _, _, err := s.srv.Chat.Post(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	path := "/api/v1/conversations/" + convID.String() + "/greeting"
	local := time.Now().In(time.FixedZone("", 2*60*60)).Format(time.RFC3339)
	body := map[string]any{"agent_id": agent.ID, "lang": "de", "local_time": local, "weekday": "Montag"}
	got := admin.expect(http.MethodPost, path, body, http.StatusOK)
	if got["text"] != "Guten Morgen, Admin! Wie steht's mit der Umfrage, sollen wir weitermachen?" {
		t.Fatalf("the greeting: %v", got)
	}
	req := modell.prompt()
	user := req.Messages[0].Content
	for _, want := range []string{"You are: Support-Agent", "The caller: Admin", "Language: de", "Montag, ",
		"- Kundenumfrage auswerten", "You finished today: Newsletter verschicken",
		"Admin: Kannst du die Umfrage auswerten?", "you: Mach ich, ich melde mich."} {
		if !strings.Contains(user, want) {
			t.Fatalf("%q missing from the greeting's prompt:\n%s", want, user)
		}
	}
	if strings.Contains(user, "Noch offen") || req.Tier != llm.TierFast {
		t.Fatalf("only the tasks in progress, on the fast tier:\n%s", user)
	}

	// Nothing is stored: the greeting is not a message.
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM conversation_messages WHERE conversation_id = $1`, convID).Scan(&n); err != nil || n != 2 {
		t.Fatalf("%d messages after the greeting (%v), want the two", n, err)
	}

	// The guard refuses an answer without a question; a slow model runs out.
	modell.mu.Lock()
	modell.antwort = "Guten Morgen, Admin."
	modell.mu.Unlock()
	admin.expect(http.MethodPost, path, body, http.StatusNoContent)
	modell.mu.Lock()
	modell.slow = true
	modell.mu.Unlock()
	start := time.Now()
	admin.expect(http.MethodPost, path, body, http.StatusNoContent)
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("the slow model held the greeting %v", d)
	}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return nil, llm.ErrNoCredential }
	admin.expect(http.MethodPost, path, body, http.StatusNoContent)

	// What is asked for.
	admin.expect(http.MethodPost, path, map[string]any{"agent_id": agent.ID, "local_time": "morgens"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, path, map[string]any{"agent_id": uuid.NewString(), "local_time": local}, http.StatusNotFound)

	// Only a member of the conversation.
	s.mitglied(t, "ada@test.local", "Ada", "agent_owner", "ada-passwort")
	ada := login(t, s, "ada@test.local", "ada-passwort")
	ada.expect(http.MethodPost, path, body, http.StatusNotFound)
}
