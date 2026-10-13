package workflow

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Outside is everything a run touches beyond its own state. covey's
// dispatcher, plugins and chat sit behind it; the tests put a recorder there.
type Outside interface {
	// Available reports whether a colleague can take work now — not
	// stopped, not a draft.
	Available(name string) bool
	// Dispatch hands a step to a colleague, or to the model for an AI step.
	// The answer comes back through Engine.Done.
	Dispatch(Task) error
	// Act does an action in a target system, as the workflow.
	Act(Action) error
	// Ask puts a decision into a person's chat. The answer comes back
	// through Engine.Decide.
	Ask(Question) error
	// Notify writes into a person's chat; nothing comes back.
	Notify(Message) error
}

type Task struct {
	Run, Case, Step, Assignee string // Assignee is empty for an AI step
	Text                      string
	Result                    []string
}

// Action is a target-system action the workflow runs as a colleague (As,
// an agent slug or a role). Params are the plugin's own parameters with the
// placeholders filled. Pending is the approval an earlier attempt asked for.
type Action struct {
	Run, Case, Step string
	As, System, Do  string
	Params          map[string]string
	Pending         string
}

// PendingError is how Act says the action waits for an approval: the run
// waits at its step and the clock tries again until it is decided.
type PendingError struct{ Approval string }

func (p *PendingError) Error() string { return "waits for approval " + p.Approval }

type Question struct {
	Run, Step, To, Text string
	Options             []string
}

type Message struct {
	Run, Step, To, Via, Text string
}

// State is where a run stands at its step.
type State string

const (
	StateWorking  State = "working"  // a colleague or the model has the step
	StateQueued   State = "queued"   // every colleague who could take it is at their limit
	StateWaiting  State = "waiting"  // for a person, an event, or time
	StateWatching State = "watching" // observe mode: the position is read, not driven
	StateDone     State = "done"
	StateFailed   State = "failed"
)

// Run is one case going through the workflow.
type Run struct {
	ID       string
	Case     string
	Step     string
	State    State
	Assignee string
	// Since is when the run arrived at its step, or entered its queue.
	Since time.Time
	// Stuck is set once a colleague step runs past its limit.
	Stuck bool
	// Unstaffed is set while a required step waits for someone to hold its role.
	Unstaffed bool
	// Vars holds what each step reported, by step id; the trigger's under
	// its id and under "case".
	Vars map[string]map[string]string
	// Loops counts, per condition, how often it sent the case back.
	Loops map[string]int
	// Had is who last did each colleague step — for "stays with the case"
	// and the four-eyes rule.
	Had map[string]string
	Log []Entry

	seq int
}

type Entry struct {
	At   time.Time `json:"at"`
	Step string    `json:"step"`
	Text string    `json:"text"`
}

func (r *Run) open() bool { return r.State != StateDone && r.State != StateFailed }

var (
	ErrOff         = errors.New("workflow: the workflow is off")
	ErrUnknownRun  = errors.New("workflow: no such run")
	ErrWrongState  = errors.New("workflow: the run is not at a step that takes this")
	ErrIncomplete  = errors.New("workflow: the result lacks a field the step asks for")
	ErrUnknownExit = errors.New("workflow: no such option")
)

// maxHops bounds how many steps one call may pass through without waiting.
// Check refuses uncounted loops; this is the guard for what it cannot see.
const maxHops = 64

// Engine moves the runs of one workflow. It is safe for concurrent use.
type Engine struct {
	mu      sync.Mutex
	def     *Definition
	out     Outside
	now     func() time.Time
	trigger string
	loops   map[string]map[string]bool // condition → exits that loop back

	runs   map[string]*Run
	byCase map[string]string // case → its open run
	seq    int
	turn   map[string]int // role → next index for "in turn"

	// changed collects the runs a call touched; they go to onChange once the
	// call is through, outside the lock.
	changed  map[string]bool
	onChange func([]Run)
	newID    func() string
}

// OnChange registers what is told about every run a call changed — the host
// stores them. It is called after the call, outside the engine's lock, in
// the order the runs were started.
func (e *Engine) OnChange(f func([]Run)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onChange = f
}

// Restore puts runs read back from storage into an engine that has none
// yet, after a restart. Queues and loads follow from the runs themselves.
func (e *Engine) Restore(runs []Run) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.runs) > 0 {
		return errors.New("workflow: restore into an engine that already has runs")
	}
	for _, r := range runs {
		r := r.snapshot()
		if e.def.Steps[r.Step] == nil {
			return fmt.Errorf("workflow: run %s stands at %q, which this version does not have", r.ID, r.Step)
		}
		e.runs[r.ID] = &r
		if r.open() {
			if other, dup := e.byCase[r.Case]; dup {
				return fmt.Errorf("workflow: runs %s and %s are both open for %s", other, r.ID, r.Case)
			}
			e.byCase[r.Case] = r.ID
		}
		if r.seq > e.seq {
			e.seq = r.seq
		}
	}
	return nil
}

// Seq is a run's place in the order runs were started; storage keeps it so
// a restored engine queues in the same order.
func (r Run) Seq() int { return r.seq }

// WithSeq sets the order a run read back from storage had.
func (r Run) WithSeq(seq int) Run { r.seq = seq; return r }

// unlockAndTell releases the lock and tells onChange what the call changed.
func (e *Engine) unlockAndTell() {
	var out []Run
	f := e.onChange
	if f != nil && len(e.changed) > 0 {
		for _, r := range e.ordered() {
			if e.changed[r.ID] {
				out = append(out, r.snapshot())
			}
		}
	}
	e.changed = map[string]bool{}
	e.mu.Unlock()
	if len(out) > 0 {
		f(out)
	}
}

// New refuses a definition with problems: a workflow that cannot run is not
// started half-way.
func New(def *Definition, out Outside, now func() time.Time) (*Engine, error) {
	if ps := Check(def); len(ps) > 0 {
		return nil, fmt.Errorf("workflow: %d problem(s), first: %s", len(ps), ps[0])
	}
	if now == nil {
		now = time.Now
	}
	e := &Engine{def: def, out: out, now: now, runs: map[string]*Run{}, byCase: map[string]string{}, turn: map[string]int{}, loops: map[string]map[string]bool{}, changed: map[string]bool{}, newID: uuid.NewString}
	for _, id := range def.Order {
		s := def.Steps[id]
		if s.Kind == KindTrigger {
			e.trigger = id
		}
		if s.Kind == KindCondition {
			e.loops[id] = map[string]bool{}
			for _, x := range LoopExits(def, id) {
				e.loops[id][x] = true
			}
		}
	}
	return e, nil
}

// Start begins a run for a case, or returns the open one: a case that comes
// back — a new comment, a reassignment — continues where it stands.
func (e *Engine) Start(caseRef string, vars map[string]string) (Run, error) {
	e.mu.Lock()
	defer e.unlockAndTell()
	if e.def.Mode == ModeOff {
		return Run{}, ErrOff
	}
	if id, ok := e.byCase[caseRef]; ok {
		return e.runs[id].snapshot(), nil
	}
	e.seq++
	r := &Run{
		ID:    e.newID(),
		Case:  caseRef,
		Vars:  map[string]map[string]string{e.trigger: copyMap(vars), "case": copyMap(vars)},
		Loops: map[string]int{},
		Had:   map[string]string{},
		seq:   e.seq,
	}
	e.runs[r.ID] = r
	e.byCase[caseRef] = r.ID
	e.log(r, e.trigger, "started for %s", caseRef)
	first := e.def.Steps[e.trigger].Next[ExitOut]
	if e.def.Mode == ModeWatch {
		e.place(r, first, StateWatching)
		return r.snapshot(), nil
	}
	e.enter(r, first)
	return r.snapshot(), nil
}

// Done takes a colleague's or the model's result for the step the run is at,
// and moves on. The fields the step asks for must be there: a condition
// further on reads them.
func (e *Engine) Done(runID string, result map[string]string) error {
	e.mu.Lock()
	defer e.unlockAndTell()
	r, ok := e.runs[runID]
	if !ok {
		return ErrUnknownRun
	}
	s := e.def.Steps[r.Step]
	if r.State != StateWorking || (s.Kind != KindAgent && s.Kind != KindAI) {
		return ErrWrongState
	}
	for _, f := range s.Result {
		if _, ok := result[f]; !ok {
			return fmt.Errorf("%w: %s", ErrIncomplete, f)
		}
	}
	r.Vars[s.ID] = copyMap(result)
	who := r.Assignee
	if who != "" {
		r.Had[s.ID] = who
		e.log(r, s.ID, "%s reported", who)
	} else {
		e.log(r, s.ID, "the model reported")
	}
	e.enter(r, s.Next[ExitOut])
	// Someone became free: whoever queued for them goes next.
	e.drainQueues()
	return nil
}

// StepFailed ends a run whose colleague or model step could not be done —
// the task failed, or ended without the fields the step asks for. The owner
// hears why. step must be where the run stands; a late report for a step the
// run has left is not its failure.
func (e *Engine) StepFailed(runID, step, reason string) error {
	e.mu.Lock()
	defer e.unlockAndTell()
	r, ok := e.runs[runID]
	if !ok {
		return ErrUnknownRun
	}
	if r.Step != step || r.State != StateWorking {
		return ErrWrongState
	}
	e.fail(r, step, "%s", reason)
	e.drainQueues()
	return nil
}

// Decide takes a person's answer to an approval.
func (e *Engine) Decide(runID, option string) error {
	e.mu.Lock()
	defer e.unlockAndTell()
	r, ok := e.runs[runID]
	if !ok {
		return ErrUnknownRun
	}
	s := e.def.Steps[r.Step]
	if r.State != StateWaiting || s.Kind != KindApproval {
		return ErrWrongState
	}
	to, ok := s.Next[option]
	if !ok || option == ExitTimeout {
		return fmt.Errorf("%w: %q", ErrUnknownExit, option)
	}
	r.Vars[s.ID] = map[string]string{"decision": option}
	e.log(r, s.ID, "%s decided: %s", s.Who, option)
	e.enter(r, to)
	e.drainQueues()
	return nil
}

// Event delivers something a wait step waits for, such as a label set on
// the ticket. Events nobody waits for are ignored.
func (e *Engine) Event(runID, name string) error {
	e.mu.Lock()
	defer e.unlockAndTell()
	r, ok := e.runs[runID]
	if !ok {
		return ErrUnknownRun
	}
	s := e.def.Steps[r.Step]
	if r.State != StateWaiting || s.Kind != KindWait || s.Until != name {
		return ErrWrongState
	}
	e.log(r, s.ID, "%s arrived", name)
	e.enter(r, s.Next[ExitOut])
	e.drainQueues()
	return nil
}

// Observe records where a case stands in observe mode, as read from the
// target system. Nothing is assigned and nothing is done.
func (e *Engine) Observe(runID, step string) error {
	e.mu.Lock()
	defer e.unlockAndTell()
	r, ok := e.runs[runID]
	if !ok {
		return ErrUnknownRun
	}
	if e.def.Mode != ModeWatch || e.def.Steps[step] == nil {
		return ErrWrongState
	}
	state := StateWatching
	if e.def.Steps[step].Kind == KindEnd {
		state = StateDone
		delete(e.byCase, r.Case)
	}
	e.place(r, step, state)
	return nil
}

// Tick looks at the clock: steps over their limit, waits past their
// maximum, required steps that may have found someone in the meantime.
func (e *Engine) Tick() {
	e.mu.Lock()
	defer e.unlockAndTell()
	now := e.now()
	for _, r := range e.ordered() {
		if !r.open() {
			continue
		}
		s := e.def.Steps[r.Step]
		elapsed := now.Sub(r.Since)
		switch {
		case r.State == StateWorking && s.Limit > 0 && elapsed > s.Limit && !r.Stuck:
			r.Stuck = true
			e.log(r, s.ID, "over its limit of %s", s.Limit)
			e.tell(r, s, fmt.Sprintf("%s has been at %q for longer than %s.", r.Case, s.Title, s.Limit))
		case r.State == StateWaiting && s.Kind == KindWait && s.Max > 0 && elapsed >= s.Max:
			exit := ExitTimeout
			if s.Until == "" {
				exit = ExitOut // a plain delay: its end is the way on
			}
			e.log(r, s.ID, "waited %s", s.Max)
			e.enter(r, s.Next[exit])
		case r.State == StateWaiting && s.Kind == KindApproval && s.Wait > 0 && elapsed >= s.Wait:
			e.log(r, s.ID, "no decision within %s", s.Wait)
			e.enter(r, s.Next[ExitTimeout])
		case r.Unstaffed:
			e.staff(r, s)
		case r.State == StateWaiting && s.Kind == KindAction:
			// An action that waits for its approval asks again.
			if e.act(r, s) {
				e.enter(r, s.Next[ExitOut])
			}
		}
	}
	e.drainQueues()
}

// OpenRun returns the open run of a case, if it has one.
func (e *Engine) OpenRun(caseRef string) (Run, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	id, ok := e.byCase[caseRef]
	if !ok {
		return Run{}, false
	}
	return e.runs[id].snapshot(), true
}

// LastRun returns the newest run of a case, open or ended.
func (e *Engine) LastRun(caseRef string) (Run, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var last *Run
	for _, r := range e.runs {
		if r.Case == caseRef && (last == nil || r.Since.After(last.Since)) {
			last = r
		}
	}
	if last == nil {
		return Run{}, false
	}
	return last.snapshot(), true
}

// Trigger is the definition's trigger step.
func (e *Engine) Trigger() *Step { return e.def.Steps[e.trigger] }

// Mode is what the workflow does to the cases it sees.
func (e *Engine) Mode() Mode { return e.def.Mode }

// Run returns a copy of one run.
func (e *Engine) Run(id string) (Run, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	r, ok := e.runs[id]
	if !ok {
		return Run{}, false
	}
	return r.snapshot(), true
}

// Runs returns copies of all runs, oldest first.
func (e *Engine) Runs() []Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Run
	for _, r := range e.ordered() {
		out = append(out, r.snapshot())
	}
	return out
}

// enter walks into a step and on through every step that does not wait.
func (e *Engine) enter(r *Run, id string) {
	for hops := 0; ; hops++ {
		if hops == maxHops {
			e.fail(r, id, "passed %d steps without waiting; stopped", maxHops)
			return
		}
		s := e.def.Steps[id]
		e.place(r, id, StateWorking)
		switch s.Kind {
		case KindAgent:
			if next, moved := e.staff(r, s); moved {
				id = next
				continue
			}
			return
		case KindAI:
			r.State = StateWorking
			if err := e.out.Dispatch(Task{Run: r.ID, Case: r.Case, Step: id, Text: e.render(r, s.Task), Result: s.Result}); err != nil {
				e.fail(r, id, "could not hand the step to the model: %v", err)
			}
			return
		case KindCondition:
			id = e.branch(r, s)
			if id == "" {
				return
			}
		case KindAction:
			if !e.act(r, s) {
				return
			}
			id = s.Next[ExitOut]
		case KindApproval:
			r.State = StateWaiting
			if err := e.out.Ask(Question{Run: r.ID, Step: id, To: s.Who, Text: e.render(r, s.Ask), Options: s.Options}); err != nil {
				e.fail(r, id, "could not ask %s: %v", s.Who, err)
			}
			return
		case KindWait:
			r.State = StateWaiting
			return
		case KindNotify:
			if err := e.out.Notify(Message{Run: r.ID, Step: id, To: s.To, Via: s.Via, Text: e.render(r, s.Text)}); err != nil {
				// A message that does not arrive does not hold the case up.
				e.log(r, id, "message to %s failed: %v", s.To, err)
			}
			id = s.Next[ExitOut]
		case KindEnd:
			r.State = StateDone
			delete(e.byCase, r.Case)
			e.log(r, id, "done")
			return
		default:
			e.fail(r, id, "cannot enter a %s", s.Kind)
			return
		}
	}
}

// act runs an action step. It reports whether the run moves on; a run whose
// action waits for an approval stays at the step, waiting.
func (e *Engine) act(r *Run, s *Step) bool {
	params := make(map[string]string, len(s.Params))
	for k, v := range s.Params {
		params[k] = e.render(r, v)
	}
	a := Action{Run: r.ID, Case: r.Case, Step: s.ID, As: s.As, System: s.System, Do: s.Do, Params: params, Pending: r.Vars[s.ID]["approval"]}
	err := e.out.Act(a)
	var pending *PendingError
	switch {
	case errors.As(err, &pending):
		if r.Vars[s.ID]["approval"] != pending.Approval {
			r.Vars[s.ID] = map[string]string{"approval": pending.Approval}
			e.log(r, s.ID, "%s %s waits for approval %s", s.System, s.Do, pending.Approval)
		}
		r.State = StateWaiting
		return false
	case err != nil:
		e.fail(r, s.ID, "%s %s as %s failed: %v", s.System, s.Do, s.As, err)
		return false
	}
	delete(r.Vars, s.ID)
	e.log(r, s.ID, "%s: %s as %s", s.System, s.Do, s.As)
	return true
}

// branch picks a condition's exit. A value that loops back counts a round;
// when the rounds are used up the limit exit is taken instead.
func (e *Engine) branch(r *Run, s *Step) string {
	step, field, _ := strings.Cut(s.On, ".")
	val := r.Vars[step][field]
	exit := val
	if _, ok := s.Next[exit]; !ok || exit == ExitLimit {
		exit = ExitElse
	}
	if _, ok := s.Next[exit]; !ok {
		e.fail(r, s.ID, "no exit for %s = %q", s.On, val)
		return ""
	}
	if e.loops[s.ID][exit] {
		if r.Loops[s.ID]+1 >= s.MaxRounds {
			e.log(r, s.ID, "%s = %q after %d rounds: limit", s.On, val, s.MaxRounds)
			return s.Next[ExitLimit]
		}
		r.Loops[s.ID]++
		e.log(r, s.ID, "%s = %q, round %d of %d", s.On, val, r.Loops[s.ID]+1, s.MaxRounds)
	} else {
		e.log(r, s.ID, "%s = %q", s.On, val)
	}
	return s.Next[exit]
}

// staff gives a colleague step to someone. It reports the next step when
// the step is skipped.
func (e *Engine) staff(r *Run, s *Step) (next string, moved bool) {
	candidates := e.candidates(r, s)
	if len(candidates) == 0 {
		if s.Optional {
			e.log(r, s.ID, "skipped: nobody holds %s", s.Role)
			return s.Next[ExitOut], true
		}
		if !r.Unstaffed {
			r.Unstaffed = true
			r.State = StateWaiting
			e.log(r, s.ID, "nobody holds %s", s.Role)
			e.tell(r, s, fmt.Sprintf("%s waits at %q: nobody can take the role %s.", r.Case, s.Title, s.Role))
		}
		return "", false
	}
	r.Unstaffed = false
	free := candidates[:0:0]
	for _, c := range candidates {
		if s.MaxParallel == 0 || e.load(c) < s.MaxParallel {
			free = append(free, c)
		}
	}
	if len(free) == 0 {
		if r.State != StateQueued {
			r.State = StateQueued
			r.Since = e.now()
			e.log(r, s.ID, "queued: %s at their limit", strings.Join(candidates, ", "))
		}
		return "", false
	}
	who := e.pick(s, free)
	r.State = StateWorking
	r.Assignee = who
	r.Since = e.now()
	e.log(r, s.ID, "given to %s", who)
	if err := e.out.Dispatch(Task{Run: r.ID, Case: r.Case, Step: s.ID, Assignee: who, Text: e.render(r, s.Task), Result: s.Result}); err != nil {
		e.fail(r, s.ID, "could not hand the step to %s: %v", who, err)
	}
	return "", false
}

// candidates are who may take the step for this run, before limits.
func (e *Engine) candidates(r *Run, s *Step) []string {
	excluded := ""
	if s.NotSameAs != "" {
		excluded = r.Had[s.NotSameAs]
	}
	var out []string
	for _, name := range e.def.Roles[s.Role] {
		if name != excluded && e.out.Available(name) {
			out = append(out, name)
		}
	}
	// The person who had the case before keeps it, if they still can.
	if prev := r.Had[s.ID]; s.Sticky && prev != "" {
		for _, c := range out {
			if c == prev {
				return []string{prev}
			}
		}
	}
	return out
}

func (e *Engine) pick(s *Step, free []string) string {
	if s.Pick == PickRound {
		members := e.def.Roles[s.Role]
		for i := 0; i < len(members); i++ {
			name := members[(e.turn[s.Role]+i)%len(members)]
			for _, f := range free {
				if f == name {
					e.turn[s.Role] = (e.turn[s.Role] + i + 1) % len(members)
					return name
				}
			}
		}
	}
	best := free[0]
	for _, c := range free[1:] {
		if e.load(c) < e.load(best) {
			best = c
		}
	}
	return best
}

// load is how many cases a colleague has in work in this workflow.
func (e *Engine) load(name string) int {
	n := 0
	for _, r := range e.runs {
		if r.State == StateWorking && r.Assignee == name {
			n++
		}
	}
	return n
}

// drainQueues hands queued cases out, oldest first, until nobody is free.
func (e *Engine) drainQueues() {
	queued := []*Run{}
	for _, r := range e.runs {
		if r.State == StateQueued {
			queued = append(queued, r)
		}
	}
	sort.Slice(queued, func(i, j int) bool {
		if !queued[i].Since.Equal(queued[j].Since) {
			return queued[i].Since.Before(queued[j].Since)
		}
		return queued[i].seq < queued[j].seq
	})
	for _, r := range queued {
		e.staff(r, e.def.Steps[r.Step])
	}
}

func (e *Engine) place(r *Run, id string, state State) {
	e.changed[r.ID] = true
	if r.Step != id || r.State != state {
		r.Since = e.now()
	}
	r.Step = id
	r.State = state
	r.Assignee = ""
	r.Stuck = false
	r.Unstaffed = false
	if state == StateWatching {
		e.log(r, id, "seen here")
	}
}

func (e *Engine) fail(r *Run, step, format string, args ...any) {
	r.State = StateFailed
	r.Step = step
	delete(e.byCase, r.Case)
	text := fmt.Sprintf(format, args...)
	e.log(r, step, "%s", text)
	e.tell(r, e.def.Steps[step], fmt.Sprintf("%s stopped: %s", r.Case, text))
}

// tell writes to the workflow's owner — everything it cannot resolve itself.
func (e *Engine) tell(r *Run, s *Step, text string) {
	step := ""
	if s != nil {
		step = s.ID
	}
	_ = e.out.Notify(Message{Run: r.ID, Step: step, To: e.def.Owner, Via: "chat", Text: text})
}

func (e *Engine) log(r *Run, step, format string, args ...any) {
	e.changed[r.ID] = true
	r.Log = append(r.Log, Entry{At: e.now(), Step: step, Text: fmt.Sprintf(format, args...)})
}

func (e *Engine) ordered() []*Run {
	out := make([]*Run, 0, len(e.runs))
	for _, r := range e.runs {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].seq < out[j].seq })
	return out
}

var placeholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_-]+)\.([A-Za-z0-9_-]+)\s*\}\}`)

// render fills {{step.field}} from what the steps reported. A placeholder
// with nothing behind it stays visible rather than turning into an empty
// gap in a sentence.
func (e *Engine) render(r *Run, text string) string {
	return placeholder.ReplaceAllStringFunc(text, func(m string) string {
		p := placeholder.FindStringSubmatch(m)
		if v, ok := r.Vars[p[1]][p[2]]; ok {
			return v
		}
		return m
	})
}

func (r *Run) snapshot() Run {
	c := *r
	c.Vars = map[string]map[string]string{}
	for k, v := range r.Vars {
		c.Vars[k] = copyMap(v)
	}
	c.Loops = map[string]int{}
	for k, v := range r.Loops {
		c.Loops[k] = v
	}
	c.Had = copyMap(r.Had)
	c.Log = append([]Entry(nil), r.Log...)
	return c
}

func copyMap(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
