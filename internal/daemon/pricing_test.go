package daemon

import "testing"

func TestPriceListMatchesByLongestPrefix(t *testing.T) {
	// Providers append dated suffixes to model ids. A table keyed on exact
	// names would go blind the day a snapshot is published — and silently,
	// which is the property worth testing against.
	l := PriceList{
		"gpt-5.3":       {Input: 1, Output: 2},
		"gpt-5.3-codex": {Input: 10, Output: 20},
	}
	got, ok := l.Price("gpt-5.3-codex-2026-04-01")
	if !ok || got.Input != 10 {
		t.Fatalf("the more specific entry has to win: %+v, %v", got, ok)
	}
	if got, ok := l.Price("gpt-5.3-2026-01-01"); !ok || got.Input != 1 {
		t.Fatalf("the general entry applies to the rest: %+v, %v", got, ok)
	}
}

func TestPriceListRefusesTheUnknown(t *testing.T) {
	// The load-bearing rule: an unknown model yields NO figure, never zero. A
	// run whose price is unknown is honest; a run priced at zero is a lie in
	// the direction nobody checks — it makes an agent look free.
	l := PriceList{"gpt-5.3": {Input: 1}}
	if _, ok := l.Price("claude-opus-5"); ok {
		t.Fatal("an unknown model must not be priced")
	}
	if _, ok := l.Price(""); ok {
		t.Fatal("an empty model name must not be priced")
	}
	if _, ok := (PriceList{}).Price("gpt-5.3"); ok {
		t.Fatal("an empty list prices nothing")
	}
}

func TestModelPriceSplitsTheTokenKinds(t *testing.T) {
	// The three kinds cost markedly different amounts, and a caching runtime
	// reads far more from the cache than fresh. Averaging them would misprice
	// exactly the runs that matter.
	p := ModelPrice{Input: 3, Output: 15, CacheRead: 0.3, CacheCreation: 3.75}
	got := p.Cost(1_000_000, 1_000_000, 1_000_000, 1_000_000)
	if want := 3 + 15 + 0.3 + 3.75; got != want {
		t.Fatalf("cost = %v, expected %v", got, want)
	}
	if got := p.Cost(0, 0, 2_000_000, 0); got != 0.6 {
		t.Fatalf("a pure cache read: %v, expected 0.6", got)
	}
}

// TestPriceRunOnlyWhereDeclared: an engine that reports its own amount must not
// be priced a second time. Two sources for one number drift, and the engine's
// is closer to the truth.
func TestPriceRunOnlyWhereDeclared(t *testing.T) {
	if _, ok := PriceRun(Mock{}, "mock-model", 1, 1, 1, 1); ok {
		t.Fatal("an engine without a price list must not be priced")
	}
}
