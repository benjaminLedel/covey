package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestPlatformPromptFollowsBinary ist die Rollout-Garantie für bestehende
// Installationen: Der Plattform-Anteil des System-Prompts (Abschluss-Protokoll,
// covey/-Meta-Aktionen, Stage-Regeln) kommt aus dem Binary, nicht aus der beim
// Speichern eingefrorenen Config-Version.
//
// Ohne das müsste nach jedem Deploy jede produktive Agent-Config von Hand neu
// gespeichert werden, damit der Agent von einer neuen Aktion überhaupt erfährt —
// und ein Agent, den seit Monaten niemand angefasst hat, liefe auf Dauer mit dem
// Plattform-Vertrag von seinem letzten Config-Edit.
//
// Der Test simuliert genau das: Er überschreibt den gespeicherten
// compiled_prompt mit einem veralteten Stand und prüft, dass der Agent trotzdem
// den aktuellen bekommt — inklusive seiner eigenen SOUL.md.
func TestPlatformPromptFollowsBinary(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("altbestand")

	// Stand einer Installation, die vor dem Deploy zuletzt gespeichert hat.
	tag, err := s.pool.Exec(ctx,
		"UPDATE agent_config_versions SET compiled_prompt=$2 WHERE agent_id=$1",
		agent.ID, "# VERALTETER PROMPT\n\nKennt weder create_task noch Stage-Regeln.")
	if err != nil {
		t.Fatal(err)
	}
	if tag.RowsAffected() == 0 {
		t.Fatal("kein gespeicherter Prompt überschrieben — der Test würde nichts beweisen")
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Prompt zeigen", "[mock:prompt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result == nil {
		t.Fatal("der Lauf muss den System-Prompt liefern")
	}
	prompt := *got.Result

	if strings.Contains(prompt, "VERALTETER PROMPT") {
		t.Fatalf("der eingefrorene compiled_prompt darf den Lauf nicht mehr tragen")
	}
	// Plattform-Anteil: aktueller Stand aus dem Binary.
	for _, want := range []string{"covey/create_task", "covey/set_stage", "COVEY_STATUS"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("Plattform-Protokoll unvollständig — %q fehlt", want)
		}
	}
	// Agenten-Anteil: die eigene Config ist weiterhin drin.
	if !strings.Contains(prompt, "Support-Agent") {
		t.Fatalf("die SOUL.md des Agenten muss im Prompt stehen")
	}
}
