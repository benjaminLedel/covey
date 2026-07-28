package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestTerminalTaskLeavesAgentStage prüft die Spalten-Hygiene: Eine erledigte
// Aufgabe gehört nicht in „Recherche", sondern nach „Erledigt". Ohne das bliebe
// in jeder vom Agenten erfundenen Spalte eine terminale Aufgabe liegen, die
// Spalte damit dauerhaft „nicht leer" — so entstand in der Praxis ein Board mit
// einem Dutzend toter Arbeitszustände.
//
// Menschlich angelegte Spalten sind davon ausgenommen: bewusste Platzierung
// wird nie überschrieben.
func TestTerminalTaskLeavesAgentStage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("spalten-agent")
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Durchlauf",
		`[mock:action covey/set_stage {"stage":"Recherche"}]
[mock:result fertig]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		if st.Name == "Recherche" {
			t.Fatalf("geleerte Agenten-Spalte muss verschwinden, Board: %+v", stages)
		}
	}
	if len(stages) != len(backlog.DefaultStages) {
		t.Fatalf("nur die Standard-Spalten dürfen übrig sein, Board: %+v", stages)
	}

	// Die Aufgabe ist nicht verloren, sondern in „Erledigt" gelandet.
	done, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.StageID == nil {
		t.Fatalf("erledigte Aufgabe darf nicht ohne Spalte zurückbleiben")
	}
	for _, st := range stages {
		if st.ID == *done.StageID && st.Name != "Erledigt" {
			t.Fatalf("erledigte Aufgabe gehört nach Erledigt, liegt in %q", st.Name)
		}
	}
}

// TestAgentStageWithActiveTaskSurvives ist die Gegenprobe zum Aufräumen: Eine
// Agenten-Spalte, in der noch echte Arbeit liegt, darf nicht verschwinden, nur
// weil eine andere Aufgabe darin fertig geworden ist. Sonst zöge das Aufräumen
// dem Agenten die Spalte unter der laufenden Aufgabe weg.
// Ein Agent arbeitet strikt seriell, zwei gleichzeitig laufende Aufgaben gibt es
// also nicht — der Test prüft die Regel deshalb am Store: zwei Aufgaben in
// derselben Agenten-Spalte, eine wird terminal.
func TestAgentStageWithActiveTaskSurvives(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// Kein Dispatch: der pausierte Agent lässt seine Aufgaben liegen, damit die
	// zweite verlässlich offen bleibt, während die erste abgeschlossen wird.
	agent := s.newSupportAgent("mitbewohner-agent")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}

	// Priorität steuert, welche ClaimNext zuerst greift.
	fertig, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Wird fertig", "", "manual", 1)
	if err != nil {
		t.Fatal(err)
	}
	bleibt, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Bleibt offen", "", "manual", 9)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{fertig.ID, bleibt.ID} {
		if _, err := s.backlog.SetTaskStageByName(ctx, agent.ID, id, "Analyse"); err != nil {
			t.Fatal(err)
		}
	}

	// Eine der beiden wird terminal — sie verlässt „Analyse" nach „Erledigt".
	claimed, err := s.backlog.ClaimNext(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != fertig.ID {
		t.Fatalf("erwartet wurde die höher priorisierte Aufgabe, kam %q", claimed.Title)
	}
	if _, err := s.backlog.Complete(ctx, fertig.ID, backlog.StateDone, "fertig", ""); err != nil {
		t.Fatal(err)
	}

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range stages {
		if st.Name == "Analyse" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Spalte mit noch offener Aufgabe darf nicht abgeräumt werden, Board: %+v", stages)
	}
	// Die offene Aufgabe liegt weiterhin dort, die fertige nicht mehr.
	still, err := s.backlog.Get(ctx, bleibt.ID)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.backlog.Get(ctx, fertig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.StageID == nil || done.StageID == nil || *still.StageID == *done.StageID {
		t.Fatalf("die fertige Aufgabe muss die Agenten-Spalte verlassen haben, die offene nicht")
	}
}

// TestHumanStageSurvivesTerminalTask ist die Gegenprobe: Legt ein Mensch eine
// eigene Spalte an und liegt die Aufgabe dort, bleibt sie liegen — Auto-Follow
// fasst bewusst platzierte Aufgaben nicht an, terminal oder nicht.
func TestHumanStageSurvivesTerminalTask(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("mensch-spalten-agent")
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}
	human, err := s.backlog.CreateStage(ctx, agent.ID, "Freigabe Buchhaltung", "")
	if err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Rechnung", "[mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.backlog.SetTaskStage(ctx, task.ID, &human.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, st := range stages {
		if st.ID == human.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("menschliche Spalte darf nicht abgeräumt werden, Board: %+v", stages)
	}
	done, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.StageID == nil || *done.StageID != human.ID {
		t.Fatalf("bewusst platzierte Aufgabe darf nicht verschoben werden")
	}
}
