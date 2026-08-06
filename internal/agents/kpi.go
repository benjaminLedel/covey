package agents

import (
	"fmt"
	"regexp"
	"strings"
)

// KPI is an entry from KPIS.md: a named counting rule over the evidence the
// control plane recorded itself — executed target-system actions and backlog
// state transitions (spec/17-kpis.md).
//
// The agent never reports these numbers itself, and it never sees them: KPIS.md
// is not compiled into the system prompt. An agent that knows it is measured on
// the number of comments writes more comments.
type KPI struct {
	Key    string `json:"key"`              // kennzahl: — the comparison anchor across agents
	Title  string `json:"title"`            // titel: — display name
	Action string `json:"action,omitempty"` // zählt: aktion <system>:<verb> ("*" as verb = any)
	Task   bool   `json:"task,omitempty"`   // zählt: aufgabe erledigt
	Origin string `json:"origin,omitempty"` // herkunft: — narrows the task form
	Per    string `json:"per,omitempty"`    // je: — param that identifies the object (COUNT DISTINCT)
	Goal   int    `json:"goal,omitempty"`   // ziel: — expectation per period, 0 = none
	Period string `json:"period,omitempty"` // the period of the goal ("tag"/"woche"/"monat")
}

// kpiKeywords are the attribute keys of a KPIS.md line.
var kpiKeywords = map[string]bool{
	"kennzahl:": true, "titel:": true, "zählt:": true, "zaehlt:": true,
	"je:": true, "ziel:": true, "herkunft:": true,
}

// kpiKeyRe restricts the comparison anchor to a slug — it groups agents in the
// org-wide evaluation, so it must not carry spaces or case.
var kpiKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,47}$`)

// kpiActionRe is the shape of a countable action: system:verb, verb "*" for all
// actions of a system.
var kpiActionRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*:([a-z][a-z0-9_-]*|\*)$`)

// kpiPeriods are the periods a goal may refer to.
var kpiPeriods = map[string]bool{"tag": true, "woche": true, "monat": true}

// ParseKPIs reads KPIS.md lines of the form
//
//   - kennzahl: geloeste-tickets titel: Gelöste Tickets zählt: aktion zammad:reply_external je: ticket_id ziel: 20 pro woche
//   - kennzahl: laeufe titel: Erledigte Aufgaben zählt: aufgabe erledigt herkunft: heartbeat
//
// Lines that do not start with one of the keys are prose and are ignored.
//
// Like ParseHeartbeat the parser is strict about recognised lines: a rule
// without a key or without something to count is an error, because the failure
// mode of a silent typo is a figure that reads as "this agent delivered
// nothing" — indistinguishable from a lazy agent, and far more expensive than a
// rejected save.
//
// What the parser can NOT catch is a rule that is well-formed but points at an
// action no plugin offers (any more). That surfaces as a lint finding on the
// agent page once the agent has run without the rule matching anything.
func ParseKPIs(content string) ([]KPI, error) {
	var out []KPI
	seen := map[string]bool{}
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "-"))
		fields := strings.Fields(line)
		if len(fields) == 0 || !kpiKeywords[fields[0]] {
			continue
		}
		var k KPI
		for i := 0; i < len(fields); i++ {
			if !kpiKeywords[fields[i]] {
				continue
			}
			// Value: all tokens up to the next key (titel: is free text).
			var val []string
			for j := i + 1; j < len(fields) && !kpiKeywords[fields[j]]; j++ {
				val = append(val, fields[j])
			}
			v := strings.Join(val, " ")
			switch fields[i] {
			case "kennzahl:":
				k.Key = v
			case "titel:":
				k.Title = v
			case "zählt:", "zaehlt:":
				if err := parseCounts(&k, val, line); err != nil {
					return nil, err
				}
			case "je:":
				if len(val) != 1 {
					return nil, fmt.Errorf("kpi %q: je: needs exactly one parameter name (e.g. ticket_id)", line)
				}
				k.Per = v
			case "herkunft:":
				k.Origin = v
			case "ziel:":
				if err := parseGoal(&k, val, line); err != nil {
					return nil, err
				}
			}
		}
		if !kpiKeyRe.MatchString(k.Key) {
			return nil, fmt.Errorf("kpi %q: kennzahl: must be a slug matching %s", line, kpiKeyRe)
		}
		if seen[k.Key] {
			return nil, fmt.Errorf("kpi %q: kennzahl: %q defined twice", line, k.Key)
		}
		if k.Action == "" && !k.Task {
			return nil, fmt.Errorf("kpi %q: zählt: missing (aktion <system>:<verb> or aufgabe erledigt)", k.Key)
		}
		if k.Origin != "" && !k.Task {
			return nil, fmt.Errorf("kpi %q: herkunft: only applies to zählt: aufgabe erledigt", k.Key)
		}
		if k.Per != "" && k.Task {
			return nil, fmt.Errorf("kpi %q: je: only applies to zählt: aktion — a task is already the object", k.Key)
		}
		if k.Title == "" {
			k.Title = k.Key
		}
		seen[k.Key] = true
		out = append(out, k)
	}
	return out, nil
}

// parseCounts reads the two forms of `zählt:`.
func parseCounts(k *KPI, val []string, line string) error {
	if len(val) != 2 {
		return fmt.Errorf("kpi %q: zählt: expects `aktion <system>:<verb>` or `aufgabe erledigt`", line)
	}
	switch val[0] {
	case "aktion":
		if !kpiActionRe.MatchString(val[1]) {
			return fmt.Errorf("kpi %q: %q is not an action of the form system:verb (verb may be *)", line, val[1])
		}
		k.Action = val[1]
	case "aufgabe":
		// Only the terminal state counts as delivery. `aufgabe offen` would
		// count intentions, which is what this whole document argues against.
		if val[1] != "erledigt" {
			return fmt.Errorf("kpi %q: only `aufgabe erledigt` counts, not %q", line, val[1])
		}
		k.Task = true
	default:
		return fmt.Errorf("kpi %q: zählt: expects `aktion …` or `aufgabe erledigt`, not %q", line, val[0])
	}
	return nil
}

// parseGoal reads `ziel: 20 pro woche`. The bare number without a period is
// rejected: "20" alone is unreadable — per day it is a lot, per month little.
func parseGoal(k *KPI, val []string, line string) error {
	if len(val) != 3 || val[1] != "pro" {
		return fmt.Errorf("kpi %q: ziel: expects `<number> pro tag|woche|monat`", line)
	}
	n := 0
	if _, err := fmt.Sscanf(val[0], "%d", &n); err != nil || n <= 0 {
		return fmt.Errorf("kpi %q: ziel: %q is not a positive number", line, val[0])
	}
	if !kpiPeriods[val[2]] {
		return fmt.Errorf("kpi %q: ziel: period %q unknown (tag, woche or monat)", line, val[2])
	}
	k.Goal, k.Period = n, val[2]
	return nil
}
