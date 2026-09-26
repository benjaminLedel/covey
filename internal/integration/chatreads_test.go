package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"covey/internal/chat"
)

// TestUnreadIsWhatTheAgentSaidSinceTheLastLook (#378): the agent's answers
// after the person's point in the thread are unread; reading moves the point
// forward to the entry shown, never back, and what the person wrote is never
// unread.
func TestUnreadIsWhatTheAgentSaidSinceTheLastLook(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("reads-agent")
	admin := teamLogin(t, s)
	store := chat.New(s.pool)

	unread := func() (int, string) {
		t.Helper()
		out := admin.expect(http.MethodGet, "/api/v1/me/threads", nil, http.StatusOK)
		for _, raw := range out["threads"].([]any) {
			th := raw.(map[string]any)
			if th["agent_id"] == agent.ID.String() {
				return int(th["unread"].(float64)), th["last_text"].(string)
			}
		}
		return 0, ""
	}

	if _, err := store.Add(ctx, agent.OrgID, agent.ID, "chat:admin@test.local", "Wie weit bist du?", false); err != nil {
		t.Fatal(err)
	}
	if n, _ := unread(); n != 0 {
		t.Fatalf("what the person wrote is not unread: %d", n)
	}
	first, err := store.Add(ctx, agent.OrgID, agent.ID, "agent", "## Stand\n\n**Halb** fertig.", false)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if _, err := store.Add(ctx, agent.OrgID, agent.ID, "agent", "Jetzt fertig.", false); err != nil {
		t.Fatal(err)
	}
	if n, last := unread(); n != 2 || last != "Jetzt fertig." {
		t.Fatalf("two answers unread, the newest shown: %d %q", n, last)
	}

	base := "/api/v1/agents/" + agent.ID.String() + "/thread/read"
	admin.expect(http.MethodPost, base, map[string]any{"at": first.CreatedAt}, http.StatusNoContent)
	if n, _ := unread(); n != 1 {
		t.Fatalf("read up to the first answer, the second stays unread: %d", n)
	}
	admin.expect(http.MethodPost, base, map[string]any{"at": first.CreatedAt.Add(-time.Hour)}, http.StatusNoContent)
	if n, _ := unread(); n != 1 {
		t.Fatalf("the point never moves back: %d", n)
	}
	admin.expect(http.MethodPost, base, map[string]any{"at": time.Now().Add(time.Minute)}, http.StatusNoContent)
	if n, _ := unread(); n != 0 {
		t.Fatalf("all read: %d", n)
	}
	admin.expect(http.MethodPost, base, map[string]any{}, http.StatusBadRequest)
}
