package orchestrator

import (
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestConversationSectionNurImGespraech: the instruction that the result is
// read out stands in the prompt of a task from a conversation and of no
// other (#457).
func TestConversationSectionNurImGespraech(t *testing.T) {
	if s := conversationSection(backlog.Task{}); s != "" {
		t.Fatalf("a task without a conversation got: %q", s)
	}
	conv := uuid.New()
	s := conversationSection(backlog.Task{ConversationID: &conv})
	if !strings.Contains(s, "This task comes from a conversation") || !strings.Contains(s, "not a summary for the record") {
		t.Fatalf("a task from a conversation got: %q", s)
	}
}
