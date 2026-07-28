package integration

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

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

// TestCreateTaskDepthLimit prüft die Tiefen-Bremse: Eine Aufgabe darf zerlegt
// werden, ihre Teilaufgabe auch — aber die Kette endet. Ohne diese Grenze
// zerlegt ein Agent seine Arbeit rekursiv weiter, bis das Budget leer ist.
//
// Der Test baut die Kette direkt im Store auf (so tief, wie sie ein Agent über
// mehrere Läufe erzeugt hätte) und lässt erst die unterste Aufgabe laufen: Sie
// darf keine weitere mehr abspalten.
func TestCreateTaskDepthLimit(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("tiefengraber")

	split := `[mock:action covey/create_task {"title":"Noch eine Stufe","body":"weiter"}]
[mock:result versucht]`

	// Stufe 0 zerlegt sich noch — die Kette ist frisch.
	root, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Stufe 0", split, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "wurzel done", 20*time.Second, func() bool {
		return s.taskState(root.ID) == backlog.StateDone
	})
	if _, err := s.backlog.Get(ctx, childOf(t, s, agent.ID, root.ID).ID); err != nil {
		t.Fatalf("die erste Zerlegung muss erlaubt sein: %v", err)
	}

	// Jetzt eine Kette, die das Limit bereits ausgeschöpft hat.
	deep := root.ID
	for i := 0; i < 3; i++ {
		child, err := s.backlog.CreateChild(ctx, deep, backlog.ChildSpec{
			Title: "Kettenglied " + strconv.Itoa(i), Body: split, Origin: "agent:tiefengraber",
		})
		if err != nil {
			t.Fatal(err)
		}
		deep = child.ID
	}
	waitFor(t, "unterste stufe terminal", 30*time.Second, func() bool {
		st := s.taskState(deep)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	n, err := s.backlog.CountChildren(ctx, deep)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("am Ende der Kette darf nicht weiter zerlegt werden, angelegt: %d", n)
	}
	// Und zwar sichtbar: der Agent bekommt die Ablehnung mit Begründung zurück,
	// statt dass die Aufgabe stillschweigend im Nichts verschwindet.
	if msg := taskError(t, s, deep); !strings.Contains(msg, "kette zu tief") {
		t.Fatalf("Ablehnung muss die Tiefe nennen, war %q", msg)
	}
}

// TestCreateTaskBreadthLimit prüft die Breiten-Bremse: Ein einzelner Lauf darf
// nur begrenzt viele Aufgaben abspalten. Wer mehr braucht, hat seine Arbeit
// nicht zerlegt, sondern kopiert.
func TestCreateTaskBreadthLimit(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("breitensaeer")

	var b strings.Builder
	for i := 0; i < 14; i++ {
		b.WriteString(`[mock:action covey/create_task {"title":"Splitter ` + strconv.Itoa(i) + `","body":"x"}]` + "\n")
	}
	b.WriteString("[mock:result gestreut]")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Streuer", b.String(), "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe terminal", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	n, err := s.backlog.CountChildren(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("die ersten Teilaufgaben müssen entstehen")
	}
	if n > 10 {
		t.Fatalf("ein Lauf darf höchstens 10 Aufgaben abspalten, waren %d", n)
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

// taskError liest den Fehlertext einer Aufgabe (leer, wenn keiner gesetzt ist).
func taskError(t *testing.T, s *stack, id uuid.UUID) string {
	t.Helper()
	task, err := s.backlog.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if task.Error == nil {
		return ""
	}
	return *task.Error
}
