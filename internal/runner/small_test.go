package runner

import (
	"testing"
)

// A batch the link could not carry goes back to the front of the ring, in
// order, and is counted as dropped only when the ring has no room for it.
func TestUndeliveredLogLinesGoBackIntoTheRing(t *testing.T) {
	r := newLogRing(0)
	for _, m := range []string{"a", "b", "c"} {
		r.add(LogEntry{Msg: m})
	}
	taken, _ := r.take(2) // a, b — and then the send fails
	r.add(LogEntry{Msg: "d"})
	r.putBack(taken, 0)
	got, dropped := r.take(10)
	if dropped != 0 || len(got) != 4 || got[0].Msg != "a" || got[1].Msg != "b" || got[2].Msg != "c" || got[3].Msg != "d" {
		t.Fatalf("order after put-back: %+v (dropped %d)", got, dropped)
	}

	// No room: the oldest of the returned lines are what gives, and they are
	// counted.
	full := newLogRing(0)
	for i := 0; i < logRingCap; i++ {
		full.add(LogEntry{Msg: "x"})
	}
	full.putBack([]LogEntry{{Msg: "old1"}, {Msg: "old2"}}, 0)
	_, dropped = full.take(1)
	if dropped != 2 {
		t.Fatalf("two lines had no room and have to be counted, got %d", dropped)
	}
}
