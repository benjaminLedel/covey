package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/target/mcp"
)

// An MCP server carries its endpoint in its own configuration, and auth is
// optional — the server may be reachable without a token. So a missing token
// must NOT deny: denying would make every unauthenticated MCP server
// unreachable for a reason nobody wrote down.
//
// The test drives it the way an agent does, through the broker, and reads the
// recording afterwards — which is where an operator would look too.
func TestMCPCredentialIsBrokeredWithoutAToken(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// An MCP server that answers, so the action gets as far as being made.
	var called bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			ID any `json:"id"`
		}
		_ = json.Unmarshal(raw, &req)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0", "id": req.ID,
			"result": map[string]any{"content": []any{map[string]any{"type": "text", "text": "fertig"}}},
		})
	}))
	defer srv.Close()

	if _, err := s.targets.PutMCP(ctx, s.orgID, mcp.Config{
		Name: "hauswerkzeug", Label: "Hauswerkzeug", URL: srv.URL,
		Tools: []mcp.Tool{{Name: "lesen", Description: "liest etwas"}},
	}); err != nil {
		t.Fatal(err)
	}

	agent, err := s.registry.Create(ctx, s.orgID, "mcp-nutzer", "MCP-Nutzer", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# MCP\n\n## Role\nNutzt ein MCP-Werkzeug.",
		"ACCESS.md": "- system: hauswerkzeug scope: read",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "MCP benutzen",
		`[mock:action hauswerkzeug/lesen {"was":"etwas"}]
[mock:result fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	if !called {
		t.Error("the MCP server was never reached")
	}

	// The broker granted without a token, and said so — that entry is what
	// tells an operator later why an unauthenticated server worked.
	var granted bool
	if err := s.pool.QueryRow(ctx, `SELECT count(*) > 0 FROM recording_events
		WHERE agent_id=$1 AND kind='credential'
		  AND payload->>'system'='hauswerkzeug'
		  AND (payload->>'granted')::bool
		  AND payload->>'kind'='mcp'`, agent.ID).Scan(&granted); err != nil {
		t.Fatal(err)
	}
	if !granted {
		t.Error("no credential event records the MCP grant")
	}
}

// And the boundary still holds: a system the agent's ACCESS.md does not name
// gets no credential, MCP or not.
func TestAnMCPSystemNotInAccessIsStillDenied(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	if _, err := s.targets.PutMCP(ctx, s.orgID, mcp.Config{
		Name: "fremdwerkzeug", Label: "Fremd", URL: "http://127.0.0.1:1",
	}); err != nil {
		t.Fatal(err)
	}
	agent := s.newSupportAgent("ohne-zugang")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Fremdes Werkzeug",
		`[mock:action fremdwerkzeug/lesen {}]
[mock:result trotzdem]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task terminal", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	var denied bool
	if err := s.pool.QueryRow(ctx, `SELECT count(*) > 0 FROM recording_events
		WHERE agent_id=$1 AND kind='credential'
		  AND payload->>'system'='fremdwerkzeug'
		  AND NOT (payload->>'granted')::bool`, agent.ID).Scan(&denied); err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Error("a system outside ACCESS.md was not refused, or the refusal was not recorded")
	}
}
