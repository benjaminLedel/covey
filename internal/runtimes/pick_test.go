package runtimes

import (
	"strings"
	"testing"

	"covey/internal/daemon"
)

func quota(ord int) Credential {
	return Credential{Ord: ord, Kind: daemon.CredSubscription}
}
func metered(ord int) Credential {
	return Credential{Ord: ord, Kind: daemon.CredAPIKey}
}

// TestBestOfPrefersPaidForCapacity is the merit order, and it is the rule that
// costs real money when it is wrong. A subscription seat is paid for whether or
// not it is used; every metered token is billed. So the seat goes first, and
// the metered credential covers only what is left over.
func TestBestOfPrefersPaidForCapacity(t *testing.T) {
	// Written in the "wrong" order on purpose: the decision must follow the
	// KIND, not the position in the slice.
	healthy := []Credential{metered(0), quota(1)}
	if got := bestOf(healthy, map[int]float64{}, map[int]int{}); got.Ord != 1 {
		t.Fatalf("the paid-for seat has to win, got ord %d", got.Ord)
	}
	// Only when no quota is left does the metered one come into play.
	if got := bestOf([]Credential{metered(0)}, map[int]float64{}, map[int]int{}); got.Ord != 0 {
		t.Fatalf("without quota the metered credential applies, got ord %d", got.Ord)
	}
}

// TestBestOfSpreadsAcrossLikeCapacity is the counter-rule, and dropping it was
// a real bug earlier in this design: three subscription seats are
// interchangeable and each has its OWN window. Stacking agents onto the first
// would let it hit its limit while the other two idle — and every agent that
// then dodges loses its prompt cache.
func TestBestOfSpreadsAcrossLikeCapacity(t *testing.T) {
	healthy := []Credential{quota(0), quota(1), quota(2)}
	seats := map[int]int{0: 2, 1: 0, 2: 1}
	if got := bestOf(healthy, map[int]float64{}, seats); got.Ord != 1 {
		t.Fatalf("the least occupied seat has to win, got ord %d", got.Ord)
	}
	// Load beats head count: a seat two agents share but that has consumed
	// nothing is better than an empty one already at 80% of its limit.
	load := map[int]float64{0: 0.1, 1: 0.8, 2: 0.2}
	if got := bestOf(healthy, load, seats); got.Ord != 0 {
		t.Fatalf("the least loaded seat has to win, got ord %d", got.Ord)
	}
}

// TestBestOfIsDeterministic: with nothing to tell two credentials apart the
// lower ord wins. Without that tie-break the choice would depend on map
// iteration order, and an agent would silently change seats between two runs —
// throwing away its cache for no reason anybody could name.
func TestBestOfIsDeterministic(t *testing.T) {
	healthy := []Credential{quota(2), quota(0), quota(1)}
	for i := 0; i < 20; i++ {
		if got := bestOf(healthy, map[int]float64{}, map[int]int{}); got.Ord != 0 {
			t.Fatalf("run %d picked ord %d — the choice is not stable", i, got.Ord)
		}
	}
}

// TestBestOfNeverMixesTheClasses: a metered credential must not win merely for
// being emptier. It is emptier because nobody is paying for it yet.
func TestBestOfNeverMixesTheClasses(t *testing.T) {
	healthy := []Credential{quota(0), metered(1)}
	load := map[int]float64{0: 0.95, 1: 0.0}
	if got := bestOf(healthy, load, map[int]int{}); got.Ord != 0 {
		t.Fatalf("a nearly full seat still beats a metered credential, got ord %d", got.Ord)
	}
}

func TestLimitActive(t *testing.T) {
	// Both halves have to be present: an amount without a window is not a
	// limit, and a window without an amount is not one either.
	for _, c := range []struct {
		l    Limit
		want bool
	}{
		{Limit{Amount: 10, Unit: "usd", WindowSecs: 3600}, true},
		{Limit{Amount: 10, Unit: "usd"}, false},
		{Limit{Unit: "usd", WindowSecs: 3600}, false},
		{Limit{}, false},
	} {
		if got := c.l.Active(); got != c.want {
			t.Fatalf("%+v.Active() = %v, expected %v", c.l, got, c.want)
		}
	}
}

// TestExhaustedCarriesTheMoment: the refusal has to say WHEN capacity frees up,
// because that is what turns a rate limit into a delay instead of a failed
// task — the control plane postpones the wake rather than burning a run.
func TestExhaustedCarriesTheMoment(t *testing.T) {
	e := &Exhausted{Runtime: "Claude team"}
	if !e.Is(ErrExhausted) {
		t.Fatal("the refusal has to be recognisable as ErrExhausted")
	}
	if got := e.Error(); !strings.Contains(got, "Claude team") {
		t.Fatalf("the message has to name the runtime: %q", got)
	}
}
