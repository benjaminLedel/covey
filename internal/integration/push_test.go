package integration

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"

	"covey/internal/chat"
	"covey/internal/push"
)

type fakeSender struct {
	mu   sync.Mutex
	sent []push.Message
	gone map[string]bool
}

func (f *fakeSender) Send(_ context.Context, m push.Message) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.gone[m.Token] {
		return push.ErrGone
	}
	f.sent = append(f.sent, m)
	return nil
}

func (f *fakeSender) take() []push.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := f.sent
	f.sent = nil
	return out
}

// TestAnAnswerReachesThePhone (#379): the person who wrote in a thread gets
// the agent's answer on their device, once, in their language, without the
// content unless the organisation allows it; what they have read is not
// pushed, and a device Apple no longer knows is forgotten.
func TestAnAnswerReachesThePhone(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("push-agent")
	admin := teamLogin(t, s)
	store := chat.New(s.pool)
	sender := &fakeSender{gone: map[string]bool{}}
	n := &push.Notifier{Pool: s.pool, Sender: sender, Lag: time.Millisecond}

	admin.expect(http.MethodPost, "/api/v1/me/push/devices", map[string]any{
		"token": "tok-a", "platform": "ios", "environment": "development", "lang": "de-DE",
	}, http.StatusNoContent)
	admin.expect(http.MethodPost, "/api/v1/me/push/devices", map[string]any{
		"token": "bad/token", "platform": "ios", "environment": "development",
	}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/me/push/devices", map[string]any{
		"token": "tok-a", "platform": "ios", "environment": "development", "sound": "../../x",
	}, http.StatusBadRequest)

	round := func() []push.Message {
		t.Helper()
		time.Sleep(20 * time.Millisecond)
		if _, err := n.Round(ctx); err != nil {
			t.Fatal(err)
		}
		return sender.take()
	}
	round() // the cursor starts at the migration; move it past the setup

	if _, err := store.Add(ctx, agent.OrgID, agent.ID, "chat:admin@test.local", "Wie weit bist du?", false); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Add(ctx, agent.OrgID, agent.ID, "agent", "## Stand\nDie Rechnung ist geprüft.", false); err != nil {
		t.Fatal(err)
	}
	got := round()
	if len(got) != 1 || got[0].Token != "tok-a" || got[0].Environment != "development" {
		t.Fatalf("one notification to the person who wrote: %+v", got)
	}
	if got[0].Sound != "covey-bot-answer.caf" {
		t.Fatalf("the bot's answer is the default sound (#381): %q", got[0].Sound)
	}
	if got[0].Body != "" || got[0].Title == "" || got[0].AgentID != agent.ID.String() || got[0].Badge != 1 {
		t.Fatalf("without preview no content, in German, the badge is the unread count: %+v", got[0])
	}
	if again := round(); len(again) != 0 {
		t.Fatalf("an entry goes out once: %+v", again)
	}

	// With the preview the first line travels.
	admin.expect(http.MethodPatch, "/api/v1/org/push", map[string]any{"preview": true}, http.StatusOK)
	if _, err := store.Add(ctx, agent.OrgID, agent.ID, "agent", "**Erledigt:** Beleg abgelegt.", false); err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/me/push/devices", map[string]any{
		"token": "tok-a", "platform": "ios", "environment": "development", "lang": "de", "sound": "glas",
	}, http.StatusNoContent)
	got = round()
	if len(got) != 1 || got[0].Body != "Erledigt: Beleg abgelegt." {
		t.Fatalf("with preview the first line is the body: %+v", got)
	}
	if got[0].Sound != "covey-glas-answer.caf" {
		t.Fatalf("the chosen family: %q", got[0].Sound)
	}

	// Read first, then nothing is pushed.
	m, err := store.Add(ctx, agent.OrgID, agent.ID, "agent", "Noch etwas.", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkRead(ctx, adminID(t, s), agent.ID, m.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if got := round(); len(got) != 0 {
		t.Fatalf("what was read is not pushed: %+v", got)
	}

	// A device Apple no longer knows is forgotten.
	sender.gone["tok-a"] = true
	if _, err := store.Add(ctx, agent.OrgID, agent.ID, "agent", "Und noch.", false); err != nil {
		t.Fatal(err)
	}
	round()
	var left int
	_ = s.pool.QueryRow(ctx, `SELECT count(*) FROM push_devices WHERE token='tok-a'`).Scan(&left)
	if left != 0 {
		t.Fatal("a gone token stays registered")
	}
}

func adminID(t *testing.T, s *stack) (id [16]byte) {
	t.Helper()
	if err := s.pool.QueryRow(context.Background(), `SELECT id FROM humans WHERE email='admin@test.local'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}
