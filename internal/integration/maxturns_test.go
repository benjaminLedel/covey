package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestMaxTurnsContinuation prüft den Turn-Limit-Pfad: Ein Lauf, der ohne
// Ergebnis am Turn-Limit endet, scheitert nicht mehr stumm. Sein Übergabe-Stand
// hängt als Notiz an der Aufgabe, und aus ihm entsteht eine Folgeaufgabe, die
// die Runtime-Session wieder aufnimmt und die Arbeit zu Ende bringt.
//
// Vorher lief genau hier die Endlosschleife: max_turns kam als failed ohne
// Fehlertext an, der Zwischenstand war verloren, und der nächste Heartbeat fing
// dieselbe Arbeit bei null wieder an.
func TestMaxTurnsContinuation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("limit-agent")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Großer Auftrag",
		"[mock:maxturns ## Erledigt\nRepo geklont\n## Offen\nFix schreiben\n## Nächster Schritt\nsrc/pay.go anfassen]",
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "aufgabe terminal", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateFailed || st == backlog.StateDone
	})

	// Der abgebrochene Lauf scheitert — aber mit sprechendem Fehler statt leer.
	parent, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parent.State != backlog.StateFailed {
		t.Fatalf("abgebrochener Lauf muss failed sein, ist %s", parent.State)
	}
	if parent.Error == nil || !strings.Contains(*parent.Error, "Turn-Limit") {
		t.Fatalf("Fehlertext muss das Turn-Limit nennen, ist %v", parent.Error)
	}
	if parent.Result == nil || !strings.Contains(*parent.Result, "Nächster Schritt") {
		t.Fatalf("Übergabe-Stand muss als Ergebnis gespeichert sein, ist %v", parent.Result)
	}

	// Der Zwischenstand ist als Notiz an der Aufgabe sichtbar.
	notes, err := s.backlog.ListNotes(ctx, task.ID)
	if err != nil || len(notes) == 0 {
		t.Fatalf("Zwischenstand muss als Notiz hängen: %+v (err=%v)", notes, err)
	}
	if !strings.Contains(notes[0].Content, "src/pay.go") {
		t.Fatalf("Notiz muss den Übergabe-Stand enthalten: %q", notes[0].Content)
	}

	// Die Folgeaufgabe hängt an der Ursprungsaufgabe, nimmt deren Session wieder
	// auf und trägt denselben Titel (sonst feuert die Heartbeat-Dedup daneben).
	child := childOf(t, s, agent.ID, task.ID)
	if child.Title != parent.Title {
		t.Fatalf("Folgeaufgabe muss den Titel der Ursprungsaufgabe tragen: %q", child.Title)
	}
	if child.RuntimeSessionID == nil || *child.RuntimeSessionID == "" {
		t.Fatalf("Folgeaufgabe muss die Runtime-Session zum Wiederaufsetzen tragen")
	}
	if !strings.HasPrefix(child.Origin, "continuation:") {
		t.Fatalf("Folgeaufgabe braucht die Herkunft continuation:…, ist %q", child.Origin)
	}

	// Und sie läuft durch: die fortgesetzte Session kommt zum Ergebnis.
	waitFor(t, "folgeaufgabe done", 20*time.Second, func() bool {
		return s.taskState(child.ID) == backlog.StateDone
	})
}

// TestMaxTurnsEscalatesAfterCap prüft den Loop-Schutz: Läuft jede Fortsetzung
// erneut ins Turn-Limit, wird die Kette nicht endlos verlängert, sondern
// eskaliert. Ohne diese Grenze ersetzt die Fortsetzung nur eine Endlosschleife
// durch eine andere.
func TestMaxTurnsEscalatesAfterCap(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("limit-loop-agent")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Nie fertig",
		"[mock:maxturns-always ## Offen\nalles]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// Der Kette folgen, bis eine Aufgabe keine Folgeaufgabe mehr erzeugt.
	last := task.ID
	depth := 0
	waitFor(t, "kette bricht ab", 60*time.Second, func() bool {
		if s.taskState(last) != backlog.StateFailed {
			return false
		}
		kids, err := s.backlog.ListByAgent(ctx, agent.ID, true)
		if err != nil {
			return false
		}
		for _, k := range kids {
			if k.ParentTaskID != nil && *k.ParentTaskID == last {
				last, depth = k.ID, depth+1
				return false
			}
		}
		return true
	})

	if depth != 3 {
		t.Fatalf("Kette muss nach 3 Fortsetzungen abbrechen, war %d", depth)
	}
	final, err := s.backlog.Get(ctx, last)
	if err != nil {
		t.Fatal(err)
	}
	if final.Result == nil || !strings.HasPrefix(*final.Result, "ESKALIERT") {
		t.Fatalf("letzte Aufgabe der Kette muss eskalieren, ist %v", final.Result)
	}
}

// childOf findet die (einzige) Folgeaufgabe einer Aufgabe.
func childOf(t *testing.T, s *stack, agentID, parentID uuid.UUID) backlog.Task {
	t.Helper()
	tasks, err := s.backlog.ListByAgent(context.Background(), agentID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ParentTaskID != nil && *task.ParentTaskID == parentID {
			return task
		}
	}
	t.Fatalf("keine Folgeaufgabe zu %s gefunden", parentID)
	return backlog.Task{}
}
