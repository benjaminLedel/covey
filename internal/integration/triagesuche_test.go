package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"covey/internal/llm"
)

// A model that looks things up: first it asks to search, then it answers
// with what it was shown. It keeps its prompts.
type suchendesModell struct {
	mu      sync.Mutex
	gesehen []string
}

func (m *suchendesModell) Name() string { return "test" }

func (m *suchendesModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := req.Messages[0].Content
	m.gesehen = append(m.gesehen, p)
	if !strings.Contains(p, "You searched the whole conversation") {
		return `{"action":"search","query":"Volker Vorrauschauend"}`, nil
	}
	return `{"action":"answer","text":"Ja, Volker Vorausschauend ist unser Delivery-Lead — er ist gerade aber gestoppt."}`, nil
}

// TestTheTriageLooksUpWhatItDoesNotSee is #416: the colleague is stopped and
// the name is mistyped; the triage searches, gets the org chart's line with
// the stopped colleague in it, and answers.
func TestTheTriageLooksUpWhatItDoesNotSee(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("sucherin")
	s.ohneLaeufe(agent.ID)
	volker := s.newSupportAgent("delivery-lead")
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET display_name='Volker Vorausschauend', job_title='Delivery Lead' WHERE id=$1`, volker.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.registry.SetKilled(ctx, volker.ID, true); err != nil {
		t.Fatal(err)
	}
	modell := &suchendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	base := "/api/v1/agents/" + agent.ID.String()

	admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Kennst du Volker Vorrauschauend?"}, http.StatusAccepted)
	wartenAuf(t, "the answer stands in the thread", func() bool {
		verlauf := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
		for _, roh := range verlauf["entries"].([]any) {
			e, _ := roh.(map[string]any)
			if e["kind"] == "answer" && strings.Contains(e["text"].(string), "Delivery-Lead") {
				return true
			}
		}
		return false
	})
	modell.mu.Lock()
	defer modell.mu.Unlock()
	if len(modell.gesehen) != 2 {
		t.Fatalf("%d turns, want 2: search, then decide", len(modell.gesehen))
	}
	// The stopped colleague is in the chat's org chart, marked.
	if !strings.Contains(modell.gesehen[0], "Volker Vorausschauend — Delivery Lead") || !strings.Contains(modell.gesehen[0], "STOPPED") {
		t.Errorf("the chat's org chart lacks the stopped colleague:\n%s", modell.gesehen[0])
	}
	// And the search found him despite the typo.
	if !strings.Contains(modell.gesehen[1], "org chart: Volker Vorausschauend") {
		t.Errorf("the search did not find the colleague:\n%s", modell.gesehen[1])
	}
	tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
	if len(tasks) != 0 {
		t.Errorf("a question about a colleague became %d task(s)", len(tasks))
	}
}

// A model that never answers.
type stummesModell struct {
	mu   sync.Mutex
	rufe int
}

func (m *stummesModell) Name() string { return "test" }
func (m *stummesModell) Complete(context.Context, llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rufe++
	return "", errors.New("overloaded")
}

// TestAFailedTriageSaysWhy is #416 too: a failed turn is tried once more, and
// when it still fails the task the message became carries the reason.
func TestAFailedTriageSaysWhy(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("stumm")
	s.ohneLaeufe(agent.ID)
	modell := &stummesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages", map[string]any{"text": "Wie geht's?"}, http.StatusAccepted)
	var task struct{ id uuid.UUID }
	wartenAuf(t, "the message becomes a task", func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		if len(tasks) == 1 {
			task.id = tasks[0].ID
			return true
		}
		return false
	})
	modell.mu.Lock()
	rufe := modell.rufe
	modell.mu.Unlock()
	if rufe != 2 {
		t.Errorf("%d calls, want 2: the turn is tried once more", rufe)
	}
	var note string
	if err := s.pool.QueryRow(ctx, `SELECT content FROM task_notes WHERE task_id=$1 AND author='triage:covey'`, task.id).Scan(&note); err != nil {
		t.Fatalf("no reason at the task: %v", err)
	}
	if !strings.Contains(note, "overloaded") {
		t.Errorf("note = %q", note)
	}
}
