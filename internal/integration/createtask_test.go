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

// TestDelegationToPausedColleagueWaits pins what triggered covey#202: a
// delegation to a paused colleague was refused, and with that the work was gone
// — the sender got an error it could do nothing with, and nothing recorded that
// the task had been wanted.
//
// Right is: the task is created and waits. runAgent returns while Killed is set,
// so it is not worked off — until a human releases the colleague. That is what
// the second half checks.
func TestDelegationToPausedColleagueWaits(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	sender := s.newSupportAgent("absender-pause")
	colleague := s.newSupportAgent("pausierter-kollege")

	if err := s.registry.SetKilled(ctx, colleague.ID, true); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, sender.ID, "Geht an den Pausierten",
		`[mock:action covey/create_task {"title":"Bitte später übernehmen","body":"Details","agent":"pausierter-kollege"}]
[mock:result delegiert]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 20*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// The delegation arrived, even though the colleague is paused.
	delegated := childOf(t, s, colleague.ID, task.ID)
	if delegated.AgentID != colleague.ID {
		t.Fatalf("the delegated task must sit with the colleague")
	}
	if got := s.taskState(delegated.ID); got == backlog.StateDone {
		t.Fatalf("a paused agent works nothing off, state was %q", got)
	}

	// And it stays put instead of quietly disappearing.
	time.Sleep(2 * time.Second)
	if got := s.taskState(delegated.ID); got == backlog.StateDone {
		t.Fatalf("the task was worked off despite the pause: %q", got)
	}

	// After the release it starts — the pause delayed it, it did not destroy it.
	if err := s.registry.SetKilled(ctx, colleague.ID, false); err != nil {
		t.Fatal(err)
	}
	s.orch.EnsureRunning(colleague.ID)
	waitFor(t, "the released colleague works it off", 30*time.Second, func() bool {
		return s.taskState(delegated.ID) == backlog.StateDone
	})
}

// idleRun is a task body the mock runtime finishes without doing anything. The
// chains below are built link by link in the store, and every link wakes its
// agent — an inert body keeps those runs from creating tasks of their own and
// the chain from growing sideways while the test builds it.
const idleRun = "[mock:result nichts zu tun]"

// relayChain builds a chain of tasks as a relay produces it: every link carries
// the origin of the station that created it and sits with the station that has
// to act on it. It returns the last link.
//
// links alternate "who created it" / "whose desk it lands on"; the body of the
// last link is what actually runs.
func relayChain(t *testing.T, s *stack, root uuid.UUID, links []relayLink) backlog.Task {
	t.Helper()
	last, err := s.backlog.Get(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range links {
		body := idleRun
		if i == len(links)-1 {
			body = l.body
		}
		child, err := s.backlog.CreateChild(context.Background(), last.ID, backlog.ChildSpec{
			AgentID: l.agentID, Title: l.title, Body: body, Origin: "agent:" + l.from,
		})
		if err != nil {
			t.Fatal(err)
		}
		last = child
	}
	return last
}

type relayLink struct {
	from    string    // the slug that created this link
	agentID uuid.UUID // whose desk it lands on
	title   string
	body    string
}

// TestCreateTaskRelaySecondRound pins covey#227: a relay is not a
// decomposition, and the depth brake must not treat it as one.
//
// Two stations pass one piece of work back and forth — writer hands over,
// reviewer hands back, writer corrects and hands over again. The chain is three
// agent-created links long at that point, and the old counter (which counted
// the chain rather than who extended it) refused the reviewer's second
// hand-back right here. Three drafts on a live instance stood still because of
// it, each waiting for objections that were never delivered.
func TestCreateTaskRelaySecondRound(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	writer := s.newSupportAgent("staffel-schreiber")
	reviewer := s.newSupportAgent("staffel-pruefer")

	handBack := `[mock:action covey/create_task {"title":"Rückgabe 2. Durchgang","body":"Einwände","agent":"staffel-schreiber"}]
[mock:result zurückgegeben]`

	root, err := s.backlog.Create(ctx, s.orgID, writer.ID, "Entwurf", idleRun, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	second := relayChain(t, s, root.ID, []relayLink{
		{from: "staffel-schreiber", agentID: reviewer.ID, title: "Übergabe"},
		{from: "staffel-pruefer", agentID: writer.ID, title: "Rückgabe"},
		{from: "staffel-schreiber", agentID: reviewer.ID, title: "Übergabe nach Korrektur", body: handBack},
	})

	waitFor(t, "second check terminal", 30*time.Second, func() bool {
		st := s.taskState(second.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	back := childOf(t, s, writer.ID, second.ID)
	if back.Title != "Rückgabe 2. Durchgang" {
		t.Fatalf("the second hand-back must reach the writer, found %q", back.Title)
	}
	if msg := taskError(t, s, second.ID); strings.Contains(msg, "too deep") {
		t.Fatalf("the relay must not run into the depth brake: %q", msg)
	}
}

// TestCreateTaskRelayRoundLimit is the other half: the brake still closes. A
// station that has handed the same chain on three times does not get a fourth —
// otherwise two agents keep a piece of work moving between them until the
// budget is empty, which is exactly what the depth brake exists against.
func TestCreateTaskRelayRoundLimit(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	writer := s.newSupportAgent("runden-schreiber")
	reviewer := s.newSupportAgent("runden-pruefer")

	handBack := `[mock:action covey/create_task {"title":"Rückgabe vierte Runde","body":"noch ein Einwand","agent":"runden-schreiber"}]
[mock:result versucht]`

	root, err := s.backlog.Create(ctx, s.orgID, writer.ID, "Entwurf", idleRun, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	var links []relayLink
	for i := 1; i <= 3; i++ {
		links = append(links,
			relayLink{from: "runden-schreiber", agentID: reviewer.ID, title: "Übergabe " + strconv.Itoa(i)},
			relayLink{from: "runden-pruefer", agentID: writer.ID, title: "Rückgabe " + strconv.Itoa(i)})
	}
	links = append(links, relayLink{from: "runden-schreiber", agentID: reviewer.ID,
		title: "Übergabe vierte Runde", body: handBack})
	last := relayChain(t, s, root.ID, links)

	waitFor(t, "fourth round terminal", 30*time.Second, func() bool {
		st := s.taskState(last.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	n, err := s.backlog.CountChildren(ctx, last.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("after three rounds nothing may be handed back, created: %d", n)
	}
	// And the reason says what to do instead — an agent that reads "do not
	// decompose further" after a hand-back learns nothing it can act on.
	if msg := taskError(t, s, last.ID); !strings.Contains(msg, "handed this chain on") {
		t.Fatalf("the rejection must name the rounds, was %q", msg)
	}
}

// TestCreateTaskRelayChainCeiling pins the backstop under the round count: with
// enough stations every single one can stay below its own limit while the chain
// grows on and on. The absolute length ends it.
func TestCreateTaskRelayChainCeiling(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	slugs := []string{"kette-a", "kette-b", "kette-c", "kette-d", "kette-e", "kette-f"}
	ids := make(map[string]uuid.UUID, len(slugs))
	for _, slug := range slugs {
		ids[slug] = s.newSupportAgent(slug).ID
	}
	handOn := `[mock:action covey/create_task {"title":"Noch eine Station","body":"weiter","agent":"kette-a"}]
[mock:result versucht]`

	root, err := s.backlog.Create(ctx, s.orgID, ids["kette-a"], "Rundlauf", idleRun, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	// Twelve links, every station twice — none of them has handed it on three
	// times, so the round count lets each of them through.
	var links []relayLink
	for round := 0; round < 2; round++ {
		for i, slug := range slugs {
			links = append(links, relayLink{
				from:    slug,
				agentID: ids[slugs[(i+1)%len(slugs)]],
				title:   "Station " + slug + " " + strconv.Itoa(round),
			})
		}
	}
	links[len(links)-1].body = handOn
	last := relayChain(t, s, root.ID, links)

	waitFor(t, "last station terminal", 30*time.Second, func() bool {
		st := s.taskState(last.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})

	n, err := s.backlog.CountChildren(ctx, last.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("the chain must end at the ceiling, created: %d", n)
	}
	if msg := taskError(t, s, last.ID); !strings.Contains(msg, "too long") {
		t.Fatalf("the rejection must name the length, was %q", msg)
	}
}
