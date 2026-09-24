package httpapi

import (
	"testing"

	"covey/internal/chat"
)

// The People department works briefs, she does not answer them (#327): a
// triage that decided "answer" for her becomes "task", everything else — a
// note onto a running brief, a task — stays as decided, and for everybody
// else the decision is untouched.
func TestPeopleDepartmentNeverAnswersABrief(t *testing.T) {
	antwort := chat.Entscheidung{Aktion: chat.AktionAntwort, Text: "I cannot see which tickets were handled."}
	if got := entscheidungFuer(peopleSlug, antwort); got.Aktion != chat.AktionAufgabe || got.Text != "" {
		t.Fatalf("an answer to the People department has to become a task, got %+v", got)
	}
	notiz := chat.Entscheidung{Aktion: chat.AktionNotiz, Aufgabe: "a1", Text: "only triage, no answers"}
	if got := entscheidungFuer(peopleSlug, notiz); got != notiz {
		t.Fatalf("a note onto a running brief stays a note, got %+v", got)
	}
	if got := entscheidungFuer("support", antwort); got != antwort {
		t.Fatalf("everybody else keeps the triage's decision, got %+v", got)
	}
}
