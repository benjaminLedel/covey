package daemon

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Reported utilisation.
//
// A limit is only worth as much as the measurement behind it, and there are
// three sources of decreasing quality (spec/18): what the ENGINE reports, what
// the platform ESTIMATES from what it booked, and what the provider REVEALS by
// rejecting a credential. This file is the first one.
//
// Claude Code answers `/usage` headless and without a model turn, with the
// provider's own figures: the share of the current rolling window and of the
// week. That beats anything inferred from our own bookkeeping — but it comes
// with two properties that have to be respected rather than wished away, and
// both are handled here:
//
//   - the answer is PROSE, not structured data, and the format will change
//     between engine versions. So parsing is fail-open: an answer that no
//     longer matches leaves the fleet running on the platform's own estimate,
//     it never blocks;
//   - the endpoint behind it has a RATE LIMIT of its own. Asking once per run
//     across a fleet would hit it, and three agents on one seat get the same
//     answer anyway — so the control plane asks centrally per credential and
//     caches (see orchestrator).

// Usage is what an engine reports about one credential.
type Usage struct {
	// WindowPercent/WeekPercent are shares of the plan limits, 0..100.
	// Negative means: not reported.
	WindowPercent float64 `json:"window_percent"`
	WeekPercent   float64 `json:"week_percent"`
	// WindowResets/WeekResets are the provider's own reset times, if given.
	WindowResets string `json:"window_resets,omitempty"`
	WeekResets   string `json:"week_resets,omitempty"`
	// Stale marks a figure the engine served from its own cache because the
	// provider's endpoint was rate limited. Reporting it as fresh would be
	// worse than having no figure — a limit would be compared against an hour
	// old number without anybody knowing.
	Stale bool `json:"stale"`
	// Fetched is when we asked.
	Fetched time.Time `json:"fetched"`
}

// Reported says whether anything usable came back.
func (u Usage) Reported() bool { return u.WindowPercent >= 0 || u.WeekPercent >= 0 }

// UsageReporter is the optional engine capability. An engine that cannot ask
// its provider simply does not implement it, and the platform falls back to its
// own estimate — which for at least one engine (Codex, spec/19) is the only
// source there is. That is precisely why the fallback exists: a capability only
// one engine implements is a special case, not a capability.
type UsageReporter interface {
	// Usage asks the provider about the credential currently in effect. The
	// credential arrives the same way it does for a run, so this runs where the
	// credential is: in the sandbox, via the daemon.
	Usage(ctx context.Context, env []string) (Usage, error)
}

// Parsing `/usage`. Deliberately tolerant: named percentages are picked out of
// the prose, everything else is ignored. What is not found stays unreported
// rather than becoming zero — zero would read as "plenty left", which is the
// wrong direction to be wrong in.
var (
	reSession = regexp.MustCompile(`(?i)current session:\s*([0-9.]+)%\s*used(?:\s*·\s*resets\s*([^\n(]+))?`)
	reWeek    = regexp.MustCompile(`(?i)current week\s*\(all models\):\s*([0-9.]+)%\s*used(?:\s*·\s*resets\s*([^\n(]+))?`)
	reStale   = regexp.MustCompile(`(?i)showing last-known usage`)
)

// ParseUsage reads the figures out of an engine's `/usage` answer.
//
// Exported for the tests, and because the format is exactly the brittle part:
// whoever changes it should be able to see it fail in a unit test rather than
// in the fleet.
func ParseUsage(text string) Usage {
	u := Usage{WindowPercent: -1, WeekPercent: -1, Fetched: time.Now()}
	if m := reSession.FindStringSubmatch(text); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			u.WindowPercent = v
			u.WindowResets = strings.TrimSpace(m[2])
		}
	}
	if m := reWeek.FindStringSubmatch(text); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			u.WeekPercent = v
			u.WeekResets = strings.TrimSpace(m[2])
		}
	}
	u.Stale = reStale.MatchString(text)
	return u
}
