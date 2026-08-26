package httpapi

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// slowChecker is a data plane that takes its time — a stand-in for a host whose
// read loop is inside a start, which is when this check really does take
// minutes.
type slowChecker struct {
	took  time.Duration
	calls atomic.Int32
	round atomic.Int32
}

func (c *slowChecker) Check(ctx context.Context) []string {
	c.calls.Add(1)
	select {
	case <-time.After(c.took):
	case <-ctx.Done():
	}
	if c.round.Load() == 0 {
		return nil
	}
	return []string{"the image is missing"}
}

// The first answer is fetched while somebody waits — there is nothing to show
// yet, and "everything fine" would be a false all-clear. Every later one is
// fetched beside the request: the page gets what was true a moment ago, at
// once, and a check that takes minutes no longer holds up the view that is
// polled — nor, through the lock, every other view that asks the same
// question.
func TestTheDataPlaneCheckIsRefreshedBesideTheRequestNotInsideIt(t *testing.T) {
	checker := &slowChecker{}
	s := &Server{DataPlane: checker}

	if problems := s.dataPlaneProblems(context.Background()); len(problems) != 0 {
		t.Fatalf("the first check answers for itself: %v", problems)
	}
	if checker.calls.Load() != 1 {
		t.Fatalf("asked %d times instead of once", checker.calls.Load())
	}

	// Within the half minute nobody is asked again.
	if problems := s.dataPlaneProblems(context.Background()); len(problems) != 0 || checker.calls.Load() != 1 {
		t.Fatalf("a fresh answer is not fetched twice: %v, %d calls", problems, checker.calls.Load())
	}

	// Now it is stale, and the host has become slow and has something to say.
	checker.took = 2 * time.Second
	checker.round.Store(1)
	s.dataPlane.mu.Lock()
	s.dataPlane.at = time.Now().Add(-2 * dataPlaneCheckTTL)
	s.dataPlane.mu.Unlock()

	start := time.Now()
	problems := s.dataPlaneProblems(context.Background())
	if took := time.Since(start); took > time.Second {
		t.Errorf("the request waited for the check: %s", took)
	}
	if len(problems) != 0 {
		t.Errorf("until the new answer is there, the old one stands: %v", problems)
	}
	// And while it runs, no second one is started.
	waitFor(t, "the refresh to start", func() bool { return checker.calls.Load() == 2 })
	s.dataPlaneProblems(context.Background())
	if checker.calls.Load() != 2 {
		t.Errorf("a running check is not started again: %d calls", checker.calls.Load())
	}

	// It arrives on its own, and the next look has it.
	waitFor(t, "the answer fetched in the background to become visible", func() bool {
		return len(s.dataPlaneProblems(context.Background())) == 1
	})
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("waited in vain for %s", what)
}
