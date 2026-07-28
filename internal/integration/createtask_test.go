package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/guardrails"
)

// TestAgentCreatesSubtask prüft die Meta-Aktion covey/create_task für den
// eigenen Agenten: Der Agent zerlegt zu große Arbeit selbst, statt sich bis zum
// Turn-Limit festzufahren. Die Teilaufgabe hängt als Kind an der Ursprungs-
// aufgabe, trägt ihn als Herkunft — und wird anschließend abgearbeitet.
func TestAgentCreatesSubtask(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("zerleger")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Großes Ding",
		`[mock:action covey/create_task {"title":"Teil zwei","body":"Rest erledigen","priority":2}]
[mock:result Teil eins erledigt, Rest als Aufgabe angelegt]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	child := childOf(t, s, agent.ID, task.ID)
	if child.Title != "Teil zwei" {
		t.Fatalf("Teilaufgabe hat den falschen Titel: %q", child.Title)
	}
	if child.Origin != "agent:zerleger" {
		t.Fatalf("Herkunft muss den erzeugenden Agenten nennen, ist %q", child.Origin)
	}
	if child.Priority != 2 {
		t.Fatalf("Priorität muss übernommen werden, ist %d", child.Priority)
	}
	// Sie ist echte Arbeit, kein Karteikarten-Eintrag: der Agent nimmt sie auf.
	waitFor(t, "teilaufgabe abgearbeitet", 20*time.Second, func() bool {
		return s.taskState(child.ID) == backlog.StateDone
	})
}

// TestAgentDelegatesToColleague prüft die Delegation: Mit "agent":"<slug>"
// landet die Aufgabe beim Kollegen aus derselben Organisation, nicht beim
// Absender — und der Kollege wird dafür geweckt.
func TestAgentDelegatesToColleague(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	sender := s.newSupportAgent("absender")
	colleague := s.newSupportAgent("kollege")

	task, err := s.backlog.Create(ctx, s.orgID, sender.ID, "Nicht mein Fach",
		`[mock:action covey/create_task {"title":"Bitte übernehmen","body":"Details","agent":"kollege"}]
[mock:result delegiert]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	delegated := childOf(t, s, colleague.ID, task.ID)
	if delegated.AgentID != colleague.ID {
		t.Fatalf("delegierte Aufgabe muss beim Kollegen liegen")
	}
	if delegated.OrgID != s.orgID {
		t.Fatalf("Delegation darf die Organisation nicht verlassen")
	}
	waitFor(t, "kollege arbeitet ab", 20*time.Second, func() bool {
		return s.taskState(delegated.ID) == backlog.StateDone
	})
}

// TestCreateTaskLoopProtection prüft die Bremsen: Ein Agent, der Aufgaben
// anlegen kann, kann sich selbst beschäftigen, bis das Budget leer ist. Deshalb
// lehnt die Control Plane eine Dublette gleichen Titels ab — genau das Muster,
// mit dem sich wiederkehrende Läufe sonst eine nie leer werdende Warteschlange
// bauen. Ein unbekannter Ziel-Agent scheitert ebenfalls, statt still zu wirken.
func TestCreateTaskLoopProtection(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("dublettist")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Doppelt hält nicht besser",
		`[mock:action covey/create_task {"title":"Immer dasselbe","body":"a"}]
[mock:action covey/create_task {"title":"Immer dasselbe","body":"b"}]
[mock:action covey/create_task {"title":"Ins Leere","body":"c","agent":"gibtsnicht"}]
[mock:result versucht]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe terminal", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	all, err := s.backlog.ListByAgent(ctx, agent.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, a := range all {
		if a.Title == "Immer dasselbe" {
			n++
		}
		if a.Title == "Ins Leere" {
			t.Fatalf("Aufgabe an unbekannten Agenten darf nicht entstehen")
		}
	}
	if n != 1 {
		t.Fatalf("Dublette gleichen Titels muss abgelehnt werden, angelegt: %d", n)
	}
}

// TestCreateTaskGuardRail prüft, dass covey/create_task — anders als die
// übrigen covey-Meta-Aktionen — durch die Guard-Rails läuft: Delegation
// (covey:create_task:foreign) lässt sich verbieten, ohne dem Agenten die
// Zerlegung der eigenen Arbeit zu nehmen.
func TestCreateTaskGuardRail(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	sender := s.newSupportAgent("gebremster")
	s.newSupportAgent("kollege2")

	if _, err := s.rails.Create(ctx, railRule(s.orgID, guardrails.RuleDenyAction, "covey:create_task:foreign")); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, sender.ID, "Darf nicht delegieren",
		`[mock:action covey/create_task {"title":"Abgeblockt","body":"x","agent":"kollege2"}]
[mock:action covey/create_task {"title":"Erlaubt","body":"y"}]
[mock:result fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe terminal", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	all, err := s.backlog.ListByAgent(ctx, sender.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if strings.HasPrefix(a.Title, "Abgeblockt") {
			t.Fatalf("verbotene Delegation darf keine Aufgabe erzeugen")
		}
	}
	// Die eigene Zerlegung bleibt erlaubt — die Regel trifft nur die Delegation.
	if _, err := s.backlog.Get(ctx, childOf(t, s, sender.ID, task.ID).ID); err != nil {
		t.Fatalf("Teilaufgabe für sich selbst muss weiterhin entstehen: %v", err)
	}
}
