package daemon

import "testing"

// Every runtime says its own name, and that name is the key the registry and
// the agent's `runtime` field are matched on. A Name() that disagreed with the
// descriptor would mean an agent assigned to one engine running on another.
func TestEveryRuntimeNamesItselfAsItIsRegistered(t *testing.T) {
	for _, d := range Runtimes() {
		if d.New == nil {
			continue // metadata for the control plane; nothing runs it here
		}
		got := d.New().Name()
		if got != d.Name {
			t.Errorf("the runtime registered as %q calls itself %q", d.Name, got)
		}
	}
}

// Priced is optional on purpose: an engine that reports its own dollar amount
// must NOT implement it, because two sources for one number would drift and the
// engine's is the closer one. Claude Code is that engine.
func TestAnEngineThatReportsItsOwnAmountDeclaresNoPrices(t *testing.T) {
	d, ok := Describe("claude-code")
	if !ok || d.New == nil {
		t.Skip("claude-code is not registered in this binary")
	}
	if _, priced := d.New().(Priced); priced {
		t.Error("claude-code implements Priced — it reports its own amount, and two sources would drift")
	}
}

// An engine in front of a gateway does not know what a run is billed. It says
// so with an empty list rather than inheriting the harness's prices, which
// would cost a run against a provider that never saw it.
func TestGatewayEnginesInheritNoPrices(t *testing.T) {
	for _, name := range []string{"educa-ai", "sevencode", "codex"} {
		d, ok := Describe(name)
		if !ok || d.New == nil {
			continue
		}
		p, priced := d.New().(Priced)
		if !priced {
			continue
		}
		if len(p.Prices()) != 0 {
			t.Errorf("%s brings a price list of its own: %v", name, p.Prices())
		}
		// And PriceRun therefore answers "do not know" rather than zero — a
		// zero would read as a free run.
		if _, ok := PriceRun(d.New(), "irgendein-modell", 1000, 100, 0, 0); ok {
			t.Errorf("%s priced a run it cannot price", name)
		}
	}
}
