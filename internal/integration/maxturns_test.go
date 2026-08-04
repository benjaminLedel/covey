package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// TestMaxTurnsContinuation checks the turn-limit path: a run that ends at the
// turn limit without a result no longer fails silently. Its handover state is
// attached to the task as a note, and from it a follow-up task emerges that
// resumes the runtime session and carries the work to the end.
//
// This is exactly where the endless loop used to run: max_turns arrived as
// failed without an error text, the interim state was lost, and the next
// heartbeat started the same work from scratch.
func TestMaxTurnsContinuation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("limit-agent")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Big assignment",
		"[mock:maxturns ## Done\nRepo cloned\n## Open\nWrite the fix\n## Next step\ntouch src/pay.go]",
		"manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "task terminal", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateFailed || st == backlog.StateDone
	})

	// The aborted run fails — but with a speaking error instead of an empty one.
	parent, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if parent.State != backlog.StateFailed {
		t.Fatalf("an aborted run has to be failed, is %s", parent.State)
	}
	if parent.Error == nil || !strings.Contains(*parent.Error, "turn limit") {
		t.Fatalf("the error text has to name the turn limit, is %v", parent.Error)
	}
	if parent.Result == nil || !strings.Contains(*parent.Result, "Next step") {
		t.Fatalf("the handover state has to be stored as the result, is %v", parent.Result)
	}

	// The interim state is visible as a note on the task.
	notes, err := s.backlog.ListNotes(ctx, task.ID)
	if err != nil || len(notes) == 0 {
		t.Fatalf("the interim state has to hang on the task as a note: %+v (err=%v)", notes, err)
	}
	if !strings.Contains(notes[0].Content, "src/pay.go") {
		t.Fatalf("the note has to contain the handover state: %q", notes[0].Content)
	}

	// The follow-up task hangs on the original task, resumes its session and
	// carries the same title (otherwise the heartbeat dedup fires alongside it).
	child := childOf(t, s, agent.ID, task.ID)
	if child.Title != parent.Title {
		t.Fatalf("the follow-up task has to carry the original task's title: %q", child.Title)
	}
	if child.RuntimeSessionID == nil || *child.RuntimeSessionID == "" {
		t.Fatalf("the follow-up task has to carry the runtime session to resume from")
	}
	if !strings.HasPrefix(child.Origin, "continuation:") {
		t.Fatalf("the follow-up task needs the origin continuation:…, is %q", child.Origin)
	}

	// And it runs through: the continued session reaches a result.
	waitFor(t, "follow-up task done", 20*time.Second, func() bool {
		return s.taskState(child.ID) == backlog.StateDone
	})
}

// TestMaxTurnsEscalatesAfterCap checks the loop protection: if every
// continuation runs into the turn limit again, the chain is not extended
// endlessly but escalates. Without that bound the continuation only replaces
// one endless loop with another.
func TestMaxTurnsEscalatesAfterCap(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("limit-loop-agent")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Never finished",
		"[mock:maxturns-always ## Open\neverything]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// Follow the chain until a task no longer produces a follow-up task.
	last := task.ID
	depth := 0
	waitFor(t, "chain breaks off", 60*time.Second, func() bool {
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
		t.Fatalf("the chain has to break off after 3 continuations, was %d", depth)
	}
	final, err := s.backlog.Get(ctx, last)
	if err != nil {
		t.Fatal(err)
	}
	if final.Result == nil || !strings.HasPrefix(*final.Result, "ESCALATED") {
		t.Fatalf("the last task of the chain has to escalate, is %v", final.Result)
	}
}

// childOf finds the (single) follow-up task of a task.
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
	t.Fatalf("no follow-up task found for %s", parentID)
	return backlog.Task{}
}
