package orchestrator

import (
	"context"
	"strings"

	"github.com/google/uuid"
)

/*
What a conversation hears of a run (#650).

A run writes its reply to the person as its final message and closes it with
the status line, whose result and question are meant as the record's short
form. Only the status line used to travel: the chat showed "overview
delivered" under a reply that held the overview, and before #637 a question
that pointed at proposals nobody saw. For a task from a conversation the reply
is the message; the status line's fields stay what is used where no reply came
— from a daemon that predates it, or a run that wrote nothing above the line.
*/

// fromConversation reports whether a task was opened from a conversation —
// the tasks whose result and question people read in the chat.
func (o *Orchestrator) fromConversation(ctx context.Context, taskID uuid.UUID) bool {
	t, err := o.Backlog.Get(ctx, taskID)
	return err == nil && t.ConversationID != nil
}

// replyWithQuestion is what a parked conversation task posts: its reply,
// followed by the question unless the reply already asks it — an agent
// usually ends its reply with the very question it then repeats in the
// status line, and asking twice reads as a glitch.
func replyWithQuestion(reply, question string) string {
	reply, question = strings.TrimSpace(reply), strings.TrimSpace(question)
	switch {
	case reply == "":
		return question
	case question == "" || strings.Contains(simplified(reply), simplified(anfang(question))):
		return reply
	}
	return reply + "\n\n" + question
}

// anfang is the opening of a question, enough to recognise it inside a reply
// that words its end a little differently — without the closing mark.
func anfang(question string) string {
	r := []rune(strings.TrimRight(question, "?!.:… "))
	if len(r) > 40 {
		r = r[:40]
	}
	return string(r)
}

// simplified folds case and whitespace, so that a line break or a capital
// letter does not hide the same sentence.
func simplified(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}
