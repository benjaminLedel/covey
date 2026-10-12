package workflow

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// world stands in for covey around the engine: who is available, and a
// record of every task, action, question and message the engine sent out.
type world struct {
	away      map[string]bool
	tasks     []Task
	actions   []Action
	questions []Question
	messages  []Message
	actErr    error
}

func (w *world) Available(name string) bool { return !w.away[name] }
func (w *world) Dispatch(t Task) error      { w.tasks = append(w.tasks, t); return nil }
func (w *world) Ask(q Question) error       { w.questions = append(w.questions, q); return nil }
func (w *world) Notify(m Message) error     { w.messages = append(w.messages, m); return nil }
func (w *world) Act(a Action) error {
	if w.actErr != nil {
		return w.actErr
	}
	w.actions = append(w.actions, a)
	return nil
}

// tasksFor lists who got which step, in order: "b:ada r:theo".
func (w *world) handedOut() string {
	var parts []string
	for _, t := range w.tasks {
		parts = append(parts, t.Step+":"+t.Assignee)
	}
	return strings.Join(parts, " ")
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time       { return c.t }
func (c *clock) pass(d time.Duration) { c.t = c.t.Add(d) }
func newClock() *clock                { return &clock{t: time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC)} }

func engine(t *testing.T, src string, w *world) (*Engine, *clock) {
	t.Helper()
	d, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	c := newClock()
	e, err := New(d, w, c.now)
	if err != nil {
		t.Fatal(err)
	}
	return e, c
}

func example(t *testing.T, w *world) (*Engine, *clock) {
	t.Helper()
	src, err := os.ReadFile("testdata/customer-ticket.workflow")
	if err != nil {
		t.Fatal(err)
	}
	return engine(t, string(src), w)
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func at(t *testing.T, e *Engine, id string) Run {
	t.Helper()
	r, ok := e.Run(id)
	if !ok {
		t.Fatalf("run %s not found", id)
	}
	return r
}

func expect(t *testing.T, r Run, step string, state State) {
	t.Helper()
	if r.Step != step || r.State != state {
		t.Fatalf("%s at %s/%s, want %s/%s\nlog:\n%s", r.ID, r.Step, r.State, step, state, logOf(r))
	}
}

func logOf(r Run) string {
	var b strings.Builder
	for _, l := range r.Log {
		b.WriteString("  " + l.Step + ": " + l.Text + "\n")
	}
	return b.String()
}

// ---------- the simplest workflow: one colleague, then done ----------

const straight = `
owner: alex
mode: steer
roles:
  Support: [mia]
steps:
  t:
    type: trigger
    next: b
  b:
    type: agent
    title: Answer
    role: Support
    task: "Answer {{case.title}} for {{case.customer}}"
    next: e
  e:
    type: end
`

func TestAStraightWorkflowGivesTheStepToTheRoleAndEnds(t *testing.T) {
	w := &world{}
	e, _ := engine(t, straight, w)
	r, err := e.Start("#1", map[string]string{"title": "Login fails", "customer": "ACME"})
	must(t, err)
	expect(t, r, "b", StateWorking)
	if r.Assignee != "mia" || len(w.tasks) != 1 {
		t.Fatalf("assignee %q, tasks %v", r.Assignee, w.tasks)
	}
	if got := w.tasks[0].Text; got != "Answer Login fails for ACME" {
		t.Errorf("task text = %q", got)
	}
	must(t, e.Done(r.ID, map[string]string{"reply": "sent"}))
	r = at(t, e, r.ID)
	expect(t, r, "e", StateDone)
	if r.Had["b"] != "mia" || r.Vars["b"]["reply"] != "sent" {
		t.Errorf("had %v, vars %v", r.Had, r.Vars)
	}
}

func TestACaseThatComesBackContinuesItsRun(t *testing.T) {
	w := &world{}
	e, _ := engine(t, straight, w)
	first, _ := e.Start("#1", nil)
	again, _ := e.Start("#1", nil)
	if first.ID != again.ID || len(w.tasks) != 1 {
		t.Fatalf("a second start made %s (first %s) and %d tasks", again.ID, first.ID, len(w.tasks))
	}
	must(t, e.Done(first.ID, nil))
	// Once done, the same case may start over: a reopened ticket is new work.
	next, _ := e.Start("#1", nil)
	if next.ID == first.ID {
		t.Fatal("a finished run was continued")
	}
}

func TestAResultMustCarryWhatLaterStepsRead(t *testing.T) {
	w := &world{}
	e, _ := example(t, w)
	r, _ := e.Start("#7", map[string]string{"no": "#7", "title": "Export"})
	if err := e.Done(r.ID, map[string]string{"summary": "done"}); !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Done without mr = %v, want ErrIncomplete", err)
	}
	expect(t, at(t, e, r.ID), "b", StateWorking)
	if err := e.Decide(r.ID, "retry"); !errors.Is(err, ErrWrongState) {
		t.Errorf("Decide at a colleague step = %v, want ErrWrongState", err)
	}
	if err := e.Done("nope", nil); !errors.Is(err, ErrUnknownRun) {
		t.Errorf("Done on an unknown run = %v", err)
	}
}

// ---------- branching, actions, messages ----------

const branching = `
owner: alex
mode: steer
roles:
  Triage: [mia]
steps:
  t:
    type: trigger
    next: k
  k:
    type: ai
    prompt: "Classify {{case.text}}"
    result: [kind]
    next: c
  c:
    type: condition
    on: k.kind
    next:
      bug: bug
      else: n
  bug:
    type: action
    system: gitlab
    do: label
    target: "{{case.no}}"
    value: bug
    next: e
  n:
    type: notify
    to: mia
    via: chat
    text: "{{case.no}} is a {{k.kind}}"
    next: e
  e:
    type: end
`

func TestAConditionBranchesOnAResultAndCoveyActsItself(t *testing.T) {
	w := &world{}
	e, _ := engine(t, branching, w)
	a, _ := e.Start("#1", map[string]string{"no": "#1", "text": "crash on save"})
	if len(w.tasks) != 1 || w.tasks[0].Assignee != "" || w.tasks[0].Text != "Classify crash on save" {
		t.Fatalf("the AI step was not handed to the model: %+v", w.tasks)
	}
	must(t, e.Done(a.ID, map[string]string{"kind": "bug"}))
	expect(t, at(t, e, a.ID), "e", StateDone)
	if len(w.actions) != 1 || w.actions[0].Target != "#1" || w.actions[0].Value != "bug" {
		t.Fatalf("actions = %+v", w.actions)
	}

	b, _ := e.Start("#2", map[string]string{"no": "#2"})
	must(t, e.Done(b.ID, map[string]string{"kind": "question"}))
	expect(t, at(t, e, b.ID), "e", StateDone)
	if len(w.messages) != 1 || w.messages[0].Text != "#2 is a question" || w.messages[0].To != "mia" {
		t.Fatalf("messages = %+v", w.messages)
	}
}

func TestAFailedActionStopsTheRunAndTellsTheOwner(t *testing.T) {
	w := &world{actErr: errors.New("403")}
	e, _ := engine(t, branching, w)
	r, _ := e.Start("#1", map[string]string{"no": "#1"})
	must(t, e.Done(r.ID, map[string]string{"kind": "bug"}))
	expect(t, at(t, e, r.ID), "bug", StateFailed)
	if len(w.messages) != 1 || w.messages[0].To != "alex" || !strings.Contains(w.messages[0].Text, "403") {
		t.Fatalf("owner was not told: %+v", w.messages)
	}
	// A failed run frees its case.
	again, _ := e.Start("#1", nil)
	if again.ID == r.ID {
		t.Error("a failed run was continued")
	}
}

// ---------- roles as pools ----------

const pool = `
owner: alex
mode: steer
roles:
  Eng: [ada, kim]
steps:
  t:
    type: trigger
    next: b
  b:
    type: agent
    role: Eng
    pick: %s
    max_parallel: 1
    next: e
  e:
    type: end
`

func TestTheRoleWithLeastLoadGetsTheNextCase(t *testing.T) {
	w := &world{}
	e, _ := engine(t, strings.Replace(pool, "%s", "load", 1), w)
	r1, _ := e.Start("#1", nil)
	e.Start("#2", nil)
	if got := w.handedOut(); got != "b:ada b:kim" {
		t.Fatalf("handed out %q", got)
	}
	// Ada is free again, Kim is not: the next case goes to Ada.
	must(t, e.Done(r1.ID, nil))
	e.Start("#3", nil)
	if got := w.handedOut(); got != "b:ada b:kim b:ada" {
		t.Fatalf("handed out %q", got)
	}
}

func TestInTurnWalksTheRole(t *testing.T) {
	w := &world{}
	e, _ := engine(t, strings.Replace(strings.Replace(pool, "%s", "round", 1), "max_parallel: 1", "max_parallel: 0", 1), w)
	for _, c := range []string{"#1", "#2", "#3"} {
		e.Start(c, nil)
	}
	if got := w.handedOut(); got != "b:ada b:kim b:ada" {
		t.Fatalf("handed out %q", got)
	}
}

func TestCasesQueueAtTheLimitAndGoOutOldestFirst(t *testing.T) {
	w := &world{}
	e, c := engine(t, strings.Replace(pool, "%s", "load", 1), w)
	r1, _ := e.Start("#1", nil)
	e.Start("#2", nil)
	c.pass(time.Minute)
	r3, _ := e.Start("#3", nil)
	c.pass(time.Minute)
	r4, _ := e.Start("#4", nil)
	expect(t, r3, "b", StateQueued)
	expect(t, r4, "b", StateQueued)
	if len(w.tasks) != 2 {
		t.Fatalf("%d tasks while both are at their limit", len(w.tasks))
	}
	must(t, e.Done(r1.ID, nil))
	if r := at(t, e, r3.ID); r.State != StateWorking || r.Assignee != "ada" {
		t.Fatalf("the oldest queued case did not go out: %+v", r)
	}
	expect(t, at(t, e, r4.ID), "b", StateQueued)
}

func TestAnOptionalStepIsSkippedARequiredOneWaitsForSomeone(t *testing.T) {
	w := &world{away: map[string]bool{"theo": true, "rosa": true}}
	e, c := example(t, w)
	r, _ := e.Start("#9", map[string]string{"no": "#9"})
	must(t, e.Done(r.ID, map[string]string{"mr": "!1"}))
	r = at(t, e, r.ID)
	// Review is optional and Theo is away: skipped. Acceptance is required
	// and Rosa is away: the case waits and the owner hears it once.
	expect(t, r, "a", StateWaiting)
	if !r.Unstaffed || len(w.messages) != 1 || w.messages[0].To != "alex" {
		t.Fatalf("unstaffed %v, messages %+v", r.Unstaffed, w.messages)
	}
	e.Tick()
	if len(w.messages) != 1 {
		t.Fatalf("the owner was told again: %+v", w.messages)
	}
	w.away["rosa"] = false
	c.pass(time.Hour)
	e.Tick()
	r = at(t, e, r.ID)
	if r.State != StateWorking || r.Assignee != "rosa" || r.Unstaffed {
		t.Fatalf("after rosa came back: %+v", r)
	}
	if strings.Contains(w.handedOut(), "r:") {
		t.Errorf("the skipped review was handed out: %s", w.handedOut())
	}
}

func TestTheFourEyesRuleKeepsTheBuilderOffTheCheck(t *testing.T) {
	const src = `
owner: alex
mode: steer
roles:
  Eng: [ada]
  Check: [ada, kim]
steps:
  t:
    type: trigger
    next: b
  b:
    type: agent
    role: Eng
    next: a
  a:
    type: agent
    role: Check
    not_same_as: b
    next: e
  e:
    type: end
`
	w := &world{}
	e, _ := engine(t, src, w)
	r, _ := e.Start("#1", nil)
	must(t, e.Done(r.ID, nil))
	if got := w.handedOut(); got != "b:ada a:kim" {
		t.Fatalf("handed out %q, want kim to check ada's work", got)
	}
	// With Kim away, nobody else may check: the case waits.
	w2 := &world{away: map[string]bool{"kim": true}}
	e2, _ := engine(t, src, w2)
	r2, _ := e2.Start("#1", nil)
	must(t, e2.Done(r2.ID, nil))
	if r := at(t, e2, r2.ID); r.State != StateWaiting || !r.Unstaffed {
		t.Fatalf("ada was let check her own work: %+v", r)
	}
}

// ---------- the full example: rounds, the limit, a decision, the wait ----------

func TestTheCustomerTicketGoesRoundsThenAPersonDecides(t *testing.T) {
	w := &world{away: map[string]bool{"theo": true}}
	e, _ := example(t, w)
	r, _ := e.Start("#384", map[string]string{"no": "#384", "title": "Import with umlauts"})
	id := r.ID
	if r.Assignee != "ada" {
		t.Fatalf("first build went to %q", r.Assignee)
	}

	// Three rounds of build and acceptance with defects. Every round goes
	// back to Ada (sticky) and to Rosa.
	for round := 1; round <= 3; round++ {
		must(t, e.Done(id, map[string]string{"mr": "!198"}))
		expect(t, at(t, e, id), "a", StateWorking)
		must(t, e.Done(id, map[string]string{"result": "defects", "defects": "ß becomes ss"}))
		if round < 3 {
			r = at(t, e, id)
			expect(t, r, "b", StateWorking)
			if r.Assignee != "ada" || r.Loops["c"] != round {
				t.Fatalf("round %d: assignee %q, loops %d", round+1, r.Assignee, r.Loops["c"])
			}
		}
	}
	// The rounds are used up: Alex decides.
	expect(t, at(t, e, id), "h", StateWaiting)
	if len(w.questions) != 1 || w.questions[0].To != "alex" {
		t.Fatalf("questions = %+v", w.questions)
	}
	if got := w.questions[0].Text; got != "Three rounds without acceptance on #384. Last defects: ß becomes ss" {
		t.Errorf("question = %q", got)
	}
	if err := e.Decide(id, "timeout"); !errors.Is(err, ErrUnknownExit) {
		t.Errorf("deciding the timeout exit = %v", err)
	}

	// One more round: back to Ada, with the defects in her task.
	must(t, e.Decide(id, "retry"))
	r = at(t, e, id)
	expect(t, r, "b", StateWorking)
	if last := w.tasks[len(w.tasks)-1]; last.Assignee != "ada" || !strings.Contains(last.Text, "Earlier defects: ß becomes ss") {
		t.Fatalf("the extra round's task: %+v", last)
	}
	must(t, e.Done(id, map[string]string{"mr": "!198"}))
	must(t, e.Done(id, map[string]string{"result": "accepted", "defects": ""}))

	// Accepted: covey assigns the ticket itself and waits for the customer.
	expect(t, at(t, e, id), "w", StateWaiting)
	if len(w.actions) != 1 || w.actions[0].Target != "#384" || w.actions[0].Value != "alex" || w.actions[0].Comment != "Accepted, !198. Please start the customer test." {
		t.Fatalf("actions = %+v", w.actions)
	}
	if err := e.Event(id, "label:something-else"); !errors.Is(err, ErrWrongState) {
		t.Errorf("an event nobody waits for = %v", err)
	}
	must(t, e.Event(id, "label:customer-ok"))
	expect(t, at(t, e, id), "e", StateDone)
	if got := w.handedOut(); got != "b:ada a:rosa b:ada a:rosa b:ada a:rosa b:ada a:rosa" {
		t.Errorf("handed out %q", got)
	}
}

func TestTakingItOverEndsTheRunWithAMessage(t *testing.T) {
	w := &world{away: map[string]bool{"theo": true}}
	e, _ := example(t, w)
	r, _ := e.Start("#5", map[string]string{"no": "#5"})
	for i := 0; i < 3; i++ {
		must(t, e.Done(r.ID, map[string]string{"mr": "!5"}))
		must(t, e.Done(r.ID, map[string]string{"result": "defects", "defects": "x"}))
	}
	must(t, e.Decide(r.ID, "self"))
	expect(t, at(t, e, r.ID), "e", StateDone)
	if len(w.messages) != 1 || w.messages[0].Text != "#5 is with you now." {
		t.Fatalf("messages = %+v", w.messages)
	}
}

// ---------- time ----------

func TestAStepOverItsLimitIsReportedOnce(t *testing.T) {
	w := &world{away: map[string]bool{"theo": true}}
	e, c := example(t, w)
	r, _ := e.Start("#1", map[string]string{"no": "#1"})
	c.pass(3 * time.Hour)
	e.Tick()
	if at(t, e, r.ID).Stuck {
		t.Fatal("stuck before the limit")
	}
	c.pass(2 * time.Hour)
	e.Tick()
	e.Tick()
	if !at(t, e, r.ID).Stuck || len(w.messages) != 1 {
		t.Fatalf("stuck %v, messages %+v", at(t, e, r.ID).Stuck, w.messages)
	}
	// Moving on clears it: the limit is per step.
	must(t, e.Done(r.ID, map[string]string{"mr": "!1"}))
	if at(t, e, r.ID).Stuck {
		t.Error("still stuck at the next step")
	}
}

func TestAWaitPastItsMaximumTakesItsTimeoutExit(t *testing.T) {
	w := &world{away: map[string]bool{"theo": true}}
	e, c := example(t, w)
	r, _ := e.Start("#1", map[string]string{"no": "#1"})
	must(t, e.Done(r.ID, map[string]string{"mr": "!1"}))
	must(t, e.Done(r.ID, map[string]string{"result": "accepted", "defects": ""}))
	expect(t, at(t, e, r.ID), "w", StateWaiting)
	c.pass(13 * 24 * time.Hour)
	e.Tick()
	expect(t, at(t, e, r.ID), "w", StateWaiting)
	c.pass(24 * time.Hour)
	e.Tick()
	expect(t, at(t, e, r.ID), "e", StateDone)
	if len(w.messages) != 1 || w.messages[0].To != "alex" {
		t.Fatalf("the timeout way did not tell alex: %+v", w.messages)
	}
}

func TestAPlainDelayGoesOnByItself(t *testing.T) {
	const src = `
owner: alex
mode: steer
steps:
  t:
    type: trigger
    next: w
  w:
    type: wait
    max: 2h
    next: e
  e:
    type: end
`
	e, c := engine(t, src, &world{})
	r, _ := e.Start("#1", nil)
	expect(t, r, "w", StateWaiting)
	c.pass(2 * time.Hour)
	e.Tick()
	expect(t, at(t, e, r.ID), "e", StateDone)
}

// ---------- modes ----------

func TestOffStartsNothingObserveOnlyRecords(t *testing.T) {
	off, _ := engine(t, strings.Replace(straight, "mode: steer", "mode: off", 1), &world{})
	if _, err := off.Start("#1", nil); !errors.Is(err, ErrOff) {
		t.Fatalf("Start when off = %v", err)
	}

	w := &world{}
	watch, _ := engine(t, strings.Replace(straight, "mode: steer", "mode: watch", 1), w)
	r, _ := watch.Start("#1", nil)
	expect(t, r, "b", StateWatching)
	if len(w.tasks) != 0 {
		t.Fatalf("observe mode handed out %v", w.tasks)
	}
	if err := watch.Done(r.ID, nil); !errors.Is(err, ErrWrongState) {
		t.Errorf("Done in observe mode = %v", err)
	}
	must(t, watch.Observe(r.ID, "e"))
	expect(t, at(t, watch, r.ID), "e", StateDone)
}

// ---------- the guard ----------

func TestALoopThatNeverWaitsIsStopped(t *testing.T) {
	// The condition counts, but its limit is far beyond what one call may
	// pass through: the guard stops the run rather than the server.
	const src = `
owner: alex
mode: steer
steps:
  t:
    type: trigger
    next: c
  c:
    type: condition
    on: t.x
    max_rounds: 1000
    next:
      a: n
      limit: e
  n:
    type: notify
    to: alex
    next: c
  e:
    type: end
`
	w := &world{}
	e, _ := engine(t, src, w)
	r, _ := e.Start("#1", map[string]string{"x": "a"})
	if r.State != StateFailed {
		t.Fatalf("a loop without waiting ended %s", r.State)
	}
	if last := w.messages[len(w.messages)-1]; last.To != "alex" || !strings.Contains(last.Text, "without waiting") {
		t.Errorf("owner was not told: %+v", last)
	}
}

func TestAnotherRoundGoesBackToWhoBuiltItEvenWhenOthersAreFreer(t *testing.T) {
	const src = `
owner: alex
mode: steer
roles:
  Eng: [ada, kim]
  Test: [rosa]
steps:
  t:
    type: trigger
    next: b
  b:
    type: agent
    role: Eng
    sticky: true
    max_parallel: 2
    next: a
  a:
    type: agent
    role: Test
    result: [ok]
    next: c
  c:
    type: condition
    on: a.ok
    max_rounds: 3
    next:
      "yes": e
      "no": b
      limit: e
  e:
    type: end
`
	w := &world{}
	e, _ := engine(t, src, w)
	r1, _ := e.Start("#1", nil) // ada
	r2, _ := e.Start("#2", nil) // kim
	e.Start("#3", nil)          // ada again: both had one
	must(t, e.Done(r2.ID, nil)) // kim is free now
	must(t, e.Done(r1.ID, nil)) // #1 goes to acceptance
	must(t, e.Done(r1.ID, map[string]string{"ok": "no"}))
	// Kim has nothing to do and Ada has #3, yet #1 returns to Ada: she knows
	// her branch and what was wrong with it.
	if r := at(t, e, r1.ID); r.Step != "b" || r.Assignee != "ada" {
		t.Fatalf("the second round went to %q at %s", r.Assignee, r.Step)
	}
}

// ---------- storage: what changed, and picking up after a restart ----------

func TestEveryCallReportsTheRunsItChanged(t *testing.T) {
	w := &world{}
	e, _ := engine(t, strings.Replace(pool, "%s", "load", 1), w)
	var told [][]string
	e.OnChange(func(rs []Run) {
		var cases []string
		for _, r := range rs {
			cases = append(cases, r.Case+"@"+string(r.State))
		}
		told = append(told, cases)
	})
	r1, _ := e.Start("#1", nil)
	e.Start("#2", nil)
	e.Start("#3", nil) // queued
	must(t, e.Done(r1.ID, nil))
	e.Tick() // nothing is due: nothing to tell
	got := fmt.Sprint(told)
	want := "[[#1@working] [#2@working] [#3@queued] [#1@done #3@working]]"
	if got != want {
		t.Fatalf("told %s\nwant %s", got, want)
	}
}

func TestAnEngineRestoredFromItsRunsCarriesOn(t *testing.T) {
	w := &world{away: map[string]bool{"theo": true}}
	before, _ := example(t, w)
	kept := map[string]Run{}
	before.OnChange(func(rs []Run) {
		for _, r := range rs {
			kept[r.ID] = r
		}
	})
	r, _ := before.Start("#384", map[string]string{"no": "#384"})
	for i := 0; i < 2; i++ {
		must(t, before.Done(r.ID, map[string]string{"mr": "!1"}))
		must(t, before.Done(r.ID, map[string]string{"result": "defects", "defects": "x"}))
	}

	// A restart: a new engine, the same definition, the runs as stored.
	after, _ := example(t, w)
	var runs []Run
	for _, r := range kept {
		runs = append(runs, r)
	}
	must(t, after.Restore(runs))
	again, _ := after.Start("#384", nil)
	if again.ID != r.ID {
		t.Fatal("the restored engine opened a second run for the same case")
	}
	// The third round is the last: one more defect goes to Alex.
	must(t, after.Done(r.ID, map[string]string{"mr": "!1"}))
	must(t, after.Done(r.ID, map[string]string{"result": "defects", "defects": "x"}))
	expect(t, at(t, after, r.ID), "h", StateWaiting)

	if err := after.Restore(runs); err == nil {
		t.Error("restored twice into the same engine")
	}
	twice, _ := example(t, w)
	dup := runs[0]
	dup.ID = "other"
	if err := twice.Restore([]Run{runs[0], dup}); err == nil {
		t.Error("two open runs for one case were restored")
	}
}

func TestAFailedStepEndsTheRunButALateOneDoesNot(t *testing.T) {
	w := &world{}
	e, _ := engine(t, straight, w)
	r, _ := e.Start("#1", nil)
	if err := e.StepFailed(r.ID, "e", "late"); !errors.Is(err, ErrWrongState) {
		t.Fatalf("a failure for a step the run is not at = %v", err)
	}
	must(t, e.StepFailed(r.ID, "b", "the task failed: no access"))
	expect(t, at(t, e, r.ID), "b", StateFailed)
	if len(w.messages) != 1 || !strings.Contains(w.messages[0].Text, "no access") {
		t.Fatalf("owner told %+v", w.messages)
	}
}
