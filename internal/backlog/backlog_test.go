package backlog

import (
	"strings"
	"testing"
)

// The state machine of a task is the core of the backlog — and until now it was
// only checked indirectly: the end-to-end run in internal/integration happened
// to pass through a few transitions, but pinned down not a single one of them.
// Whoever rebuilt validTransitions got no word about it from the tests.
//
// This table is therefore deliberately written in a DIFFERENT shape than the
// production code (a map[string][]string there, a grid here). A copy of the same
// data structure would silently follow every change; a grid has to be read and
// deliberately rearranged. As a side effect, one sees the machine in one piece
// for the first time.
//
// Row = source state, column = target state, x = allowed.
const transitionGrid = `
              open in_progress blocked done failed cancelled
open           .        x         .      .     .        x
in_progress    x        .         x      x     x        x
blocked        x        .         .      .     .        x
done           .        .         .      .     .        .
failed         x        .         .      .     .        .
cancelled      x        .         .      .     .        .
`

// parseGrid turns the grid into the set of allowed pairs.
func parseGrid(t *testing.T) (columns []string, allowed map[[2]string]bool) {
	t.Helper()
	lines := []string{}
	for _, l := range strings.Split(strings.TrimSpace(transitionGrid), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	columns = strings.Fields(lines[0])
	allowed = map[[2]string]bool{}
	for _, l := range lines[1:] {
		f := strings.Fields(l)
		if len(f) != len(columns)+1 {
			t.Fatalf("grid row %q has %d fields, expected %d", l, len(f), len(columns)+1)
		}
		from := f[0]
		for i, cell := range f[1:] {
			if cell == "x" {
				allowed[[2]string{from, columns[i]}] = true
			}
		}
	}
	return columns, allowed
}

// TestTransitionMatrix checks EVERY combination — the forbidden ones too. A
// test that only walks the allowed paths does not notice when another one is
// added by accident.
func TestTransitionMatrix(t *testing.T) {
	columns, allowed := parseGrid(t)

	// The grid must know all the states the code knows — otherwise it checks a
	// section and claims to be the whole machine.
	all := []string{StateOpen, StateInProgress, StateBlocked, StateDone, StateFailed, StateCancelled}
	if len(columns) != len(all) {
		t.Fatalf("the grid knows %d states, the code %d", len(columns), len(all))
	}
	for i, s := range all {
		if columns[i] != s {
			t.Fatalf("column %d is %q, expected %q", i, columns[i], s)
		}
	}

	for _, from := range all {
		for _, to := range all {
			want := allowed[[2]string{from, to}]
			if got := transitionAllowed(from, to); got != want {
				t.Errorf("%s → %s: transitionAllowed=%v, per grid %v", from, to, got, want)
			}
		}
	}
}

// The properties behind the table — they survive even a deliberate rearranging
// of the grid and say WHY the machine looks the way it does.
func TestTransitionProperties(t *testing.T) {
	// "done" is the only dead end: what is completed is not reopened. What
	// failed (retry) or was discarded (resumption), on the other hand, is.
	for _, to := range []string{StateOpen, StateInProgress, StateBlocked, StateFailed, StateCancelled} {
		if transitionAllowed(StateDone, to) {
			t.Errorf("no way may lead out of done, found: done → %s", to)
		}
	}
	for _, from := range []string{StateFailed, StateCancelled} {
		if !transitionAllowed(from, StateOpen) {
			t.Errorf("%s must be openable again (retry resp. resumption)", from)
		}
	}

	// A run goes into progress ONLY out of the backlog: open is the only
	// predecessor of in_progress. Otherwise a second run could tear a waiting
	// task away.
	for _, from := range []string{StateInProgress, StateBlocked, StateDone, StateFailed, StateCancelled} {
		if transitionAllowed(from, StateInProgress) {
			t.Errorf("only open may lead to in_progress, found: %s → in_progress", from)
		}
	}

	// Cancelling works from every non-terminal state — the kill switch must not
	// run into a wall anywhere.
	for _, from := range []string{StateOpen, StateInProgress, StateBlocked} {
		if !transitionAllowed(from, StateCancelled) {
			t.Errorf("it must be possible to cancel out of %s", from)
		}
	}

	// No state leads to itself: a transition is a change.
	for _, s := range []string{StateOpen, StateInProgress, StateBlocked, StateDone, StateFailed, StateCancelled} {
		if transitionAllowed(s, s) {
			t.Errorf("%s → %s (onto itself) must not be allowed", s, s)
		}
	}
}

// Unknown states are not a special case, they are simply not allowed —
// fail-closed. Should a value the code does not know ever come out of the
// database, every transition would be barred instead of every one allowed.
func TestUnknownStates(t *testing.T) {
	for _, pair := range [][2]string{
		{"nonsense", StateOpen},
		{StateOpen, "nonsense"},
		{"", StateOpen},
		{StateOpen, ""},
		{"OPEN", StateInProgress}, // capitalization counts
	} {
		if transitionAllowed(pair[0], pair[1]) {
			t.Errorf("%q → %q must not be allowed", pair[0], pair[1])
		}
	}
}

func TestTerminalState(t *testing.T) {
	terminal := map[string]bool{
		StateOpen: false, StateInProgress: false, StateBlocked: false,
		StateDone: true, StateFailed: true, StateCancelled: true,
	}
	for s, want := range terminal {
		if got := terminalState(s); got != want {
			t.Errorf("terminalState(%q)=%v, expected %v", s, got, want)
		}
	}
	if terminalState("nonsense") {
		t.Error("an unknown state is not terminal")
	}
}

// Every terminal state has a column on the board, otherwise a completed task
// would stay lying where it last stood (see syncStage).
func TestTerminalStatesHaveAStage(t *testing.T) {
	for _, s := range []string{StateDone, StateFailed, StateCancelled} {
		if stateStage[s] == "" {
			t.Errorf("terminal state %q has no target column in stateStage", s)
		}
	}
}
