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

// TestConversationSectionChatAnswer: a message nobody triaged is told that
// its result is the reply itself (#483), and not the retelling instruction —
// nothing retells it.
func TestConversationSectionChatAnswer(t *testing.T) {
	conv := uuid.New()
	s := conversationSection(backlog.Task{ConversationID: &conv, ChatAnswer: true})
	if !strings.Contains(s, "This task is a chat message") || !strings.Contains(s, "word for word") {
		t.Fatalf("a chat answer got: %q", s)
	}
	if strings.Contains(s, "covey retells it") {
		t.Fatalf("a chat answer must not be told it is retold: %q", s)
	}
}

// TestConversationSectionImAnruf: a chat answer said in a call is told to
// end with its spoken form (#502); a typed one is not, and a triaged task
// from a call is retold as before — its spoken form comes when it is posted.
func TestConversationSectionImAnruf(t *testing.T) {
	conv := uuid.New()
	s := conversationSection(backlog.Task{ConversationID: &conv, ChatAnswer: true, SaidInCall: true})
	if !strings.Contains(s, "This task is a chat message") || !strings.Contains(s, "<spoken details_in_chat=") {
		t.Fatalf("a chat answer from a call got: %q", s)
	}
	if strings.Contains(conversationSection(backlog.Task{ConversationID: &conv, ChatAnswer: true}), "<spoken") {
		t.Fatal("a typed chat answer must not be asked for a spoken form")
	}
	if strings.Contains(conversationSection(backlog.Task{ConversationID: &conv, SaidInCall: true}), "<spoken") {
		t.Fatal("a triaged task is retold; its run is not asked for a spoken form")
	}
}
