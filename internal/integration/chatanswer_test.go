package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestAnUntriagedMessageIsAnsweredNotReported is the fallback half of #483:
// without the triage a message still becomes a task, but a task that answers
// it — the run is told its result is the reply, posted word for word in the
// chat's tone, and what arrives in the conversation is the agent's message,
// not a result entry with the run's report behind it.
func TestAnUntriagedMessageIsAnsweredNotReported(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("ohne-triage")
	admin := teamLogin(t, s)
	admin.expect(http.MethodPatch, "/api/v1/org/chat-tone",
		map[string]any{"address": "du", "tone": "casual", "emoji": "never"}, http.StatusOK)

	created := admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/messages",
		map[string]any{"text": "Hey, was geht?\n[mock:prompt]"}, http.StatusCreated)
	aus := created["task"].(map[string]any)
	if aus["chat_answer"] != true {
		t.Fatalf("an untriaged message has to become a chat answer: %v", aus)
	}
	taskID, _ := uuid.Parse(aus["id"].(string))
	waitFor(t, "the answer run finishes", 40*time.Second, func() bool {
		return s.taskState(taskID) == backlog.StateDone
	})
	got, err := s.backlog.Get(ctx, taskID)
	if err != nil || got.Result == nil {
		t.Fatalf("result: %v %v", got.Result, err)
	}
	prompt := *got.Result
	if !strings.Contains(prompt, "This task is a chat message") {
		t.Fatal("the run was not told it answers a chat message")
	}
	if strings.Contains(prompt, "This task comes from a conversation") {
		t.Fatal("the run was told its result is retold, which nothing does on this path")
	}
	if !strings.Contains(prompt, "How you talk in the team chat") || !strings.Contains(prompt, "no emoji at all") {
		t.Fatal("the run was not given the chat tone its reply goes out in")
	}

	s.srv.Nacherzaehlen(ctx)
	s.srv.Nacherzaehlen(ctx) // twice: written once
	var kinds []string
	rows, err := s.pool.Query(ctx, `SELECT kind, coalesce(meta->>'reports', '') FROM conversation_messages
		WHERE task_id = $1 AND author_kind = 'agent'`, taskID)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k, r string
		if err := rows.Scan(&k, &r); err != nil {
			t.Fatal(err)
		}
		kinds = append(kinds, k+"/"+r)
	}
	rows.Close()
	if len(kinds) != 1 || kinds[0] != "text/result" {
		t.Fatalf("the conversation has to get exactly one plain reply, got %v", kinds)
	}
	if z := eintraege(t, admin, agent.ID); !enthaelt(z, "answer: ") || enthaelt(z, "result: ") {
		t.Fatalf("the thread: %v", z)
	}

	// A task the triage opened keeps its retelling instruction: it is a task.
	conv := direkt(t, s, s.adminID, agent.ID)
	echt, err := s.backlog.CreateIn(ctx, s.orgID, agent.ID, "Prompt zeigen", "[mock:prompt]", "chat:admin@test.local", 3, &conv)
	if err != nil {
		t.Fatal(err)
	}
	if echt.ChatAnswer {
		t.Fatal("a task opened by the triage is not a chat answer")
	}
}
