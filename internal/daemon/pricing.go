package daemon

import "strings"

// Pricing: turning token counts into a comparable figure where the engine
// reports none.
//
// Claude Code computes a dollar amount itself and Covey books it unchanged.
// Codex reports token counts and leaves the pricing to the caller (spec/19), so
// "book what the engine reports" only holds for engines that report something.
// What is needed for the rest is a lookup table, not a second cost model: it
// produces the same LIST-PRICE EQUIVALENT one level further in.
//
// What that figure means is stated once and then carried in the labels: on a
// metered credential it is what was billed, on a subscription seat it is what
// the same work WOULD have cost through the API. Nobody pays the latter; the
// seat costs what the seat costs (spec/17, spec/18).
//
// A price list ages, and a WRONG one is worse than an absent one because it
// looks like a measurement. Hence two rules, both enforced below:
//
//   - it belongs to the engine plugin, versioned with the binary, not to the
//     database where it would drift out of sight;
//   - a model the list does not know yields NO figure rather than a guessed
//     one. A run whose price is unknown is honest; a run priced at zero is a
//     lie in the direction nobody checks.

// ModelPrice is the price of one model in US dollars per MILLION tokens, split
// the way the tokens are counted — the three kinds cost markedly different
// amounts, and averaging them would misprice exactly the runs that matter (a
// caching runtime reads far more from the cache than fresh).
type ModelPrice struct {
	Input         float64
	Output        float64
	CacheRead     float64
	CacheCreation float64
}

// Cost prices a run. ok=false means the model is unknown — the caller then
// books no amount rather than a made-up one.
func (p ModelPrice) Cost(input, output, cacheRead, cacheCreation int64) float64 {
	const perMillion = 1_000_000.0
	return (float64(input)*p.Input +
		float64(output)*p.Output +
		float64(cacheRead)*p.CacheRead +
		float64(cacheCreation)*p.CacheCreation) / perMillion
}

// PriceList maps a model name to its price. Keys are matched by PREFIX, because
// providers append dated suffixes to model ids (`gpt-5.3-codex-2026-04-01`) and
// a table keyed on exact names would go blind on the day a snapshot is
// published — silently, which is the property to avoid.
type PriceList map[string]ModelPrice

// Price looks a model up. Longest matching prefix wins, so a more specific
// entry beats a general one.
func (l PriceList) Price(model string) (ModelPrice, bool) {
	model = strings.TrimSpace(model)
	if model == "" || len(l) == 0 {
		return ModelPrice{}, false
	}
	var (
		best    ModelPrice
		bestLen = -1
	)
	for prefix, p := range l {
		if strings.HasPrefix(model, prefix) && len(prefix) > bestLen {
			best, bestLen = p, len(prefix)
		}
	}
	return best, bestLen >= 0
}

// Priced is the optional engine capability: an engine that does not report a
// dollar figure supplies a price list instead, and the daemon computes one from
// the token counts it does report.
//
// Optional on purpose. An engine that reports its own amount (Claude Code) must
// NOT implement this — two sources for one number would drift, and the one that
// comes from the engine is closer to the truth.
type Priced interface {
	Prices() PriceList
}

// PriceRun computes the cost of a run from its tokens, if the runtime brings a
// price list and knows the model. Returns ok=false otherwise; the caller then
// keeps whatever the engine itself reported (possibly nothing).
func PriceRun(rt Runtime, model string, input, output, cacheRead, cacheCreation int64) (float64, bool) {
	p, ok := rt.(Priced)
	if !ok {
		return 0, false
	}
	price, ok := p.Prices().Price(model)
	if !ok {
		return 0, false
	}
	return price.Cost(input, output, cacheRead, cacheCreation), true
}
