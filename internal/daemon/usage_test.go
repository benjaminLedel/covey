package daemon

import "testing"

// The real answer of `claude -p "/usage"`, captured from the binary. It is
// pinned here verbatim because the parser's whole risk is that this text
// changes — and then it should fail HERE and not in a fleet that quietly stops
// measuring.
const realUsageOutput = `You are currently using your subscription to power your Claude Code usage

Current session: 6% used · resets Aug 7 at 1:20pm (Europe/Berlin)
Current week (all models): 87% used · resets Aug 9 at 6:59am (Europe/Berlin)
Current week (Fable): 0% used

What's contributing to your limits usage?
Approximate, based on local sessions on this machine — does not include other devices or claude.ai.

Last 24h · 965 requests · 9 sessions
  73% of your usage was at >150k context
`

func TestParseUsageReadsTheRealAnswer(t *testing.T) {
	u := ParseUsage(realUsageOutput)
	if u.WindowPercent != 6 {
		t.Fatalf("session share = %v, expected 6", u.WindowPercent)
	}
	if u.WeekPercent != 87 {
		t.Fatalf("week share = %v, expected 87", u.WeekPercent)
	}
	if u.WindowResets == "" || u.WeekResets == "" {
		t.Fatalf("the reset times belong in it: %+v", u)
	}
	if u.Stale {
		t.Fatal("a fresh answer must not be marked stale")
	}
	if !u.Reported() {
		t.Fatal("a parsed answer counts as reported")
	}
}

// TestParseUsageMarksStale: when the provider's endpoint is rate limited the
// engine serves bars up to an hour old. Reporting those as fresh would be worse
// than having no figure — a limit would be compared against a number nobody
// knows the age of.
func TestParseUsageMarksStale(t *testing.T) {
	u := ParseUsage("Current session: 40% used\nShowing last-known usage (fetched 12 minutes ago)")
	if !u.Stale {
		t.Fatal("the staleness note has to be read")
	}
	if u.WindowPercent != 40 {
		t.Fatalf("the figure is still usable: %v", u.WindowPercent)
	}
}

// TestParseUsageFailsOpen is the load-bearing one. The answer is prose and will
// change; what must never happen is that a text we no longer understand turns
// into "0% used", which reads as plenty of room left.
func TestParseUsageFailsOpen(t *testing.T) {
	for _, text := range []string{
		"",
		"Total cost: $0.0000\nTotal duration (API): 0s", // no credential in the sandbox
		"Your plan usage: nearly full",                  // a rewritten format
		"Current session: unknown% used",                // a number we cannot read
		"You have used up 6 of your sessions",           // reworded entirely
	} {
		u := ParseUsage(text)
		if u.Reported() {
			t.Fatalf("unparsable text must not report a figure (%q → %+v)", text, u)
		}
		if u.WindowPercent >= 0 || u.WeekPercent >= 0 {
			t.Fatalf("unknown has to stay negative, never 0 (%q → %+v)", text, u)
		}
	}
}
