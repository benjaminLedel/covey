package orchestrator

import (
	"testing"
	"time"

	"covey/internal/runtimes"
)

// TestRejectionCooldown pins the hard signal: which error text parks a value,
// and for how long.
//
// The distinction is not cosmetic. A rate limit passes by itself, so an hour is
// the right order of magnitude; a revoked token does not recover, and trying it
// again every hour means an agent runs into the same wall all day. Mixing the
// two up costs either a day of standstill or a day of failed runs.
//
// The texts are the ones the runtime actually passes through — the adapter
// (spec/12) recognises the same phrases in order to explain them; here they
// decide which value is out of play.
func TestRejectionCooldown(t *testing.T) {
	cases := []struct {
		name   string
		text   string
		want   time.Duration
		reason string
	}{
		{"revoked subscription token", `The stored subscription token is rejected ("Invalid bearer token")`, cooldownRejected, runtimes.ReasonError},
		{"expired token", `claude exit: OAuth token has expired`, cooldownRejected, runtimes.ReasonError},
		{"authentication error", `{"type":"authentication_error","message":"invalid x-api-key"}`, cooldownRejected, runtimes.ReasonError},
		{"rate limit, snake case", `{"type":"rate_limit_error"}`, cooldownRateLimit, runtimes.ReasonLimit},
		{"rate limit, prose", `Rate limit exceeded — try again later`, cooldownRateLimit, runtimes.ReasonLimit},
		{"rate limit, status code", `API error 429`, cooldownRateLimit, runtimes.ReasonLimit},

		// The subscription case, taken verbatim from a failed run on the live
		// instance. It carries no "rate limit" anywhere, which is exactly why
		// the first version of the rule missed it — and a fleet on seats hits
		// this far more often than anything else here.
		{"session limit", "You've hit your session limit · resets 4:10pm (UTC)", cooldownSeatWindow, runtimes.ReasonLimit},
		{"session limit, ascii apostrophe", "You've hit your session limit", cooldownSeatWindow, runtimes.ReasonLimit},
		{"weekly limit", "You've hit your weekly limit · resets Aug 9 at 6:59am", cooldownSeatWindow, runtimes.ReasonLimit},
		{"model limit", "You've hit your Opus limit", cooldownSeatWindow, runtimes.ReasonLimit},
		// Casing is the provider's business, not ours.
		{"lower case rate limit", "the request hit a rate limit", cooldownRateLimit, runtimes.ReasonLimit},

		// Everything else is NOT a credential problem. A run that failed on the
		// task must not park a working token — that would take a value out of
		// the pool for a fault it had nothing to do with.
		{"turn limit", `turn limit reached (40 turns) — run cut off before it produced a result`, 0, ""},
		{"missing credential", `Claude Code has no credential in the sandbox ("Not logged in · Please run /login")`, 0, ""},
		{"plain failure", `claude exit: signal: killed`, 0, ""},
		{"empty", "", 0, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, reason := rejectionCooldown(c.text)
			if got != c.want {
				t.Fatalf("cooldown %v, expected %v", got, c.want)
			}
			if c.want == 0 && reason != "" {
				t.Fatalf("no cooldown, but reason %q", reason)
			}
			// Der Grund unterscheidet Normalbetrieb von Stoerung: ein
			// aufgebrauchtes Fenster ist erwartbar, ein abgewiesenes Token
			// muss sich jemand ansehen.
			if c.want != 0 && reason != c.reason {
				t.Fatalf("reason %q, expected %q", reason, c.reason)
			}
		})
	}
}

// TestRejectionCooldownOrder: a text carrying BOTH signals has to be read as
// the more serious one. A revoked token that is additionally reported as rate
// limited must not come back after an hour.
func TestRejectionCooldownOrder(t *testing.T) {
	got, _ := rejectionCooldown(`429: Invalid bearer token`)
	if got != cooldownRejected {
		t.Fatalf("cooldown %v, expected the longer one (%v)", got, cooldownRejected)
	}
}
