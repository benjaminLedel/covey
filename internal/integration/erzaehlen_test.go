package integration

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/llm"
)

// A model for the control plane's cheap turns: it answers the triage with a
// task and an acknowledgement, and the narration with a sentence — and keeps
// what it was shown, so the test can see the agent's voice went in.
type redendesModell struct {
	mu      sync.Mutex
	gesehen []string
	stumm   bool
}

func (m *redendesModell) Name() string { return "test" }

func (m *redendesModell) Complete(_ context.Context, req llm.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gesehen = append(m.gesehen, req.Messages[0].Content)
	if strings.Contains(req.System, "triage step") {
		return `{"action":"task","title":"Globex-Rechnung prüfen","body":"Bitte die Rechnung von Globex prüfen","text":"Mach ich, ich schau mir die Globex-Rechnung an."}`, nil
	}
	if m.stumm {
		return "", errors.New("model unavailable")
	}
	return "Hab nachgesehen: Die Rechnung war doppelt gebucht, die zweite ist storniert.", nil
}

func (m *redendesModell) prompts() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.gesehen, "\n---\n")
}

// TestAChatTaskIsAcknowledgedAndToldInTheChat walks #411: a message that is
// work gets a sentence at once and the task behind it; when the task is done,
// the agent tells what came out in a few words of chat, and the report stays
// one tap away — in the thread, in the list of conversations.
func TestAChatTaskIsAcknowledgedAndToldInTheChat(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("erzaehler")
	s.ohneLaeufe(agent.ID)
	// A colleague the agent should know of.
	kollegin := s.newSupportAgent("qa-kollegin")
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET display_name='Quirina Prüfgeist', job_title='QA' WHERE id=$1`, kollegin.ID); err != nil {
		t.Fatal(err)
	}
	modell := &redendesModell{}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	// Who writes (#412): the organisation keeps a job title and what the
	// person is responsible for, and the agent is to talk to that.
	if _, err := s.pool.Exec(ctx, `UPDATE humans SET job_title='Vertriebsleitung', responsibilities='Kundenverträge'
		WHERE email='admin@test.local'`); err != nil {
		t.Fatal(err)
	}

	admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Bitte die Rechnung von Globex prüfen"}, http.StatusAccepted)

	var task backlog.Task
	wartenAuf(t, "the message becomes a task", func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		if len(tasks) == 1 {
			task = tasks[0]
			return true
		}
		return false
	})
	// The org chart went in (#415): the triage knows the colleagues, the
	// same directory a run gets at dispatch.
	if !strings.Contains(modell.prompts(), "The organisation you work in") || !strings.Contains(modell.prompts(), "Quirina Prüfgeist") {
		t.Errorf("the triage did not get the org chart with the colleague:\n%s", modell.prompts())
	}
	// The voice went in: the triage saw the agent's SOUL.md, not only its title.
	if !strings.Contains(modell.prompts(), "Who the agent is (its SOUL.md)") {
		t.Fatalf("the triage did not get the agent's SOUL.md:\n%s", modell.prompts())
	}

	// A sentence at once, before any work is done.
	wartenAuf(t, "the acknowledgement stands in the thread", func() bool {
		verlauf := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
		for _, roh := range verlauf["entries"].([]any) {
			e, _ := roh.(map[string]any)
			if e["kind"] == "answer" && e["text"] == "Mach ich, ich schau mir die Globex-Rechnung an." {
				return true
			}
		}
		return false
	})

	// The run ends with a report.
	bericht := "## Ergebnis\n- Rechnung 4711 doppelt gebucht\n- Storno 4711-S erstellt"
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, task.ID, backlog.StateDone, bericht, ""); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)

	// Told, not delivered: the sentence is what the thread shows, the report
	// is still there under it.
	verlauf := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	var ergebnis map[string]any
	for _, roh := range verlauf["entries"].([]any) {
		e, _ := roh.(map[string]any)
		if e["kind"] == "result" {
			ergebnis = e
		}
	}
	if ergebnis == nil {
		t.Fatal("no result in the thread")
	}
	if ergebnis["said"] != "Hab nachgesehen: Die Rechnung war doppelt gebucht, die zweite ist storniert." {
		t.Errorf("said = %v", ergebnis["said"])
	}
	if ergebnis["text"] != bericht {
		t.Errorf("the report is gone from the result: %v", ergebnis["text"])
	}
	// The narration was told from the report and the request, nothing else.
	if p := modell.prompts(); !strings.Contains(p, "Rechnung 4711 doppelt gebucht") || !strings.Contains(p, "What you were asked to do") {
		t.Errorf("the narration did not see the report and the request:\n%s", p)
	}

	// The list of conversations says the sentence too, not the report.
	threads := admin.expect(http.MethodGet, "/api/v1/me/threads", nil, http.StatusOK)
	for _, roh := range threads["threads"].([]any) {
		th, _ := roh.(map[string]any)
		if th["agent_id"] == agent.ID.String() && !strings.HasPrefix(th["last_text"].(string), "Hab nachgesehen") {
			t.Errorf("last_text = %v", th["last_text"])
		}
	}

	// Both turns knew whom they were talking to (#412): the triage and the
	// narration each saw the person's job title.
	if n := strings.Count(modell.prompts(), "Who you are talking to:\nAdmin — job title: Vertriebsleitung"); n != 2 {
		t.Errorf("the person was described %d times, want 2 (triage and narration):\n%s", n, modell.prompts())
	}

	// A follow-up has its context (#413): the next message's triage sees
	// what the task said back, and the task made from it carries the
	// conversation for the run.
	vorFolge := len(modell.prompts())
	admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Welche Rechnung war das nochmal?"}, http.StatusAccepted)
	var folge backlog.Task
	wartenAuf(t, "the follow-up becomes a task", func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, false)
		for _, x := range tasks {
			if x.ID != task.ID {
				folge = x
				return true
			}
		}
		return false
	})
	if neu := modell.prompts()[vorFolge:]; !strings.Contains(neu, "Hab nachgesehen: Die Rechnung war doppelt gebucht") {
		t.Errorf("the follow-up's triage did not see the earlier result:\n%s", neu)
	}
	if !strings.Contains(folge.Body, "Earlier in this conversation") || !strings.Contains(folge.Body, "doppelt gebucht") {
		t.Errorf("the follow-up task does not carry the conversation:\n%s", folge.Body)
	}
	if strings.Contains(folge.Body, "- person: Welche Rechnung war das nochmal?") {
		t.Errorf("the message itself is the task, not its own context:\n%s", folge.Body)
	}

	// Told once: a second pass does not tell it again.
	vorher := strings.Count(modell.prompts(), "What you were asked to do")
	s.srv.Nacherzaehlen(ctx)
	if strings.Count(modell.prompts(), "What you were asked to do") != vorher {
		t.Error("a told task was told again")
	}
}

// Without a model to tell it with, the report stands on its own, as before
// #411 — and the task is not asked about again and again.
func TestAResultNobodyCanTellStandsOnItsOwn(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("stumm")
	modell := &redendesModell{stumm: true}
	s.srv.OrgLLM = func(context.Context, uuid.UUID) (llm.Provider, error) { return modell, nil }
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas prüfen", "", "chat:admin@test.local", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, task.ID, backlog.StateDone, "Geprüft, alles in Ordnung.", ""); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)
	var said *string
	var gesagt bool
	if err := s.pool.QueryRow(ctx, `SELECT said, said_at IS NOT NULL FROM backlog_tasks WHERE id=$1`, task.ID).Scan(&said, &gesagt); err != nil {
		t.Fatal(err)
	}
	if !gesagt || said == nil || *said != "" {
		t.Fatalf("said = %v, said_at set = %v: want an empty sentence, marked", said, gesagt)
	}
	verlauf := admin.expect(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/thread", nil, http.StatusOK)
	for _, roh := range verlauf["entries"].([]any) {
		e, _ := roh.(map[string]any)
		if e["kind"] == "result" && (e["said"] != nil || e["text"] != "Geprüft, alles in Ordnung.") {
			t.Errorf("result entry = %v", e)
		}
	}
}
