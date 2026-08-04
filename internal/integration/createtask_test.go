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

// TestAgentCreatesSubtask checks the meta action covey/create_task for the
// agent's own account: the agent breaks down work that is too large itself
// instead of running into the turn limit. The subtask hangs off the originating
// task as a child, carries it as its origin — and is worked off afterwards.
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
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	child := childOf(t, s, agent.ID, task.ID)
	if child.Title != "Teil zwei" {
		t.Fatalf("the subtask has the wrong title: %q", child.Title)
	}
	if child.Origin != "agent:zerleger" {
		t.Fatalf("the origin must name the creating agent, is %q", child.Origin)
	}
	if child.Priority != 2 {
		t.Fatalf("the priority must be carried over, is %d", child.Priority)
	}
	// It is real work, not an index-card entry: the agent picks it up.
	waitFor(t, "subtask worked off", 20*time.Second, func() bool {
		return s.taskState(child.ID) == backlog.StateDone
	})
}

// TestAgentDelegatesToColleague checks delegation: with "agent":"<slug>" the
// task lands with the colleague from the same organization, not with the sender
// — and the colleague is woken up for it.
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
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	delegated := childOf(t, s, colleague.ID, task.ID)
	if delegated.AgentID != colleague.ID {
		t.Fatalf("the delegated task must sit with the colleague")
	}
	if delegated.OrgID != s.orgID {
		t.Fatalf("delegation must not leave the organization")
	}
	waitFor(t, "colleague works it off", 20*time.Second, func() bool {
		return s.taskState(delegated.ID) == backlog.StateDone
	})
}

// TestCreateTaskLoopProtection checks the brakes: an agent that can create
// tasks can keep itself busy until the budget is empty. That is why the control
// plane rejects a duplicate with the same title — exactly the pattern with
// which recurring runs otherwise build themselves a queue that never empties.
// An unknown target agent fails as well, instead of silently having no effect.
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
	waitFor(t, "task terminal", 20*time.Second, func() bool {
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
			t.Fatalf("a task for an unknown agent must not come into being")
		}
	}
	if n != 1 {
		t.Fatalf("a duplicate with the same title must be rejected, created: %d", n)
	}
}

// TestCreateTaskDepthLimit checks the depth brake: a task may be broken down,
// its subtask as well — but the chain ends. Without this limit an agent breaks
// its work down recursively until the budget is empty.
//
// The test builds the chain directly in the store (as deep as an agent would
// have produced it over several runs) and only then lets the lowest task run:
// it must not split off another one.
func TestCreateTaskDepthLimit(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("tiefengraber")

	split := `[mock:action covey/create_task {"title":"Noch eine Stufe","body":"weiter"}]
[mock:result versucht]`

	// Level 0 still breaks itself down — the chain is fresh.
	root, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Stufe 0", split, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "root done", 20*time.Second, func() bool {
		return s.taskState(root.ID) == backlog.StateDone
	})
	if _, err := s.backlog.Get(ctx, childOf(t, s, agent.ID, root.ID).ID); err != nil {
		t.Fatalf("the first decomposition must be allowed: %v", err)
	}

	// Now a chain that has already exhausted the limit.
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
	waitFor(t, "lowest level terminal", 30*time.Second, func() bool {
		st := s.taskState(deep)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	n, err := s.backlog.CountChildren(ctx, deep)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("at the end of the chain nothing may be decomposed further, created: %d", n)
	}
	// And visibly so: the agent gets the rejection back with a reason instead of
	// the task vanishing into nothing without a word.
	if msg := taskError(t, s, deep); !strings.Contains(msg, "chain too deep") {
		t.Fatalf("the rejection must name the depth, was %q", msg)
	}
}

// TestCreateTaskBreadthLimit checks the breadth brake: a single run may only
// split off a limited number of tasks. Whoever needs more has not broken down
// their work but copied it.
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
	waitFor(t, "task terminal", 30*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	n, err := s.backlog.CountChildren(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatalf("the first subtasks must come into being")
	}
	if n > 10 {
		t.Fatalf("a run may split off at most 10 tasks, there were %d", n)
	}
}

// TestCreateTaskGuardRail checks that covey/create_task — unlike the remaining
// covey meta actions — runs through the guard rails: delegation
// (covey:create_task:foreign) can be forbidden without taking away the agent's
// ability to break down its own work.
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
	waitFor(t, "task terminal", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	all, err := s.backlog.ListByAgent(ctx, sender.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range all {
		if strings.HasPrefix(a.Title, "Abgeblockt") {
			t.Fatalf("a forbidden delegation must not produce a task")
		}
	}
	// The agent's own decomposition stays allowed — the rule only hits delegation.
	if _, err := s.backlog.Get(ctx, childOf(t, s, sender.ID, task.ID).ID); err != nil {
		t.Fatalf("a subtask for itself must still come into being: %v", err)
	}
}

// taskError reads a task's error text (empty if none is set).
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
