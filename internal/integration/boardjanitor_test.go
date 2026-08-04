package integration

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestBoardJanitorArchivesOldTerminalTasks checks the board's self-cleanup: a
// terminal task older than the deadline is archived by the control plane on its
// own — and the agent column that becomes empty as a result disappears with it.
// Without this path every finished card keeps its column alive; the column
// cleanup alone never takes hold because the corpse stays put. That is exactly
// how a board with a dozen dead columns came about in practice, one that only a
// human on the "clean up" button could get rid of again.
func TestBoardJanitorArchivesOldTerminalTasks(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	// Paused: the tasks are meant to stay put, not to be dispatched.
	agent := s.newSupportAgent("janitor-agent")
	if err := s.registry.SetKilled(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.backlog.SeedDefaultStages(ctx, agent.ID); err != nil {
		t.Fatal(err)
	}

	// Two terminal tasks, each in its own agent column — the state a board is in
	// after days of work.
	alt := s.terminalTaskInStage(t, agent.ID, "Alte Analyse", "Analyse #83")
	frisch := s.terminalTaskInStage(t, agent.ID, "Frische Analyse", "Analyse #99")

	// Only the old task is past the deadline.
	if _, err := s.pool.Exec(ctx,
		"UPDATE backlog_tasks SET updated_at = now() - interval '48 hours' WHERE id=$1", alt.ID); err != nil {
		t.Fatal(err)
	}

	n, err := s.backlog.ArchiveTerminalOlderThan(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("exactly the old task should be archived, there were %d", n)
	}

	altNach, err := s.backlog.Get(ctx, alt.ID)
	if err != nil {
		t.Fatal(err)
	}
	if altNach.ArchivedAt == nil {
		t.Fatalf("the old task must be archived")
	}
	if altNach.State != backlog.StateDone {
		t.Fatalf("archiving is not deleting — the state stays done, is %q", altNach.State)
	}
	frischNach, err := s.backlog.Get(ctx, frisch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if frischNach.ArchivedAt != nil {
		t.Fatalf("freshly finished work must stay visible on the board — otherwise the last run's work vanishes before the reviewer's eyes")
	}

	stages, err := s.backlog.ListStages(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range stages {
		if st.Name == "Analyse #83" {
			t.Fatalf("the emptied agent column must disappear with it, board: %+v", stages)
		}
	}
	found := false
	for _, st := range stages {
		if st.Name == "Analyse #99" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the column of the fresh task must not be cleared away, board: %+v", stages)
	}
}

// terminalTaskInStage reproduces the state a run leaves behind: a finished task
// that sits in a column created by the agent.
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
	// Put it into the agent column after completion: that is what the boards
	// look like which came about before the auto-follow fix.
	if _, err := s.backlog.SetTaskStageByName(ctx, agentID, task.ID, stage); err != nil {
		t.Fatal(err)
	}
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
