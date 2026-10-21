package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/backlog"
)

// TestAPlainAnswerAnswersTheOpenQuestion is #638: a person answers a parked
// task's question the way one answers in any chat — in the message box, or
// with a reply to the question — and the waiting task resumes with their
// words. With the triage off the answer used to become a new task, and the
// question stayed open for good.
func TestAPlainAnswerAnswersTheOpenQuestion(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("offene-frage")
	s.ohneLaeufe(agent.ID)
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET supervisor_id=$2 WHERE id=$1`, agent.ID, s.adminID); err != nil {
		t.Fatal(err)
	}
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	created := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Ich weiß noch nicht, wobei mir ein Agent helfen könnte."}, http.StatusCreated)
	taskID := created["task"].(map[string]any)["id"].(string)
	convID := created["message"].(map[string]any)["conversation_id"].(string)
	parken := func(frage string) backlog.Task {
		t.Helper()
		task, err := s.backlog.ClaimNext(ctx, agent.ID)
		if err != nil || task.ID.String() != taskID {
			t.Fatalf("claim: %v %v", task.ID, err)
		}
		if _, err := s.backlog.Block(ctx, task.ID, "wahl", "sitzung-1", frage); err != nil {
			t.Fatal(err)
		}
		s.srv.Nacherzaehlen(ctx)
		return task
	}
	task := parken("Was machst du jeden Monat?")

	// In the message box: the waiting task resumes, nothing new is opened.
	antwort := "Jeden Monat prüfe ich die Rechnungen."
	r := admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": antwort}, http.StatusCreated)
	if got, _ := r["task"].(map[string]any); got == nil || got["id"] != taskID {
		t.Fatalf("the answer has to resume the waiting task, not open another: %v", r["task"])
	}
	markiert := false
	for _, m := range nachrichten(t, admin, convID) {
		if meta, _ := m["meta"].(map[string]any); m["text"] == antwort && meta["answer_to"] == taskID && meta["question"] != nil {
			markiert = m["triage_state"] == nil
		}
	}
	if !markiert {
		t.Fatalf("the message is marked as the answer to the question, not handed to the triage: %v", nachrichten(t, admin, convID))
	}
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil || got.State != backlog.StateOpen || got.ResumeInput == nil || *got.ResumeInput != antwort {
		t.Fatalf("the task resumes with the person's words: %+v %v", got, err)
	}
	if alle, _ := s.backlog.ListByAgent(ctx, agent.ID, false); len(alle) != 1 {
		t.Fatalf("the answer became work: %d tasks", len(alle))
	}

	// With a reply to the question in the conversation: the same.
	parken("Wie viele Rechnungen sind es?")
	var frageID string
	for _, m := range nachrichten(t, admin, convID) {
		if m["kind"] == "question" && m["text"] == "Wie viele Rechnungen sind es?" {
			frageID, _ = m["id"].(string)
		}
	}
	if frageID == "" {
		t.Fatal("the second question is not in the conversation")
	}
	r = admin.expect(http.MethodPost, "/api/v1/conversations/"+convID+"/messages",
		map[string]any{"text": "Etwa vierzig.", "reply_to": frageID}, http.StatusCreated)
	if got, _ := s.backlog.Get(ctx, task.ID); got.State != backlog.StateOpen || got.ResumeInput == nil || *got.ResumeInput != "Etwa vierzig." {
		t.Fatalf("a reply to the question answers it: %+v", got)
	}
	if alle, _ := s.backlog.ListByAgent(ctx, agent.ID, false); len(alle) != 1 {
		t.Fatalf("the reply became work: %d tasks", len(alle))
	}

	// Answered, the next message is a request again.
	r = admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Und noch etwas anderes."}, http.StatusCreated)
	if got, _ := r["task"].(map[string]any); got == nil || got["id"] == taskID {
		t.Fatalf("a message without an open question is a new task: %v", r["task"])
	}

	// A question a run from elsewhere parked in the supervisor's
	// conversation — waiting on a customer — does not swallow the next
	// request there.
	lauf, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Kündigungen prüfen", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, lauf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Block(ctx, lauf.ID, "kunde", "", "Ich warte auf die Antwort von Initech."); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)
	r = admin.expect(http.MethodPost, base+"/messages", map[string]any{"text": "Bitte die Globex-Rechnung prüfen."}, http.StatusCreated)
	if got, _ := r["task"].(map[string]any); got == nil || got["id"] == lauf.ID.String() {
		t.Fatalf("a new request is not the answer to another run's wait: %v", r["task"])
	}
	if got, _ := s.backlog.Get(ctx, lauf.ID); got.State != backlog.StateBlocked {
		t.Fatalf("the run keeps waiting on its customer: %s", got.State)
	}
}
