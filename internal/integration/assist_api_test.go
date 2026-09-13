package integration

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/claudeapi"
)

// The config assistant is the one place where a model writes into a
// configuration a human then has to approve. Two properties make that safe,
// and both are what this test holds: the proposals are filtered down to the
// files that may be edited at all, and a reply the model did not shape as
// JSON still reaches the person as prose instead of disappearing.
func TestConfigAssistantProposesOnlyEditableFiles(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("assistenz-agent")

	// The control plane's own model access. BaseURL is a variable so a test
	// can slip in a server — that is what its comment says it is for.
	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "sk-ant-test"); err != nil {
		t.Fatal(err)
	}
	var sawSystem string
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var req struct {
			System []struct {
				Text string `json:"text"`
			} `json:"system"`
		}
		_ = json.Unmarshal(raw, &req)
		for _, blk := range req.System {
			sawSystem += blk.Text
		}
		// A fenced answer with prose around it, which is what a model
		// actually returns, and one proposal for a file nobody may edit here.
		inner := `{"reply":"Ich habe den Ton ergänzt.","proposals":[` +
			`{"file":"SOUL.md","content":"# Support\n\n## Tone\nFreundlich."},` +
			`{"file":"TOOLS.md","content":"- zammad: reply"}]}`
		answer := "Gerne.\n```json\n" + inner + "\n```"
		_ = json.NewEncoder(w).Encode(map[string]any{
			"content": []any{map[string]any{"type": "text", "text": answer}},
		})
	}))
	defer fake.Close()
	old := claudeapi.BaseURL
	claudeapi.BaseURL = fake.URL
	defer func() { claudeapi.BaseURL = old }()

	// With a credential the interface is told the assistant exists.
	status := admin.expect(http.MethodGet, "/api/v1/assist/status", nil, http.StatusOK)
	if available, _ := status["available"].(bool); !available {
		t.Fatal("the assistant reports itself unavailable although a credential is deposited")
	}

	got := admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/config/assist",
		map[string]any{
			"messages": []any{map[string]any{"role": "user", "content": "Der Ton ist zu knapp."}},
			"files":    map[string]string{"SOUL.md": "# Support\n\n## Role\nSupport."},
		}, http.StatusOK)

	if reply, _ := got["reply"].(string); !strings.Contains(reply, "Ton ergänzt") {
		t.Errorf("the reply is %v", got["reply"])
	}
	proposals, _ := got["proposals"].([]any)
	if len(proposals) != 1 {
		t.Fatalf("got %d proposals, expected only the editable one: %v", len(proposals), proposals)
	}
	first, _ := proposals[0].(map[string]any)
	if first["file"] != "SOUL.md" {
		t.Errorf("the surviving proposal is for %v", first["file"])
	}

	// TOOLS.md was merged into ACCESS.md and is dropped: a proposal for a file
	// the editor no longer manages would be a change somebody accepts and
	// nothing applies.
	//
	// ACCESS.md and EGRESS.md are NOT dropped here, and that is deliberate —
	// they are editable, and the role boundary sits where they are SAVED (the
	// write-through wants the security role). Filtering them out here would
	// move an access decision into a place that cannot make it.
	for _, p := range proposals {
		m, _ := p.(map[string]any)
		if m["file"] == "TOOLS.md" {
			t.Error("a proposal for the merged-away TOOLS.md was offered")
		}
	}

	// The draft the person is looking at travels with the question: the
	// assistant advises on the unsaved text, not on the last saved version.
	if !strings.Contains(sawSystem, "SOUL.md") {
		t.Error("the current draft did not reach the model")
	}
}

// A model that does not answer JSON at all still reaches the person: the reply
// is handed through as prose rather than swallowed, because a dialogue that
// silently drops a turn is worse than one that shows an odd answer.
func TestConfigAssistantPassesProseThrough(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("prosa-agent")

	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "sk-ant-test"); err != nil {
		t.Fatal(err)
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"content":[{"type":"text","text":"Dazu fällt mir nichts ein."}]}`)
	}))
	defer fake.Close()
	old := claudeapi.BaseURL
	claudeapi.BaseURL = fake.URL
	defer func() { claudeapi.BaseURL = old }()

	got := admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/config/assist",
		map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Hallo"}}}, http.StatusOK)
	if reply, _ := got["reply"].(string); !strings.Contains(reply, "nichts ein") {
		t.Errorf("the prose reply was lost: %v", got)
	}
	if proposals, _ := got["proposals"].([]any); len(proposals) != 0 {
		t.Errorf("proposals were invented: %v", proposals)
	}
}

// A provider that cannot be reached is a gateway error, not a 500: the fault
// is at the far end, and the person should be told to try again rather than
// that covey is broken.
func TestConfigAssistantWhenTheProviderIsUnreachable(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("unerreichbar-agent")

	if err := s.secrets.Put(ctx, s.orgID, "anthropic_api_key", "sk-ant-test"); err != nil {
		t.Fatal(err)
	}
	old := claudeapi.BaseURL
	claudeapi.BaseURL = "http://127.0.0.1:1"
	defer func() { claudeapi.BaseURL = old }()

	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/config/assist",
		map[string]any{"messages": []any{map[string]any{"role": "user", "content": "Hallo"}}},
		http.StatusBadGateway)

	// And a question without a question is a bad request rather than a call
	// that costs the organisation money for nothing.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/config/assist",
		map[string]any{"messages": []any{}}, http.StatusBadRequest)
}
