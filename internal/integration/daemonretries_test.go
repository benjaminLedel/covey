package integration

import (
	"context"
	"testing"

	"covey/internal/backlog"

	"github.com/google/uuid"
)

// The retry cap for lost sandbox connections rests on one number that has to
// survive a restart of the control plane: backlog_tasks.daemon_retries. A
// restart is one of the things that PRODUCES these losses, so a counter kept in
// process memory would be back at zero right afterwards and the loop it is
// supposed to stop would keep running.
//
// These tests therefore go through the real store against the real database,
// not through a double: what is being checked is precisely that the number
// lands in the row and comes back out of it.

// newStoreStack is newStack with the dispatch loop stopped. These tests drive
// the state machine by hand, task by task; a live dispatcher would claim the
// same open tasks first and race them for it. What is under test here is the
// store, not the orchestration around it.
func newStoreStack(t *testing.T) *stack {
	t.Helper()
	s := newStack(t)
	s.cancel()
	return s
}

// claimed puts a fresh task into in_progress — the state every one of these
// transitions starts from.
func (s *stack) claimed(agentID uuid.UUID, title string) backlog.Task {
	s.t.Helper()
	ctx := context.Background()
	if _, err := s.backlog.Create(ctx, s.orgID, agentID, title, "body", "test", 5); err != nil {
		s.t.Fatal(err)
	}
	return s.reclaim(agentID)
}

// reclaim is the next run picking the task up again — what makes a series of
// losses a series rather than one loss seen repeatedly.
func (s *stack) reclaim(agentID uuid.UUID) backlog.Task {
	s.t.Helper()
	task, err := s.backlog.ClaimNext(context.Background(), agentID)
	if err != nil {
		s.t.Fatal(err)
	}
	return task
}

func (s *stack) retriesOf(id uuid.UUID) int {
	s.t.Helper()
	task, err := s.backlog.Get(context.Background(), id)
	if err != nil {
		s.t.Fatal(err)
	}
	return task.DaemonRetries
}

// TestDaemonRetriesCountUp: every lost connection in a row raises the count by
// one, and the value the caller gets back is already the new one — the
// orchestrator decides on that number whether to requeue once more.
func TestDaemonRetriesCountUp(t *testing.T) {
	s := newStoreStack(t)
	agent := s.newSupportAgent("retry-counter")
	ctx := context.Background()

	task := s.claimed(agent.ID, "Task that keeps losing its sandbox")
	if task.DaemonRetries != 0 {
		t.Fatalf("a fresh task starts at %d losses, want 0", task.DaemonRetries)
	}

	for want := 1; want <= 3; want++ {
		reopened, err := s.backlog.ReopenAfterDaemonLoss(ctx, task.ID, "daemon connection lost — retrying")
		if err != nil {
			t.Fatalf("loss %d: %v", want, err)
		}
		if reopened.DaemonRetries != want {
			t.Fatalf("after loss %d the returned task carries %d, want %d",
				want, reopened.DaemonRetries, want)
		}
		if reopened.State != backlog.StateOpen {
			t.Fatalf("after loss %d the task is %q, want open", want, reopened.State)
		}
		if got := s.retriesOf(task.ID); got != want {
			t.Fatalf("after loss %d the database holds %d, want %d — the count has to "+
				"survive a control-plane restart, so it may not live only in the returned struct", want, got, want)
		}
		s.reclaim(agent.ID)
	}
}

// TestDaemonRetriesResetOnOtherOutcomes is the "in a row" half of the contract.
// A task that hits a blip today, works fine for a week and hits another one
// next Tuesday must not fail on the accumulated total — only a real series
// counts.
func TestDaemonRetriesResetOnOtherOutcomes(t *testing.T) {
	s := newStoreStack(t)
	agent := s.newSupportAgent("retry-reset")
	ctx := context.Background()

	// Each case: lose the connection twice, then end a run some OTHER way and
	// check the count is back to zero.
	cases := map[string]func(t *testing.T, taskID uuid.UUID){
		// The budget stop and a kill mid-run: the run ended for its own
		// reasons, the link was fine.
		"plain reopen": func(t *testing.T, taskID uuid.UUID) {
			if _, err := s.backlog.Reopen(ctx, taskID, "budget exceeded — agent paused"); err != nil {
				t.Fatal(err)
			}
		},
		// The run got far enough to ask a question — whatever came before it
		// was not a series.
		"blocked": func(t *testing.T, taskID uuid.UUID) {
			if _, err := s.backlog.Block(ctx, taskID, "corr-"+taskID.String(), "sess-1", "May I?"); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, endTheRun := range cases {
		t.Run(name, func(t *testing.T) {
			task := s.claimed(agent.ID, "Task ending via "+name)
			for i := 0; i < 2; i++ {
				if _, err := s.backlog.ReopenAfterDaemonLoss(ctx, task.ID, "lost"); err != nil {
					t.Fatal(err)
				}
				s.reclaim(agent.ID)
			}
			if got := s.retriesOf(task.ID); got != 2 {
				t.Fatalf("precondition: %d losses recorded, want 2", got)
			}

			endTheRun(t, task.ID)

			if got := s.retriesOf(task.ID); got != 0 {
				t.Fatalf("after %q the count is %d, want 0 — otherwise single blips spread over "+
					"weeks add up and fail a task that never had a real series", name, got)
			}
		})
	}
}

// TestDaemonRetriesResetOnRetry: somebody is deliberately asking for another
// attempt — including for a task that was failed BECAUSE of a series of
// losses. That attempt has to start from a clean count, otherwise it fails
// again on its first hiccup and the retry button does nothing.
func TestDaemonRetriesResetOnRetry(t *testing.T) {
	s := newStoreStack(t)
	agent := s.newSupportAgent("retry-button")
	ctx := context.Background()

	task := s.claimed(agent.ID, "Task failed after a series of losses")
	for i := 0; i < 4; i++ {
		if _, err := s.backlog.ReopenAfterDaemonLoss(ctx, task.ID, "lost"); err != nil {
			t.Fatal(err)
		}
		s.reclaim(agent.ID)
	}
	// This is what the orchestrator does on the fifth loss in a row.
	if _, err := s.backlog.Complete(ctx, task.ID, backlog.StateFailed, "",
		"sandbox connection lost 5 times in a row — giving up instead of requeueing again"); err != nil {
		t.Fatal(err)
	}
	if s.taskState(task.ID) != backlog.StateFailed {
		t.Fatalf("precondition: task is %q, want failed", s.taskState(task.ID))
	}

	if _, err := s.backlog.Retry(ctx, task.ID, "retried by hand"); err != nil {
		t.Fatal(err)
	}
	if got := s.retriesOf(task.ID); got != 0 {
		t.Fatalf("after Retry the count is %d, want 0 — a deliberate new attempt starts fresh", got)
	}
}
