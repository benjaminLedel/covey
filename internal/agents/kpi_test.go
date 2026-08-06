package agents

import (
	"strings"
	"testing"
)

func TestParseKPIs(t *testing.T) {
	content := `# KPIS.md — what this agent is measured by

Prose lines like this one are ignored.

- kennzahl: geloeste-tickets titel: Gelöste Tickets zählt: aktion zammad:reply_external je: ticket_id ziel: 20 pro woche
- kennzahl: code-reviews titel: Code-Reviews zählt: aktion gitlab:comment_mr je: mr_iid
- kennzahl: bugs-erfasst zählt: aktion gitlab:create_issue
- kennzahl: alles-gitlab titel: GitLab überhaupt berührt zählt: aktion gitlab:*
- kennzahl: laeufe titel: Erledigte Aufgaben zählt: aufgabe erledigt herkunft: heartbeat
`
	ks, err := ParseKPIs(content)
	if err != nil {
		t.Fatalf("ParseKPIs: %v", err)
	}
	if len(ks) != 5 {
		t.Fatalf("expected 5 entries, got %d: %+v", len(ks), ks)
	}
	if ks[0].Key != "geloeste-tickets" || ks[0].Title != "Gelöste Tickets" {
		t.Errorf("entry 0 wrong: %+v", ks[0])
	}
	if ks[0].Action != "zammad:reply_external" || ks[0].Per != "ticket_id" {
		t.Errorf("action/je of entry 0 wrong: %+v", ks[0])
	}
	if ks[0].Goal != 20 || ks[0].Period != "woche" {
		t.Errorf("goal of entry 0 wrong: %+v", ks[0])
	}
	// Without titel: the key stands in for it — a slug is still readable, and
	// refusing the line would be pedantry.
	if ks[2].Title != "bugs-erfasst" {
		t.Errorf("title fallback missing: %q", ks[2].Title)
	}
	// Without je: the raw event count is intended, not an oversight.
	if ks[2].Per != "" {
		t.Errorf("je must stay empty: %+v", ks[2])
	}
	if ks[3].Action != "gitlab:*" {
		t.Errorf("wildcard action not accepted: %+v", ks[3])
	}
	if !ks[4].Task || ks[4].Action != "" || ks[4].Origin != "heartbeat" {
		t.Errorf("task form wrong: %+v", ks[4])
	}
}

func TestParseKPIsEmpty(t *testing.T) {
	ks, err := ParseKPIs("")
	if err != nil || len(ks) != 0 {
		t.Fatalf("empty file: ks=%v err=%v", ks, err)
	}
}

// The parser is strict about recognised lines, because the failure mode of a
// silent typo is a figure reading "delivered nothing" — indistinguishable from
// a lazy agent.
func TestParseKPIsErrors(t *testing.T) {
	cases := map[string]string{
		"key missing":           "- titel: X zählt: aktion zammad:reply",
		"key not a slug":        "- kennzahl: Gelöste Tickets zählt: aktion zammad:reply",
		"nothing counted":       "- kennzahl: x titel: X",
		"action without verb":   "- kennzahl: x zählt: aktion zammad",
		"action with spaces":    "- kennzahl: x zählt: aktion zammad reply",
		"unknown count form":    "- kennzahl: x zählt: tickets zammad:reply",
		"open tasks not valid":  "- kennzahl: x zählt: aufgabe offen",
		"je without value":      "- kennzahl: x zählt: aktion zammad:reply je:",
		"je with two values":    "- kennzahl: x zählt: aktion zammad:reply je: a b",
		"je on the task form":   "- kennzahl: x zählt: aufgabe erledigt je: ticket_id",
		"herkunft on an action": "- kennzahl: x zählt: aktion zammad:reply herkunft: heartbeat",
		"goal without period":   "- kennzahl: x zählt: aufgabe erledigt ziel: 20",
		"goal unknown period":   "- kennzahl: x zählt: aufgabe erledigt ziel: 20 pro quartal",
		"goal not a number":     "- kennzahl: x zählt: aufgabe erledigt ziel: viele pro woche",
		"goal zero":             "- kennzahl: x zählt: aufgabe erledigt ziel: 0 pro woche",
		"key defined twice":     "- kennzahl: x zählt: aufgabe erledigt\n- kennzahl: x zählt: aktion zammad:reply",
	}
	for name, content := range cases {
		if _, err := ParseKPIs(content); err == nil {
			t.Errorf("%s: expected an error for %q", name, content)
		}
	}
}

// The measurement stays outside the measured system: an agent that knew it is
// counted on comments would write more comments.
func TestKPIsNotInPrompt(t *testing.T) {
	files := map[string]string{
		"SOUL.md": "Du bist ein Support-Agent.",
		"KPIS.md": "- kennzahl: geloeste-tickets zählt: aktion zammad:reply_external je: ticket_id ziel: 20 pro woche",
	}
	prompt := CompilePrompt(files)
	if !strings.Contains(prompt, "Support-Agent") {
		t.Fatal("SOUL.md belongs in the prompt")
	}
	for _, forbidden := range []string{"kennzahl", "geloeste-tickets", "ziel:", "20 pro woche"} {
		if strings.Contains(prompt, forbidden) {
			t.Errorf("KPIS.md leaked into the prompt (%q):\n%s", forbidden, prompt)
		}
	}
}
