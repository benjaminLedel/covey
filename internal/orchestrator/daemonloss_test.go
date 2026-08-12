package orchestrator

import (
	"strings"
	"testing"
)

// TestGivesUpAfterDaemonLoss pins the boundary of the retry cap, and with it
// the off-by-one that is easy to get wrong here: the count handed in is the one
// the task carried when this run CLAIMED it, so it does not yet include the
// loss currently being handled.
//
// Written out value by value rather than as `previous >= max-1`, because a
// condition copied from the production code would follow every change of it
// silently — which is exactly what a boundary test must not do.
func TestGivesUpAfterDaemonLoss(t *testing.T) {
	// With maxDaemonLossRetries = 5: four requeues, the fifth loss is terminal.
	want := map[int]bool{
		0: false, // first loss ever — a blip, requeue
		1: false,
		2: false,
		3: false, // the fourth loss, still requeued
		4: true,  // the fifth in a row — give up
		5: true,  // beyond it (a task that got here some other way) stays given up
	}
	if maxDaemonLossRetries != 5 {
		t.Fatalf("this table is written for maxDaemonLossRetries = 5, it is %d — "+
			"adjust the expectations deliberately instead of the assertion", maxDaemonLossRetries)
	}
	for previous, wantGiveUp := range want {
		giveUp, why := givesUpAfterDaemonLoss(previous)
		if giveUp != wantGiveUp {
			t.Errorf("after %d earlier losses: giveUp=%v, want %v", previous, giveUp, wantGiveUp)
		}
		if giveUp && why == "" {
			t.Errorf("after %d earlier losses: giving up without a reason for the backlog", previous)
		}
		if !giveUp && why != "" {
			t.Errorf("after %d earlier losses: requeueing must not carry a failure text (%q)", previous, why)
		}
	}
}

// TestGivesUpAfterDaemonLossNamesTheCause: the whole point of telling a lost
// connection apart from a real agent error is that a human looking at the
// failed task can see which of the two happened. If the text stops saying so,
// the distinction exists only in the code.
func TestGivesUpAfterDaemonLossNamesTheCause(t *testing.T) {
	_, why := givesUpAfterDaemonLoss(maxDaemonLossRetries - 1)
	for _, want := range []string{"sandbox connection", "5 times in a row"} {
		if !strings.Contains(why, want) {
			t.Errorf("the failure text %q does not mention %q", why, want)
		}
	}
}
