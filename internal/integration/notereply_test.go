package integration

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"covey/internal/llm"
)

// notierendesModell writes onto the first open task it is shown and says a
// sentence in the conversation — the reply field of a note (#460).
type notierendesModell struct {
	mu    sync.Mutex
	reply string
}

var kurzeKennung = regexp.MustCompile(`\[([0-9a-f]{4})\]`)

func (m *notierendesModell) Name() string { return "test" }

func (m *notierendesModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := kurzeKennung.FindStringSubmatch(req.Messages[0].Content)
	if k == nil {
		return `{"action":"answer","text":"?"}`, nil
	}
	return `{"action":"note","task":"` + k[1] + `","text":"Globex hat eine Gutschrift geschickt.","reply":"` + m.reply + `"}`, nil
}

// TestANoteIsAnsweredInTheConversation walks #460: a message that adds to an
// open task becomes a note on it — and what the agent says about it reaches
// the conversation, instead of the person hearing nothing.
func TestANoteIsAnsweredInTheConversation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("notizantwort")
	s.ohneLaeufe(agent.ID)
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Globex-Rechnung prüfen", "…", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	modell := &notierendesModell{reply: "Danke, nehm ich mit — ich bin noch dran."}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": "Globex hat eine Gutschrift geschickt — wie weit bist du?"}, http.StatusAccepted)
	wartenAuf(t, "the reply reaches the conversation", func() bool {
		return strings.Contains(strings.Join(eintraege(t, admin, agent.ID), "\n"), "nehm ich mit")
	})
	notes, err := s.backlog.ListNotes(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	var steht bool
	for _, n := range notes {
		steht = steht || strings.Contains(n.Content, "Gutschrift")
	}
	if !steht {
		t.Fatalf("the note must stand on the task: %+v", notes)
	}
}
