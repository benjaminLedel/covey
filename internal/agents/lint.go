package agents

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// The config lint checks agent configurations against patterns that in practice
// have produced endless loops, burnt budgets or unusable boards.
//
// Why as a separate check and not as a prohibition when saving: they are
// **warnings with context**, not hard errors. A 2-minute heartbeat is perfectly
// fine for an agent that only checks a mailbox and ruinous for one that clones
// repos — the rule can tell the two apart, but never with final certainty.
// Whoever knows better shall be allowed to save.
//
// The check is a pure function over Subject: the caller collects the facts (the
// CLI from the database), the rules decide without I/O. That makes them
// testable and reusable from the UI or the API later on.

// Severity of a finding.
const (
	SeverityWarn = "warn" // ought to be changed
	SeverityInfo = "info" // a hint, often fine
)

// Finding is a single lint finding on an agent.
type Finding struct {
	AgentSlug string `json:"agent_slug"`
	Rule      string `json:"rule"`
	Severity  string `json:"severity"`
	// File/Line point at the spot in the config. Empty resp. 0 for findings
	// that come from the runtime state (columns, aborts).
	File    string `json:"file,omitempty"`
	Line    int    `json:"line,omitempty"`
	Message string `json:"message"`
	Hint    string `json:"hint"`
}

// Subject bundles everything the rules need to know about an agent.
// The runtime fields are optional: if they are missing (value 0/nil), the rules
// that need them are dropped — the lint then runs over the config alone.
type Subject struct {
	Slug  string
	Files map[string]string

	// Skills are the agent's skills (internal/skills): skill name → the content
	// of its files. They belong in the check because procedures migrate there
	// from PLAYBOOKS.md — a comment step inside a skill is the same step. If the
	// lint did not see it, it would warn wrongly on every migrated agent, and a
	// rule that nags at good configs gets ignored.
	Skills map[string]string

	// AgentStages are the board columns the agent created itself
	// (created_by='agent').
	AgentStages []string
	// TurnLimitFailures counts tasks that were aborted at the turn limit.
	TurnLimitFailures int
	// MaxTurns is the agent's turn limit (0 = runtime default).
	MaxTurns int

	// ActionCounts are the target-system actions this agent actually executed
	// in the observation window (action name → count, successful ones only).
	//
	// It is what makes the indicator rule decidable: a KPIS.md line pointing at
	// an action that never occurs counts zero forever, and zero reads like a
	// lazy agent rather than a configuration error. Empty (or nil) means the
	// agent did no work in the window at all — then the rule is dropped, or
	// every freshly created agent would be nagged about rules that simply have
	// not had their first hit yet.
	ActionCounts map[string]int
}

// heavySystems are target systems whose use typically lifts a run into the
// range of minutes: cloning repos, builds, real browser sessions. For them a
// tight heartbeat cadence is expensive and rarely sensible.
var heavySystems = map[string]bool{"gitlab": true, "github": true, "dev": true, "browser": true}

// edgeGatedSystems are the target systems whose `nur-wenn:` check triggers on
// the EDGE: an item counts as handled as soon as the last contribution comes
// from the bot. A playbook that works without commenting leaves no edge there
// (see lintVisibleTrace).
var edgeGatedSystems = []string{"gitlab", "github"}

// commentActions are the actions with which an agent leaves a visible trace in
// the target system. Without one of them the `nur-wenn:` edge never tips.
var commentActions = []string{"comment", "comment_mr", "comment_pr", "comment_external", "reply", "create_issue"}

// stageWithItemID spots column names that name a concrete item instead of a
// working state ("#83 CSV import", "MR !1641").
var stageWithItemID = regexp.MustCompile(`[#!]\d+`)

// maxAgentStages is the number of self-created columns from which on a board no
// longer shows states but a history.
const maxAgentStages = 8

// DefaultMaxTurns is the runaway guard per runtime run when the agent has not
// set a turn limit of its own (agents.max_turns = 0). It lives here rather than
// in the orchestrator because the lint has to name it: a finding that says "too
// few turns" without saying how few is not actionable.
const DefaultMaxTurns = 30

// Lint checks an agent and returns the findings, most severe first.
func Lint(s Subject) []Finding {
	var out []Finding
	systems := lintSystems(s.Files["ACCESS.md"])
	hbs, _ := ParseHeartbeat(s.Files["HEARTBEAT.md"])

	out = append(out, lintIntervals(s, hbs, systems)...)
	out = append(out, lintVisibleTrace(s, hbs)...)
	out = append(out, lintBlockedOnPolling(s, systems)...)
	out = append(out, lintStages(s)...)
	out = append(out, lintTurnLimit(s)...)
	out = append(out, lintDeadKPIs(s)...)

	sort.SliceStable(out, func(i, j int) bool {
		return out[i].Severity == SeverityWarn && out[j].Severity != SeverityWarn
	})
	return out
}

func lintSystems(access string) map[string]bool {
	systems := map[string]bool{}
	for _, a := range ParseAccess(access) {
		systems[strings.ToLower(a.System)] = true
	}
	return systems
}

func (s Subject) hasHeavySystem(systems map[string]bool) bool {
	for name := range systems {
		if heavySystems[name] {
			return true
		}
	}
	return false
}

// lintIntervals: the interval has to match the duration of a run, not the
// desired reaction time. An agent that clones a repo and runs tests needs
// minutes to quarter-hours — with `alle: 2m` the next run begins before the
// previous one has understood what it is about.
func lintIntervals(s Subject, hbs []Heartbeat, systems map[string]bool) []Finding {
	heavy := s.hasHeavySystem(systems)
	var out []Finding
	for _, hb := range hbs {
		if hb.Every <= 0 {
			continue // time-of-day form, no interval
		}
		mins := hb.Every.Minutes()
		switch {
		case heavy && mins < 5:
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "heartbeat-interval-too-short", Severity: SeverityWarn,
				File: "HEARTBEAT.md", Line: heartbeatLine(s.Files["HEARTBEAT.md"], hb.Name),
				Message: fmt.Sprintf("%q fires every %s, but the agent uses a target system with long runs (%s)",
					hb.Name, shortDur(hb.Every), joinHeavy(systems)),
				Hint: "Runs with checkout/build/browser take minutes to quarter-hours — at least 5m, realistically 15m.",
			})
		case !heavy && mins < 2:
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "heartbeat-interval-too-short", Severity: SeverityWarn,
				File: "HEARTBEAT.md", Line: heartbeatLine(s.Files["HEARTBEAT.md"], hb.Name),
				Message: fmt.Sprintf("%q fires every %s", hb.Name, shortDur(hb.Every)),
				Hint:    "Below 2 minutes a run barely pays off — every wake costs a runtime start.",
			})
		}
	}
	return out
}

// lintVisibleTrace: the `nur-wenn:` condition of GitLab and GitHub triggers on
// the edge — an item counts as handled as soon as the last contribution comes
// from the bot. A playbook that works without commenting leaves no edge: the
// same item wakes the agent again in every interval. That is the cause of the
// most expensive loop in the system.
func lintVisibleTrace(s Subject, hbs []Heartbeat) []Finding {
	var gatedOn string
	var line int
	for _, hb := range hbs {
		for _, sys := range edgeGatedSystems {
			if strings.HasPrefix(strings.ToLower(hb.OnlyIf), sys) {
				gatedOn, line = sys, heartbeatLine(s.Files["HEARTBEAT.md"], hb.Name)
				break
			}
		}
		if gatedOn != "" {
			break
		}
	}
	if gatedOn == "" {
		return nil
	}
	haystack := strings.ToLower(s.Files["PLAYBOOKS.md"] + "\n" + s.Files["HEARTBEAT.md"] + "\n" +
		s.Files["SOUL.md"] + "\n" + s.skillText())
	for _, a := range commentActions {
		if strings.Contains(haystack, a) {
			return nil
		}
	}
	return []Finding{{
		AgentSlug: s.Slug, Rule: "no-visible-trace", Severity: SeverityWarn,
		File: "PLAYBOOKS.md", Line: line,
		Message: "The heartbeat is gated on " + gatedOn + ", but no playbook step leaves a comment",
		Hint:    "A silent run leaves no edge — at the next interval the item counts as unhandled again and wakes the agent anew. Whoever works, comments.",
	}}
}

// lintBlockedOnPolling: GitLab and email have no webhook inbound that wakes a
// parked task again. An agent that goes `blocked` there is waiting for an event
// that never arrives.
func lintBlockedOnPolling(s Subject, systems map[string]bool) []Finding {
	if !systems["gitlab"] && !systems["email"] {
		return nil
	}
	// Source by source instead of over one glued-together text: otherwise the
	// finding points at a line number that does not exist in the named file.
	for _, src := range s.proseSources() {
		for i, line := range strings.Split(src.text, "\n") {
			low := strings.ToLower(line)
			idx := strings.Index(low, "blocked")
			if idx < 0 {
				continue
			}
			// Negations are the normal case in good configs — and they rarely
			// stand directly in front of the word there ("end with done, NEVER
			// with blocked"). Hence the look at the clause before the word
			// instead of at fixed word pairs.
			if negatedBefore(low[:idx]) {
				continue
			}
			return []Finding{{
				AgentSlug: s.Slug, Rule: "blocked-on-polling-system", Severity: SeverityInfo,
				File: src.name, Line: i + 1,
				Message: "The config mentions blocked although the agent hangs off a polling target system (gitlab/email)",
				Hint:    "These systems have no webhook that wakes a parked task — end with done there and pick it up again at the next heartbeat.",
			}}
		}
	}
	return nil
}

// proseSource is a checkable piece of an agent's prose together with its origin.
type proseSource struct{ name, text string }

// proseSources are the texts that describe the behaviour: the two prompt files
// and the skills. Skills sorted by name, so that the same agent does not report
// now this and now that finding.
func (s Subject) proseSources() []proseSource {
	out := []proseSource{
		{"SOUL.md", s.Files["SOUL.md"]},
		{"PLAYBOOKS.md", s.Files["PLAYBOOKS.md"]},
	}
	names := make([]string, 0, len(s.Skills))
	for name := range s.Skills {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		out = append(out, proseSource{"skill:" + name, s.Skills[name]})
	}
	return out
}

// skillText glues all skills into one haystack — for rules that only search
// whether something occurs anywhere.
func (s Subject) skillText() string {
	var b strings.Builder
	for _, src := range s.proseSources()[2:] {
		b.WriteString(src.text)
		b.WriteString("\n")
	}
	return b.String()
}

// negationTokens negate a mention of blocked within the same sentence. Both
// languages, because an agent config may be written in either: the shipped
// templates exist in English and German, and the linter must not nag at a good
// English config just because it says "never" instead of "nie".
var negationTokens = []string{
	"nie", "niemals", "nicht", "kein", "statt", "ohne",
	"never", "not", "no", "instead", "without", "rather",
}

// negatedBefore says whether a negation stands in the clause before a mention.
// Only the current sentence is considered — a "not" two sentences earlier has
// nothing to do with this statement.
func negatedBefore(prefix string) bool {
	if cut := strings.LastIndexAny(prefix, ".;"); cut >= 0 {
		prefix = prefix[cut+1:]
	}
	for _, w := range strings.FieldsFunc(prefix, func(r rune) bool {
		return !('a' <= r && r <= 'z') && !strings.ContainsRune("äöüß", r)
	}) {
		for _, n := range negationTokens {
			if w == n {
				return true
			}
		}
	}
	return false
}

// lintStages checks the board: columns should name working states. A name with
// an item ID fits exactly one task and stays behind as a dead column
// afterwards; many columns usually mean that the agent is keeping a diary.
func lintStages(s Subject) []Finding {
	var out []Finding
	for _, name := range s.AgentStages {
		if stageWithItemID.MatchString(name) {
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "stage-names-item", Severity: SeverityWarn,
				Message: fmt.Sprintf("Board column %q names an item instead of a working state", name),
				Hint:    "Column names hold for every task (\"Analysis\"), not for one (\"#83 CSV import\"). Prescribe a fixed column list in SOUL.md.",
			})
		}
	}
	if n := len(s.AgentStages); n > maxAgentStages {
		out = append(out, Finding{
			AgentSlug: s.Slug, Rule: "stage-sprawl", Severity: SeverityWarn,
			Message: fmt.Sprintf("%d self-created board columns", n),
			Hint:    "Half a dozen is enough for any workflow. More usually means: different names for the same state. Prescribe a fixed column list in SOUL.md.",
		})
	}
	return out
}

// lintTurnLimit reports runs that were aborted at the turn limit. Individual
// ones are normal — the platform catches them with a follow-up task. If they
// pile up, the assignment is cut too big or max_turns is too small.
func lintTurnLimit(s Subject) []Finding {
	if s.TurnLimitFailures < 3 {
		return nil
	}
	// The effective limit, not the configured one. The condition used to read
	// `s.MaxTurns > 0 && s.MaxTurns < 50` and so kept quiet about max_turns in
	// exactly the case where it matters most: 0 means the agent has set NO limit
	// of its own and runs against the default of 30 — the tightest value there
	// is. That was the situation of tester-1, which had 22 of its 23 failures at
	// the turn limit and never got told.
	limit, source := s.MaxTurns, "currently %d"
	if limit <= 0 {
		limit, source = DefaultMaxTurns, "currently the default of %d, because the agent sets none of its own"
	}
	hint := "Cut the assignment smaller (playbook step: close off the partial result, file the rest via covey/create_task)"
	if limit < 50 {
		hint += fmt.Sprintf(" or raise max_turns ("+source+")", limit)
	}
	// Heavy systems tip the recommendation the other way: whoever checks out a
	// repo, installs dependencies and runs a test suite does not manage it in 30
	// turns, no matter how small the assignment is cut.
	if heavy := joinHeavy(lintSystems(s.Files["ACCESS.md"])); heavy != "" && limit < 50 {
		hint = fmt.Sprintf("With %s a run needs turns for checkout, setup, build and test — %d is too few for that. "+
			"Raise max_turns (100–150 is realistic for repo work) and hand the heavy part to a sub-agent "+
			"(dev agent with its own turn budget) instead of doing it yourself, step by step, in the main run",
			heavy, limit)
	}
	return []Finding{{
		AgentSlug: s.Slug, Rule: "frequent-turn-limit-aborts", Severity: SeverityWarn,
		Message: fmt.Sprintf("%d runs were aborted at the turn limit", s.TurnLimitFailures),
		Hint:    hint + ".",
	}}
}

// lintDeadKPIs reports an indicator that counts nothing while the agent works
// (spec/17-kpis.md).
//
// The failure mode this catches is quiet and expensive: a plugin renames an
// action, the KPIS.md line keeps parsing, and the figure drops to zero. Zero
// looks exactly like a lazy agent — nobody suspects the config, because the
// config was never touched.
//
// Only fires when the agent has executed actions in the window. Without that
// guard every freshly created agent would be nagged about rules that simply
// have not had their first hit yet, and a lint that nags at correct configs is
// one nobody reads.
//
// The task form (`zählt: aufgabe erledigt`) is not checked: there is no name
// that could go stale.
func lintDeadKPIs(s Subject) []Finding {
	if len(s.ActionCounts) == 0 {
		return nil
	}
	kpis, err := ParseKPIs(s.Files["KPIS.md"])
	if err != nil {
		return nil // a broken file is caught when saving, not here
	}
	var out []Finding
	for _, k := range kpis {
		if k.Action == "" || matchesAnyAction(k.Action, s.ActionCounts) {
			continue
		}
		out = append(out, Finding{
			AgentSlug: s.Slug, Rule: "kpi-never-matched", Severity: SeverityWarn,
			Message: fmt.Sprintf("the indicator %q counts nothing: the action %q has not occurred once, "+
				"although the agent worked", k.Key, k.Action),
			Hint: fmt.Sprintf("Check the action name in KPIS.md against what the agent actually does (%s). "+
				"Until then the indicator reads as 'delivered nothing', which is indistinguishable from a lazy agent",
				strings.Join(topActions(s.ActionCounts, 3), ", ")),
		})
	}
	return out
}

// matchesAnyAction answers whether a rule found anything — the wildcard form
// `system:*` counts every action of that system.
func matchesAnyAction(rule string, counts map[string]int) bool {
	if prefix, ok := strings.CutSuffix(rule, "*"); ok {
		for name := range counts {
			if strings.HasPrefix(name, prefix) {
				return true
			}
		}
		return false
	}
	return counts[rule] > 0
}

// topActions names the actions the agent performs most often — without them the
// finding says what is wrong but not what to write instead.
func topActions(counts map[string]int, n int) []string {
	names := make([]string, 0, len(counts))
	for name := range counts {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if counts[names[i]] != counts[names[j]] {
			return counts[names[i]] > counts[names[j]]
		}
		return names[i] < names[j]
	})
	if len(names) > n {
		names = names[:n]
	}
	return names
}

func joinHeavy(systems map[string]bool) string {
	var names []string
	for name := range systems {
		if heavySystems[name] {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

// shortDur formats an interval the way it stands in HEARTBEAT.md (2m, 1h30m).
func shortDur(d time.Duration) string {
	return strings.TrimSuffix(d.String(), "0s")
}

// heartbeatLine finds a heartbeat's line via its title. The parser does not
// pass the line number along (it is not in the persisted form either) — for a
// finding that a human is supposed to look up it is worth it nonetheless.
// Without a hit: 0, the output then only lacks the number.
func heartbeatLine(content, name string) int {
	if name == "" {
		return 0
	}
	for i, line := range strings.Split(content, "\n") {
		if strings.Contains(line, name) {
			return i + 1
		}
	}
	return 0
}
