package daemon

import (
	"context"
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

// The mock builds its proxy URL from the task env and the task body (#554):
// a port that is not a number, or a path that is not <system>/<action>, never
// becomes part of a URL.
func TestCallActionProxyRefusesWhatIsNotAPortOrAnAction(t *testing.T) {
	ctx := context.Background()
	for _, c := range []struct{ port, path string }{
		{"1@example.org", "gitlab/comment"},
		{"0", "gitlab/comment"},
		{"70000", "gitlab/comment"},
		{"8080", "../admin"},
		{"8080", "gitlab/comment?x=1"},
		{"8080", "gitlab"},
	} {
		if _, _, err := callActionProxy(ctx, c.port, c.path, "{}"); err == nil {
			t.Errorf("port %q path %q: expected a refusal", c.port, c.path)
		}
	}
}
