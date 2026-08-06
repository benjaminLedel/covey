package agents

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rules collects the rule names of the findings so that tests can check on them.
func rules(fs []Finding) []string {
	out := make([]string, len(fs))
	for i, f := range fs {
		out[i] = f.Rule
	}
	return out
}

func hasRule(fs []Finding, rule string) bool {
	for _, f := range fs {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

// TestLintIntervalDependsOnSystems is the core of the interval rule: the same
// interval is fine or ruinous depending on the target system. Checking a
// mailbox every 2 minutes is normal; cloning a repo every 2 minutes is not.
func TestLintIntervalDependsOnSystems(t *testing.T) {
	hb := "- alle: 2m nur-wenn: gitlab:issues:assigned titel: Issues sichten aufgabe: Bearbeite und kommentiere.\n"

	heavy := Lint(Subject{Slug: "dev", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read,write",
		"HEARTBEAT.md": hb,
	}})
	if !hasRule(heavy, "heartbeat-interval-too-short") {
		t.Fatalf("2m with gitlab has to be flagged, got %v", rules(heavy))
	}

	// The same interval on a pure mailbox agent: no finding.
	light := Lint(Subject{Slug: "mail", Files: map[string]string{
		"ACCESS.md":    "- system: email scope: read,write",
		"HEARTBEAT.md": "- alle: 3m nur-wenn: email titel: Posteingang aufgabe: Antworte per reply.\n",
	}})
	if hasRule(light, "heartbeat-interval-too-short") {
		t.Fatalf("3m without a heavy target system must not be flagged, got %v", rules(light))
	}
}

// TestLintVisibleTrace checks the rule behind the observed endless loop: a
// gitlab-gated heartbeat without a comment step leaves no edge.
func TestLintVisibleTrace(t *testing.T) {
	stumm := Lint(Subject{Slug: "stumm", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read,write",
		"HEARTBEAT.md": "- alle: 15m nur-wenn: gitlab:issues titel: Sichten aufgabe: Lies die Issues und setze Labels.\n",
		"PLAYBOOKS.md": "1. list_issues aufrufen.\n2. Label setzen.\n",
	}})
	if !hasRule(stumm, "no-visible-trace") {
		t.Fatalf("a playbook without a comment step has to be flagged, got %v", rules(stumm))
	}

	// With a comment step the edge is secured — no finding.
	laut := Lint(Subject{Slug: "laut", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read,write,comment",
		"HEARTBEAT.md": "- alle: 15m nur-wenn: gitlab:issues titel: Sichten aufgabe: Bearbeite die Issues.\n",
		"PLAYBOOKS.md": "1. list_issues aufrufen.\n2. Ergebnis per comment im Issue festhalten.\n",
	}})
	if hasRule(laut, "no-visible-trace") {
		t.Fatalf("a playbook with comment must not be flagged, got %v", rules(laut))
	}

	// Without a gitlab gate the rule does not apply at all — it holds for the
	// edge condition, not as a general obligation to comment.
	ohneGate := Lint(Subject{Slug: "frei", Files: map[string]string{
		"ACCESS.md":    "- system: gitlab scope: read",
		"HEARTBEAT.md": "- täglich: 09:00 titel: Bericht aufgabe: Fasse zusammen.\n",
		"PLAYBOOKS.md": "1. Nichts kommentieren.\n",
	}})
	if hasRule(ohneGate, "no-visible-trace") {
		t.Fatalf("without a gitlab gate the rule must not apply, got %v", rules(ohneGate))
	}
}

// TestLintBlockedNegationsNotFlagged: good configs write "end with done, NEVER
// blocked". A rule that fires on that would be worthless — it would report
// exactly the agents that get it right.
func TestLintBlockedNegationsNotFlagged(t *testing.T) {
	gut := Lint(Subject{Slug: "gut", Files: map[string]string{
		"ACCESS.md": "- system: gitlab scope: read,write",
		"SOUL.md":   "Ende jeden Lauf mit done, NIE mit blocked (GitLab hat keinen Webhook).",
	}})
	if hasRule(gut, "blocked-on-polling-system") {
		t.Fatalf("a negation must not be flagged, got %v", rules(gut))
	}

	schlecht := Lint(Subject{Slug: "schlecht", Files: map[string]string{
		"ACCESS.md": "- system: gitlab scope: read,write",
		"SOUL.md":   "Wartest du auf eine Antwort, beende den Lauf mit blocked.",
	}})
	if !hasRule(schlecht, "blocked-on-polling-system") {
		t.Fatalf("a real blocked instruction has to be flagged, got %v", rules(schlecht))
	}
}

// TestLintStages checks the board rules against the sprawl observed in the wild.
func TestLintStages(t *testing.T) {
	f := Lint(Subject{Slug: "board", AgentStages: []string{"Issue-Triage", "#83 CSV-Import"}})
	if !hasRule(f, "stage-names-item") {
		t.Fatalf("a column with an item ID has to be flagged, got %v", rules(f))
	}
	if hasRule(f, "stage-sprawl") {
		t.Fatalf("two columns are not sprawl, got %v", rules(f))
	}

	viele := make([]string, 12)
	for i := range viele {
		viele[i] = string(rune('A' + i))
	}
	if !hasRule(Lint(Subject{Slug: "board", AgentStages: viele}), "stage-sprawl") {
		t.Fatalf("12 columns have to be flagged")
	}
}

// TestLintTurnLimit only reports on accumulation — the platform catches
// individual aborts itself with a follow-up task.
func TestLintTurnLimit(t *testing.T) {
	if hasRule(Lint(Subject{Slug: "a", TurnLimitFailures: 1}), "frequent-turn-limit-aborts") {
		t.Fatalf("a single abort is not a finding")
	}
	f := Lint(Subject{Slug: "a", TurnLimitFailures: 7, MaxTurns: 20})
	if !hasRule(f, "frequent-turn-limit-aborts") {
		t.Fatalf("an accumulation has to be flagged, got %v", rules(f))
	}
	if !strings.Contains(f[0].Hint, "max_turns") {
		t.Fatalf("with a small max_turns the hint has to name it: %q", f[0].Hint)
	}
}

// TestLintCleanConfigIsSilent is the most important assurance: a lint that nags
// at good configs gets ignored and is therefore worthless. Checked against the
// shipped example bundles — they are the template for new agents and have to be
// free of findings.
func TestLintCleanConfigIsSilent(t *testing.T) {
	paths, err := filepath.Glob("../../examples/*.bundle.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("example bundles not found: %v", err)
	}
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var bundle struct {
			Agent struct {
				Slug string `json:"slug"`
			} `json:"agent"`
			Files  map[string]string `json:"files"`
			Skills []struct {
				Name  string            `json:"name"`
				Files map[string]string `json:"files"`
			} `json:"skills"`
		}
		if err := json.Unmarshal(raw, &bundle); err != nil {
			t.Fatal(err)
		}
		// The skills belong to it: ever since procedures migrate there from
		// PLAYBOOKS.md, the lint would otherwise face a config missing half its
		// description — and report findings that are none.
		skills := map[string]string{}
		for _, sk := range bundle.Skills {
			var b strings.Builder
			for _, content := range sk.Files {
				b.WriteString(content)
				b.WriteString("\n")
			}
			skills[sk.Name] = b.String()
		}
		if f := Lint(Subject{Slug: bundle.Agent.Slug, Files: bundle.Files, Skills: skills}); len(f) > 0 {
			t.Errorf("%s: the example bundle has to be free of findings, got %+v", filepath.Base(p), f)
		}
	}
}

// The turn-limit finding has to name the limit even when the agent sets none of
// its own. The condition used to read `MaxTurns > 0 && MaxTurns < 50` and so
// kept quiet in exactly the case where it matters most: 0 means the agent runs
// against the default of 30, the tightest value there is. That was the
// situation of tester-1 — 22 of 23 failures at the turn limit, and the rule
// that would have said so said nothing about it.
func TestLintTurnLimitNamesTheDefault(t *testing.T) {
	f := Lint(Subject{
		Slug:              "tester-1",
		Files:             map[string]string{"ACCESS.md": "- system: email scope: read"},
		TurnLimitFailures: 22,
		MaxTurns:          0,
	})
	hint := findingHint(t, f, "frequent-turn-limit-aborts")
	if !strings.Contains(hint, "max_turns") || !strings.Contains(hint, "30") {
		t.Fatalf("the hint has to name the effective limit: %q", hint)
	}
}

// With a heavy system the recommendation tips: whoever checks out a repo,
// installs dependencies and runs a test suite does not manage it in 30 turns,
// however small the assignment is cut.
func TestLintTurnLimitPointsAtTheSubAgentForRepoWork(t *testing.T) {
	f := Lint(Subject{
		Slug:              "tester-1",
		Files:             map[string]string{"ACCESS.md": "- system: gitlab scope: read,write\n- system: dev scope: exec"},
		TurnLimitFailures: 22,
		MaxTurns:          0,
	})
	hint := findingHint(t, f, "frequent-turn-limit-aborts")
	if !strings.Contains(hint, "dev agent") {
		t.Fatalf("for repo work the sub-agent is the way out, not a smaller assignment: %q", hint)
	}
}

// An agent that has raised its limit far enough is not nagged about max_turns.
func TestLintTurnLimitStaysQuietAboutAGenerousLimit(t *testing.T) {
	f := Lint(Subject{
		Slug:              "x",
		Files:             map[string]string{"ACCESS.md": "- system: gitlab scope: read"},
		TurnLimitFailures: 5,
		MaxTurns:          150,
	})
	hint := findingHint(t, f, "frequent-turn-limit-aborts")
	if strings.Contains(hint, "max_turns") {
		t.Fatalf("at 150 turns the limit is not the problem: %q", hint)
	}
}

func findingHint(t *testing.T, findings []Finding, rule string) string {
	t.Helper()
	for _, f := range findings {
		if f.Rule == rule {
			return f.Hint
		}
	}
	t.Fatalf("finding %q missing from %+v", rule, findings)
	return ""
}

// A renamed action breaks an indicator silently: the rule keeps parsing, the
// figure drops to zero, and zero looks exactly like a lazy agent.
func TestLintReportsAnIndicatorThatCountsNothing(t *testing.T) {
	s := Subject{
		Slug: "support",
		Files: map[string]string{
			"KPIS.md": "- kennzahl: geloeste-tickets zählt: aktion zammad:antwort_extern je: ticket_id\n" +
				"- kennzahl: notizen zählt: aktion zammad:note",
		},
		ActionCounts: map[string]int{"zammad:reply_external": 40, "zammad:note": 3, "zammad:get_ticket": 90},
	}
	f := Lint(s)
	hint := findingHint(t, f, "kpi-never-matched")
	// The finding has to name what the agent actually does — otherwise it says
	// what is wrong but not what to write instead.
	if !strings.Contains(hint, "zammad:get_ticket") {
		t.Errorf("the hint has to name the agent's actual actions: %q", hint)
	}
	// Only the broken rule, not the one that matches.
	if n := countFindings(f, "kpi-never-matched"); n != 1 {
		t.Errorf("expected exactly one finding, got %d: %+v", n, f)
	}
}

// A wildcard rule counts every action of its system — it must not be reported
// just because that exact string never appears.
func TestLintAcceptsAWildcardIndicator(t *testing.T) {
	f := Lint(Subject{
		Slug:         "dev",
		Files:        map[string]string{"KPIS.md": "- kennzahl: gitlab zählt: aktion gitlab:*"},
		ActionCounts: map[string]int{"gitlab:comment_mr": 7},
	})
	if n := countFindings(f, "kpi-never-matched"); n != 0 {
		t.Errorf("the wildcard matched — no finding belongs here: %+v", f)
	}
}

// A fresh agent that has not worked yet is not nagged: its rules simply have
// not had their first hit, and a lint that complains about correct configs is
// one nobody reads.
func TestLintStaysQuietWhileTheAgentHasNotWorked(t *testing.T) {
	f := Lint(Subject{
		Slug:  "neu",
		Files: map[string]string{"KPIS.md": "- kennzahl: geloeste-tickets zählt: aktion zammad:reply_external"},
	})
	if n := countFindings(f, "kpi-never-matched"); n != 0 {
		t.Errorf("without actions in the window the rule is dropped: %+v", f)
	}
}

func countFindings(findings []Finding, rule string) int {
	n := 0
	for _, f := range findings {
		if f.Rule == rule {
			n++
		}
	}
	return n
}

// The hint has to name actions one would actually want to count. Sorted by
// frequency alone it lists reading actions — measured on covey.work it read
// "email:mark_seen, gitlab:list_merge_requests, email:get_message", all true
// and none of it delivery.
func TestLintHintPrefersActionsWithEffect(t *testing.T) {
	f := Lint(Subject{
		Slug:  "support",
		Files: map[string]string{"KPIS.md": "- kennzahl: geloeste-tickets zählt: aktion zammad:gibtsnicht"},
		ActionCounts: map[string]int{
			"zammad:get_ticket": 500, "zammad:list_articles": 300,
			"zammad:reply_external": 12, "zammad:set_state": 4,
		},
	})
	hint := findingHint(t, f, "kpi-never-matched")
	if !strings.Contains(hint, "zammad:reply_external") || !strings.Contains(hint, "zammad:set_state") {
		t.Errorf("the writing actions belong up front: %q", hint)
	}
	if strings.Contains(hint, "zammad:list_articles") {
		t.Errorf("with two effectful actions a read action must not push in: %q", hint)
	}
}
