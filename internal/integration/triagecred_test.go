package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"covey/internal/claudeapi"
	"covey/internal/daemon"
)

// TestTriageFindsTheSeatCredential is #483: an organisation whose engine access
// sits on a Claude Code seat under a name of its own — not under
// anthropic_api_key or claude_code_oauth_token — has agents that run, and has
// to have a triage that finds the same access. Before, the chat settings said
// "off, unavailable" there and every greeting became a sandbox run.
func TestTriageFindsTheSeatCredential(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("seat-triage")
	s.ohneLaeufe(agent.ID)
	admin := teamLogin(t, s)

	status := func() bool {
		got := admin.expect(http.MethodGet, "/api/v1/org/chat-triage", nil, http.StatusOK)
		v, _ := got["available"].(bool)
		return v
	}
	if status() {
		t.Fatal("without any credential the triage cannot be available")
	}

	rt, err := s.runtimes.Create(ctx, s.orgID, "claude-code", "Claude Team", "")
	if err != nil {
		t.Fatal(err)
	}
	// An agent-own secret is no seat and must not count: it is that agent's
	// access, not the organisation's.
	if err := s.secrets.PutAgent(ctx, s.orgID, agent.ID, "claude_code_oauth_token", "sk-ant-oat01-private"); err != nil {
		t.Fatal(err)
	}
	if status() {
		t.Fatal("an agent-own secret made the organisation's triage available")
	}

	// Two values under one key: the seat names slot 1, and slot 1 is what the
	// control plane has to use, exactly as a run would.
	s.seat(t, rt.ID, daemon.CredSubscription, "team_claude", "sk-ant-oat01-unused", "first value")
	if _, err := s.pool.Exec(ctx, "DELETE FROM runtime_credentials WHERE runtime_id=$1", rt.ID); err != nil {
		t.Fatal(err)
	}
	ord := s.seat(t, rt.ID, daemon.CredSubscription, "team_claude", "sk-ant-oat01-seat", "Team seat")
	if !status() {
		t.Fatal("a credential on the Claude Code seat has to make the triage available")
	}

	// Paused by hand is out of play for the control plane too.
	if err := s.runtimes.SetPaused(ctx, s.orgID, rt.ID, ord, true); err != nil {
		t.Fatal(err)
	}
	if status() {
		t.Fatal("a paused seat made the triage available")
	}
	if err := s.runtimes.SetPaused(ctx, s.orgID, rt.ID, ord, false); err != nil {
		t.Fatal(err)
	}

	// And the turn itself goes out with the seat's value, as Bearer (the
	// prefix says it is a subscription token).
	var (
		mu   sync.Mutex
		auth []string
	)
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth = append(auth, r.Header.Get("Authorization"))
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": `{"action":"answer","text":"Alles gut, danke!"}`}},
		})
	}))
	defer fake.Close()
	old := claudeapi.BaseURL
	claudeapi.BaseURL = fake.URL
	defer func() { claudeapi.BaseURL = old }()

	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": "Hey, wie läuft's?"}, http.StatusAccepted)
	wartenAuf(t, "the triage answers", func() bool {
		return enthaelt(eintraege(t, admin, agent.ID), "answer: Alles gut")
	})
	mu.Lock()
	defer mu.Unlock()
	if len(auth) == 0 || auth[0] != "Bearer sk-ant-oat01-seat" {
		t.Fatalf("the turn has to authenticate with the seat's value in slot %d, got %v", 1, auth)
	}
	var tasks int
	if err := s.pool.QueryRow(ctx, "SELECT count(*) FROM backlog_tasks WHERE agent_id=$1", agent.ID).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if tasks != 0 {
		t.Fatalf("a greeting answered by the triage opened %d tasks", tasks)
	}
}
