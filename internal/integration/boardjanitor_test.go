package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestBoardJanitorArchivesOldTerminalTasks prüft das Selbstaufräumen des Boards:
// Eine terminale Aufgabe, die älter als die Frist ist, archiviert die Control
// Plane von selbst — und die Agenten-Spalte, die dadurch leer wird, verschwindet
// mit. Ohne diesen Pfad hält jede erledigte Karte ihre Spalte am Leben; das
// Spalten-Cleanup allein greift nie, weil die Leiche liegen bleibt. Genau so
// entstand in der Praxis ein Board mit einem Dutzend toter Spalten, das nur ein
// Mensch am „Aufräumen"-Knopf wieder loswurde.
func TestBoardJanitorArchivesOldTerminalTasks(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// Pausiert: die Aufgaben sollen liegen bleiben, nicht dispatcht werden.
	agent := s.newSupportAgent("janitor-agent")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}

	// Zwei terminale Aufgaben, jede in ihrer eigenen Agenten-Spalte — der Stand,
	// den ein Board nach Tagen Arbeit hat.
	alt := s.terminalTaskInStage(t, agent.ID, "Alte Analyse", "Analyse #83")
	frisch := s.terminalTaskInStage(t, agent.ID, "Frische Analyse", "Analyse #99")

	// Nur die alte Aufgabe ist über die Frist hinaus.
	if _, err := s.pool.Exec(ctx,
		"UPDATE backlog_tasks SET updated_at = now() - interval '48 hours' WHERE id=$1", alt.ID); err != nil {
		t.Fatal(err)
	}

	n, err := s.backlog.ArchiveTerminalOlderThan(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("genau die alte Aufgabe sollte archiviert werden, waren %d", n)
	}

	altNach, err := s.backlog.Get(ctx, alt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if altNach.ArchivedAt == nil {
		t.Fatalf("die alte Aufgabe muss archiviert sein")
	}
	if altNach.State != backlog.StateDone {
		t.Fatalf("archivieren ist kein Löschen — Zustand bleibt done, ist %q", altNach.State)
	}
	frischNach, err := s.backlog.Get(ctx, frisch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frischNach.ArchivedAt != nil {
		t.Fatalf("frisch Erledigtes muss auf dem Board sichtbar bleiben — sonst verschwindet die Arbeit des letzten Laufs vor den Augen des Prüfers")
	}

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		if st.Name == "Analyse #83" {
			t.Fatalf("die geleerte Agenten-Spalte muss mit verschwinden, Board: %+v", stages)
		}
	}
	found := false
	for _, st := range stages {
		if st.Name == "Analyse #99" {
			found = true
		}
	}
	if !found {
		t.Fatalf("die Spalte der frischen Aufgabe darf nicht abgeräumt werden, Board: %+v", stages)
	}
}

// terminalTaskInStage baut den Zustand nach, den ein Lauf hinterlässt: eine
// erledigte Aufgabe, die in einer vom Agenten angelegten Spalte liegt.
func (s *stack) terminalTaskInStage(t *testing.T, agentID uuid.UUID, title, stage string) backlog.Task {
	t.Helper()
	ctx := context.Background()
	task, err := s.backlog.Create(ctx, s.orgID, agentID, title, "", "manual", 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.ClaimNext(ctx, agentID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.Complete(ctx, task.ID, backlog.StateDone, "fertig", ""); err != nil {
		t.Fatal(err)
	}
	// Nach dem Abschluss in die Agenten-Spalte legen: so sehen die Boards aus,
	// die vor dem Auto-Follow-Fix entstanden sind.
	if _, err := s.backlog.SetTaskStageByName(ctx, agentID, task.ID, stage); err != nil {
		t.Fatal(err)
	}
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
