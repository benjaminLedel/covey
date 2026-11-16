package integration

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"covey/internal/backlog"
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

// zeigerModell writes a note onto the first open task it is shown, and keeps
// every prompt — a model that follows a message saying "about that task:
// yes, go ahead".
type zeigerModell struct {
	mu      sync.Mutex
	gesehen []string
}

func (m *zeigerModell) Name() string { return "test" }

func (m *zeigerModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gesehen = append(m.gesehen, req.Messages[0].Content)
	k := kurzeKennung.FindStringSubmatch(req.Messages[0].Content)
	if k == nil {
		return `{"action":"task","title":"Approval","body":"approved"}`, nil
	}
	return `{"action":"note","task":"` + k[1] + `","text":"yes, approved, proceed"}`, nil
}

func (m *zeigerModell) prompts() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.gesehen, "\n")
}

// TestATriageNoteAnswersOnlyWhatTheWriterMayAnswer: a note from the triage
// wakes a parked task with the writer's words as its answer — the same act
// as POST /tasks/{id}/reply, so it reaches only tasks the writer may answer
// there (darfAntworten). A seat that the reply route refuses gets its
// message turned into a task of its own; and the turn it runs is not shown
// what other people's conversations put into the backlog, neither the
// open tasks nor the finished results.
func TestATriageNoteAnswersOnlyWhatTheWriterMayAnswer(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("triage-note-rights")
	s.ohneLaeufe(agent.ID)
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	// In the admin's own conversation: one task done, one parked on a question.
	fertig := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "ADMIN-DONE summarise the board minutes"}, http.StatusCreated)
	fertigID := uuid.MustParse(fertig["task"].(map[string]any)["id"].(string))
	if _, err := s.backlog.ClaimNext(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, fertigID, backlog.StateDone, "ADMIN-RESULT minutes say layoffs", ""); err != nil {
		t.Fatal(err)
	}
	geparkt := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "ADMIN-PARKED refund the Globex invoice"}, http.StatusCreated)
	geparktID := uuid.MustParse(geparkt["task"].(map[string]any)["id"].(string))
	if tk, err := s.backlog.ClaimNext(ctx, agent.ID); err != nil || tk.ID != geparktID {
		t.Fatalf("claim: %v %v", tk.ID, err)
	}
	if _, err := s.backlog.Block(ctx, geparktID, "q", "sess-1", "May I refund 5000 EUR?"); err != nil {
		t.Fatal(err)
	}

	s.mitglied(t, "aud@test.local", "Aud", "auditor", "auditor-passwort")
	aud := login(t, s, "aud@test.local", "auditor-passwort")
	aud.expect(http.MethodPost, "/api/v1/tasks/"+geparktID.String()+"/reply",
		map[string]any{"text": "yes"}, http.StatusForbidden)

	modell := &zeigerModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	// The auditor, in its own direct conversation with the agent.
	aud.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "About the Globex refund: yes, approved, proceed."}, http.StatusAccepted)
	wartenAuf(t, "the auditor's message becomes a task of its own", func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, tk := range tasks {
			if tk.Origin == "chat:aud@test.local" {
				return true
			}
		}
		return false
	})
	if got, _ := s.backlog.Get(ctx, geparktID); got.State != backlog.StateBlocked || got.ResumeInput != nil {
		t.Fatalf("the parked task must stay parked, unanswered: %s %v", got.State, got.ResumeInput)
	}
	if notes, _ := s.backlog.ListNotes(ctx, geparktID); len(notes) != 0 {
		t.Fatalf("no note from the auditor's message: %+v", notes)
	}
	if p := modell.prompts(); strings.Contains(p, "ADMIN-") {
		t.Fatalf("the auditor's turn was shown the admin's conversation: %s", p)
	}

	// The admin, in the conversation the task came from, answers it.
	admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "About the Globex refund: yes, go ahead."}, http.StatusAccepted)
	wartenAuf(t, "the admin's note wakes the parked task", func() bool {
		got, _ := s.backlog.Get(ctx, geparktID)
		return got.State == backlog.StateOpen
	})
}
