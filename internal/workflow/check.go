package workflow

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

var dailyPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// Problem is something in a definition that keeps it from being saved.
type Problem struct {
	Step string // empty for the workflow as a whole
	Code string // stable, for tests and the interface
	Text string
}

func (p Problem) String() string {
	if p.Step == "" {
		return p.Code + ": " + p.Text
	}
	return p.Step + ": " + p.Code + ": " + p.Text
}

// Check finds what keeps a definition from running. It does not look at who
// holds a role today — an empty role is a matter of the moment a step starts,
// not of the definition.
func Check(d *Definition) []Problem {
	var out []Problem
	add := func(step, code, format string, args ...any) {
		out = append(out, Problem{Step: step, Code: code, Text: fmt.Sprintf(format, args...)})
	}

	var triggers []string
	for _, id := range d.Order {
		if d.Steps[id].Kind == KindTrigger {
			triggers = append(triggers, id)
		}
	}
	switch len(triggers) {
	case 0:
		add("", "no-trigger", "a workflow needs one trigger")
	case 1:
	default:
		add("", "many-triggers", "a workflow has one trigger, this one has %s", strings.Join(triggers, ", "))
	}

	for _, id := range d.Order {
		s := d.Steps[id]
		for _, exit := range sortedKeys(s.Next) {
			to := s.Next[exit]
			target, ok := d.Steps[to]
			if !ok {
				add(id, "unknown-target", "exit %q leads to %q, which is not a step", exit, to)
				continue
			}
			if target.Kind == KindTrigger {
				add(id, "into-trigger", "exit %q leads back to the trigger", exit)
			}
		}
		switch s.Kind {
		case KindEnd:
			if len(s.Next) > 0 {
				add(id, "end-has-exits", "an end leads nowhere")
			}
		case KindCondition:
			values := 0
			for e := range s.Next {
				if e != ExitLimit {
					values++
				}
			}
			if values == 0 {
				add(id, "no-exit", "a condition needs at least one value to branch on")
			}
		case KindApproval:
			if len(s.Options) == 0 {
				add(id, "no-exit", "an approval needs at least one option")
			}
		default:
			if _, ok := s.Next[ExitOut]; !ok {
				add(id, "no-exit", "leads nowhere")
			}
		}

		switch s.Kind {
		case KindTrigger:
			if s.Daily != "" && !dailyPattern.MatchString(s.Daily) {
				add(id, "bad-time", "daily must be HH:MM, got %q", s.Daily)
			}
			if s.Every > 0 && s.Every < time.Minute {
				add(id, "too-often", "every %s is shorter than a minute", s.Every)
			}
			if s.OnlyIf != "" && s.Every == 0 && s.Daily == "" {
				add(id, "gate-without-schedule", "only_if needs every or daily, as nur-wenn needs alle or täglich")
			}
			if s.OnlyIf != "" && s.As == "" {
				add(id, "no-colleague", "only_if checks with a colleague's access; name it with as")
			}
		case KindAction:
			if s.System == "" || s.Do == "" {
				add(id, "incomplete-action", "an action needs system and do")
			}
			if s.As == "" {
				add(id, "no-colleague", "an action runs as a colleague; name it with as")
			}
		case KindAgent:
			if _, ok := d.Roles[s.Role]; !ok {
				add(id, "unknown-role", "role %q is not defined", s.Role)
			}
			if s.NotSameAs != "" {
				other, ok := d.Steps[s.NotSameAs]
				if !ok || other.Kind != KindAgent {
					add(id, "four-eyes", "not_same_as names %q, which is not a colleague step", s.NotSameAs)
				}
			}
		case KindAI:
			if s.Task == "" {
				add(id, "no-task", "an AI step needs a prompt")
			}
		case KindCondition:
			step, _, ok := strings.Cut(s.On, ".")
			if !ok || d.Steps[step] == nil {
				add(id, "bad-field", "on must name step.field, got %q", s.On)
			}
		case KindApproval:
			if s.Who == "" {
				add(id, "no-person", "an approval needs someone who decides")
			}
			if s.Wait > 0 && s.Next[ExitTimeout] == "" {
				add(id, "no-timeout-exit", "it waits at most %s but has no timeout exit", s.Wait)
			}
		case KindWait:
			if s.Until == "" && s.Max == 0 {
				add(id, "endless-wait", "a wait needs an event, a maximum, or both")
			}
			if s.Max > 0 && s.Next[ExitTimeout] == "" && s.Until != "" {
				add(id, "no-timeout-exit", "it waits at most %s but has no timeout exit", s.Max)
			}
		}
	}

	// Every loop needs a way out that does not depend on the result going
	// well: a condition on the loop counts rounds and leaves by its limit.
	for _, id := range d.Order {
		s := d.Steps[id]
		if s.Kind != KindCondition {
			continue
		}
		if len(LoopExits(d, id)) == 0 {
			continue
		}
		if s.MaxRounds <= 0 {
			add(id, "loop-without-limit", "it leads back into the workflow but sets no max_rounds")
		} else if s.Next[ExitLimit] == "" {
			add(id, "loop-without-limit", "it counts %d rounds but has no limit exit", s.MaxRounds)
		}
	}
	// A loop that passes no condition at all cannot be counted.
	for _, id := range d.Order {
		if cycleWithoutCondition(d, id) {
			add(id, "uncounted-loop", "a way leads back here without passing a condition that counts rounds")
		}
	}

	if len(triggers) == 1 {
		seen := reachable(d, triggers[0])
		for _, id := range d.Order {
			if !seen[id] {
				add(id, "unreachable", "nothing leads here")
			}
		}
	}
	return out
}

// LoopExits are the exits of a condition that lead back to it — taking one
// starts another round.
func LoopExits(d *Definition, cond string) []string {
	var out []string
	for _, exit := range sortedKeys(d.Steps[cond].Next) {
		if exit == ExitLimit {
			continue
		}
		if reachable(d, d.Steps[cond].Next[exit])[cond] {
			out = append(out, exit)
		}
	}
	return out
}

func reachable(d *Definition, from string) map[string]bool {
	seen := map[string]bool{}
	stack := []string{from}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[id] || d.Steps[id] == nil {
			continue
		}
		seen[id] = true
		for _, to := range d.Steps[id].Next {
			stack = append(stack, to)
		}
	}
	return seen
}

// cycleWithoutCondition reports whether start lies on a cycle that passes no
// condition: such a loop would circle without anyone counting.
func cycleWithoutCondition(d *Definition, start string) bool {
	if d.Steps[start] == nil || d.Steps[start].Kind == KindCondition {
		return false
	}
	seen := map[string]bool{}
	stack := []string{}
	for _, to := range d.Steps[start].Next {
		stack = append(stack, to)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == start {
			return true
		}
		s := d.Steps[id]
		if seen[id] || s == nil || s.Kind == KindCondition {
			continue
		}
		seen[id] = true
		for _, to := range s.Next {
			stack = append(stack, to)
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
