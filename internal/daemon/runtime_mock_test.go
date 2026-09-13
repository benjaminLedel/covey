package daemon

import (
	"testing"
	"time"
)

// The budget a scripted action gets. It is stretched by the same lever the
// integration suite's own waits use, because the alternative is two screws for
// one fact — and the second one is always the one nobody sets on the slow
// machine (#252, #244).
func TestTheMockActionBudgetFollowsTheWaitFactor(t *testing.T) {
	t.Setenv("COVEY_TEST_WAIT_FACTOR", "")
	if got := mockActionTimeout(); got != mockActionBudget {
		t.Errorf("without a factor the budget stands: %s", got)
	}
	// A factor below one would SHORTEN the budget, and nothing about a slow
	// machine asks for that.
	t.Setenv("COVEY_TEST_WAIT_FACTOR", "0.5")
	if got := mockActionTimeout(); got != mockActionBudget {
		t.Errorf("a factor below one must not shorten anything: %s", got)
	}
	t.Setenv("COVEY_TEST_WAIT_FACTOR", "not a number")
	if got := mockActionTimeout(); got != mockActionBudget {
		t.Errorf("nonsense leaves the budget standing: %s", got)
	}
	t.Setenv("COVEY_TEST_WAIT_FACTOR", "3")
	if got, want := mockActionTimeout(), 90*time.Second; got != want {
		t.Errorf("timeout = %s, expected %s", got, want)
	}
}
