package orchestrator

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A wake that keeps failing has to be asked less often, not more politely.
//
// On covey.work an agent whose home could not be materialised was woken every
// thirty seconds for six and a half hours — about 900 attempts, each holding a
// runner slot and writing four lines of recording, none of them able to end
// differently (#139). The cause of a failed wake does not change while one
// waits thirty seconds.
func TestWakeBackoffGrowsAndStops(t *testing.T) {
	// The first retry stays quick: a host that was briefly away is the common
	// case and deserves the fast path.
	if got := wakeBackoff(1); got != 30*time.Second {
		t.Errorf("first retry after %v, expected 30s", got)
	}
	// It grows.
	if wakeBackoff(3) <= wakeBackoff(2) || wakeBackoff(5) <= wakeBackoff(4) {
		t.Error("the wait does not grow with the number of failures")
	}
	// And it stops growing — a broken agent must not become invisible for
	// hours; half an hour is short enough that a repair is noticed.
	if got := wakeBackoff(50); got != 30*time.Minute {
		t.Errorf("the wait grew past the cap: %v", got)
	}
}

// The record of it is what the interface reads: how often, why, until when.
func TestWakeFailureIsRememberedAndCleared(t *testing.T) {
	o := &Orchestrator{}
	id := uuid.New()

	if _, blocked := o.WakeBlocked(id); blocked {
		t.Fatal("an agent nobody failed on must not be blocked")
	}
	o.noteWakeFailure(id, errors.New("materialising the home failed"))
	t1, blocked := o.WakeBlocked(id)
	if !blocked || t1.Failures != 1 || t1.Err == "" {
		t.Fatalf("the first failure was not recorded: %+v", t1)
	}
	o.noteWakeFailure(id, errors.New("materialising the home failed"))
	t2, _ := o.WakeBlocked(id)
	if t2.Failures != 2 || !t2.Until.After(t1.Until) {
		t.Errorf("the second failure did not extend the wait: %+v", t2)
	}
	if len(o.WakeTroubles()) != 1 {
		t.Error("the list the interface reads does not name the agent")
	}

	// A wake that worked — and a person pressing the button — end it.
	o.clearWakeFailure(id)
	if _, blocked := o.WakeBlocked(id); blocked {
		t.Error("the trouble outlived its cause")
	}
	if len(o.WakeTroubles()) != 0 {
		t.Error("the interface still shows a problem that is over")
	}
}
