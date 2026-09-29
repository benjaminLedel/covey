package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestAConversationTaskIsToldItsResultIsReadOut walks the run half of #457:
// a task opened from a conversation learns that its result is read out to
// the people there and has to carry the content; a task from anywhere else
// keeps the summary for the record and hears nothing about a conversation.
func TestAConversationTaskIsToldItsResultIsReadOut(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("gespraechslauf")
	conv := direkt(t, s, s.adminID, agent.ID)

	aus, err := s.backlog.CreateIn(ctx, s.orgID, agent.ID, "Prompt zeigen", "[mock:prompt]", "chat:admin@test.local", 3, &conv)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the conversation task finishes", 40*time.Second, func() bool {
		return s.taskState(aus.ID) == backlog.StateDone
	})
	got, err := s.backlog.Get(ctx, aus.ID)
	if err != nil || got.Result == nil {
		t.Fatalf("result: %v %v", got.Result, err)
	}
	if !strings.Contains(*got.Result, "This task comes from a conversation") {
		t.Fatal("a task from a conversation must be told that its result is read out there")
	}

	_, anders := laufLassen(t, s, agent, "Prompt zeigen", "[mock:prompt]")
	if strings.Contains(anders, "This task comes from a conversation") {
		t.Fatal("a task from the backlog must not be told about a conversation")
	}
	if !strings.Contains(anders, "COVEY_STATUS") {
		t.Fatal("the prompt of the second run was not shown")
	}
}
