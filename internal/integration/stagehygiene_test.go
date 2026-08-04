package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestTerminalTaskLeavesAgentStage checks the column hygiene: a completed task
// does not belong in "Recherche", it belongs in "Erledigt". Without it, a
// terminal task would stay behind in every column the agent invented, keeping
// that column permanently "not empty" — that is how a board with a dozen dead
// working states came about in practice.
//
// Columns created by humans are exempt: a deliberate placement is never
// overwritten.
func TestTerminalTaskLeavesAgentStage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("spalten-agent")
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Full pass",
		`[mock:action covey/set_stage {"stage":"Recherche"}]
[mock:result done]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		if st.Name == "Recherche" {
			t.Fatalf("an emptied agent column has to disappear, board: %+v", stages)
		}
	}
	if len(stages) != len(backlog.DefaultStages) {
		t.Fatalf("only the default columns may be left, board: %+v", stages)
	}

	// The task is not lost, it landed in "Erledigt".
	done, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.StageID == nil {
		t.Fatalf("a completed task must not be left without a column")
	}
	for _, st := range stages {
		if st.ID == *done.StageID && st.Name != "Erledigt" {
			t.Fatalf("a completed task belongs in Erledigt, it lies in %q", st.Name)
		}
	}
}

// TestAgentStageWithActiveTaskSurvives is the counter-check to the cleanup: an
// agent column that still holds real work must not disappear just because
// another task in it has finished. Otherwise the cleanup would pull the column
// out from under the agent's running task.
// An agent works strictly serially, so two tasks running at the same time do not
// exist — the test therefore checks the rule at the store: two tasks in the same
// agent column, one of them turns terminal.
func TestAgentStageWithActiveTaskSurvives(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// No dispatch: the paused agent leaves its tasks alone so the second one
	// reliably stays open while the first is being completed.
	agent := s.newSupportAgent("mitbewohner-agent")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}

	// The priority controls which one ClaimNext picks up first.
	fertig, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Will finish", "", "manual", 1)
	if err != nil {
		t.Fatal(err)
	}
	bleibt, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Stays open", "", "manual", 9)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{fertig.ID, bleibt.ID} {
		if _, err := s.backlog.SetTaskStageByName(ctx, agent.ID, id, "Analyse"); err != nil {
			t.Fatal(err)
		}
	}

	// One of the two turns terminal — it leaves "Analyse" for "Erledigt".
	claimed, err := s.backlog.ClaimNext(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ID != fertig.ID {
		t.Fatalf("the higher-prioritized task was expected, got %q", claimed.Title)
	}
	if _, err := s.backlog.Complete(ctx, fertig.ID, backlog.StateDone, "done", ""); err != nil {
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
		t.Fatalf("a column with a still-open task must not be cleaned up, board: %+v", stages)
	}
	// The open task still lies there, the finished one does not.
	still, err := s.backlog.Get(ctx, bleibt.ID)
	if err != nil {
		t.Fatal(err)
	}
	done, err := s.backlog.Get(ctx, fertig.ID)
	if err != nil {
		t.Fatal(err)
	}
	if still.StageID == nil || done.StageID == nil || *still.StageID == *done.StageID {
		t.Fatalf("the finished task has to have left the agent column, the open one not")
	}
}

// TestHumanStageSurvivesTerminalTask is the counter-check: if a human creates a
// column of their own and the task lies there, it stays there — auto-follow does
// not touch deliberately placed tasks, terminal or not.
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

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Invoice", "[mock:result done]", "manual", 3)
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
		t.Fatalf("a human column must not be cleaned up, board: %+v", stages)
	}
	done, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if done.StageID == nil || *done.StageID != human.ID {
		t.Fatalf("a deliberately placed task must not be moved")
	}
}
