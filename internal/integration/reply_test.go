package integration

import (
	"context"
	"net/http"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestAnAnswerStandsUnderItsQuestion is #620: an answer to a parked task's
// question goes into the conversation the question went to — as the
// person's message on the task, marked as an answer — and the triage, which
// is on, does not take it for a new request. Both ways a question travels:
// the task's own conversation, and the direct conversation of the agent
// with its supervisor for a task from anywhere else.
func TestAnAnswerStandsUnderItsQuestion(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("antwort")
	s.ohneLaeufe(agent.ID)
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET supervisor_id=$2 WHERE id=$1`, agent.ID, s.adminID); err != nil {
		t.Fatal(err)
	}
	admin := teamLogin(t, s)
	base := "/api/v1/agents/" + agent.ID.String()

	// A task from the conversation, without triage, parked with a question.
	created := admin.expect(http.MethodPost, base+"/messages",
		map[string]any{"text": "Bitte die Rechnung von Globex prüfen"}, http.StatusCreated)
	taskID := created["task"].(map[string]any)["id"].(string)
	task, err := s.backlog.ClaimNext(ctx, agent.ID)
	if err != nil || task.ID.String() != taskID {
		t.Fatalf("claim: %v %v", task.ID, err)
	}
	if _, err := s.backlog.Block(ctx, task.ID, "frage", "sitzung-1", "Darf ich Globex direkt antworten?"); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)

	// From here on the triage is on: an ordinary message would wait for it.
	admin.expect(http.MethodPatch, "/api/v1/org/chat-triage", map[string]any{"mode": "on"}, http.StatusOK)
	vorher, err := s.backlog.ListByAgent(ctx, agent.ID, false)
	if err != nil {
		t.Fatal(err)
	}

	antwort := "Ja, aber ohne Preisnachlass."
	reply := admin.expect(http.MethodPost, "/api/v1/tasks/"+taskID+"/reply", map[string]any{"text": antwort}, http.StatusOK)
	if reply["woken"] != true {
		t.Fatalf("the answer has to wake the task: %v", reply)
	}
	msg, _ := reply["message"].(map[string]any)
	if msg == nil || msg["text"] != antwort || msg["author_kind"] != "human" || msg["task_id"] != taskID {
		t.Fatalf("the reply carries the message the answer became: %v", reply["message"])
	}
	if meta, _ := msg["meta"].(map[string]any); meta["answer_to"] != taskID || meta["question"] == nil {
		t.Fatalf("the message is marked as the answer to the question: %v", msg["meta"])
	}
	if msg["triage_state"] != nil {
		t.Fatalf("an answer is not handed to the triage: %v", msg["triage_state"])
	}

	thread := admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)
	var frageAt, antwortAt string
	for _, e := range thread["entries"].([]any) {
		m := e.(map[string]any)
		switch {
		case m["kind"] == "question":
			frageAt, _ = m["at"].(string)
		case m["kind"] == "message" && m["text"] == antwort:
			antwortAt, _ = m["at"].(string)
			if m["task_id"] != taskID {
				t.Fatalf("the answer belongs to its task: %v", m)
			}
		}
	}
	fa, err1 := time.Parse(time.RFC3339Nano, frageAt)
	aa, err2 := time.Parse(time.RFC3339Nano, antwortAt)
	if err1 != nil || err2 != nil || aa.Before(fa) {
		t.Fatalf("the answer has to stand under its question: %v", thread["entries"])
	}
	if thread["pending"] == true {
		t.Fatal("nothing waits for a triage after an answer")
	}

	// No new work came of it: the triage would make a task of a request it
	// cannot decide (no model here), and it got none.
	time.Sleep(200 * time.Millisecond)
	nachher, err := s.backlog.ListByAgent(ctx, agent.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(nachher) != len(vorher) {
		t.Fatalf("the answer became work: %d tasks before, %d after", len(vorher), len(nachher))
	}

	// A task from elsewhere: its question went to the supervisor's direct
	// conversation, and the answer follows it there.
	lauf, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Kündigungen prüfen", "", "heartbeat", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET state='in_progress' WHERE id=$1`, lauf.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Block(ctx, lauf.ID, "", "", "Darf ich den Vertrag von Initech kündigen?"); err != nil {
		t.Fatal(err)
	}
	s.srv.Nacherzaehlen(ctx)
	zweite := "Nein, erst nach Rücksprache."
	reply = admin.expect(http.MethodPost, "/api/v1/tasks/"+lauf.ID.String()+"/reply", map[string]any{"text": zweite}, http.StatusOK)
	if reply["woken"] != true || reply["message"] == nil {
		t.Fatalf("the supervisor's answer wakes the run and is written down: %v", reply)
	}
	gefunden := false
	for _, e := range admin.expect(http.MethodGet, base+"/thread", nil, http.StatusOK)["entries"].([]any) {
		if m := e.(map[string]any); m["kind"] == "message" && m["text"] == zweite {
			gefunden = true
		}
	}
	if !gefunden {
		t.Fatal("the answer to a run's question has to stand in the supervisor's conversation")
	}

	// A note on a task nobody waits on, from no conversation, opens none.
	still, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Ablage", "", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}
	reply = admin.expect(http.MethodPost, "/api/v1/tasks/"+still.ID.String()+"/reply", map[string]any{"text": "nur zur Info"}, http.StatusOK)
	if reply["woken"] != false || reply["message"] != nil {
		t.Fatalf("a note stays a note: %v", reply)
	}
	if got, _ := s.backlog.Get(ctx, still.ID); got.State != backlog.StateOpen {
		t.Fatalf("the quiet task is untouched: %s", got.State)
	}
}
