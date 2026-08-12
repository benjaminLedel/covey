package examples

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinsLoad(t *testing.T) {
	bs := Builtins()
	if len(bs) != len(manifest) {
		t.Fatalf("expected %d builtins, got %d", len(manifest), len(bs))
	}
	seen := map[string]bool{}
	for _, b := range bs {
		if b.ID.String() == "00000000-0000-0000-0000-000000000000" {
			t.Errorf("%s: empty ID", b.Key)
		}
		if seen[b.ID.String()] {
			t.Errorf("%s: duplicate ID %s", b.Key, b.ID)
		}
		seen[b.ID.String()] = true
		if b.Name == "" {
			t.Errorf("%s: no name", b.Key)
		}
		// The bundle has to be an instantiable agent-config.
		var probe struct {
			Kind  string `json:"kind"`
			Agent struct {
				Slug string `json:"slug"`
			} `json:"agent"`
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal(b.Bundle, &probe); err != nil {
			t.Fatalf("%s: bundle not readable: %v", b.Key, err)
		}
		if probe.Kind != "covey.agent-config" {
			t.Errorf("%s: kind=%q, expected covey.agent-config", b.Key, probe.Kind)
		}
		if probe.Agent.Slug == "" {
			t.Errorf("%s: agent.slug missing", b.Key)
		}
		if len(probe.Files) == 0 {
			t.Errorf("%s: no files", b.Key)
		}
	}
}

// TestBuiltinIDsStable pins the fixed IDs: if they change, existing
// instantiation links break — deliberately a regression guard.
func TestBuiltinIDsStable(t *testing.T) {
	want := map[string]string{
		"builtin:people-department":         "",
		"builtin:improvement-engineer":      "",
		"builtin:coding-agent":              "",
		"builtin:qa-agent":                  "",
		"builtin:delivery-lead":             "",
		"builtin:log-triage-agent":          "",
		"builtin:web-researcher":            "",
		"builtin:dependency-security-agent": "",
	}
	for _, b := range Builtins() {
		if _, ok := want[b.Key]; !ok {
			t.Errorf("unexpected key %q", b.Key)
		}
	}
}

// TestBuiltinSkills pins the rules under which a skill in a bundled template
// fails without anybody noticing:
//
//   - Without a SKILL.md the runtime does not recognise the directory as a
//     skill; the agent would silently get nothing at all.
//   - Without a description nothing decides whether the skill gets loaded.
//   - And a skill that neither PLAYBOOKS.md nor HEARTBEAT.md points at is never
//     pulled: it then permanently costs its description in the context and
//     contributes nothing. That is the real trap when converting a template —
//     the procedure moves into the skill and the reference is left out.
func TestBuiltinSkills(t *testing.T) {
	for _, b := range Builtins() {
		var bundle struct {
			Files  map[string]string `json:"files"`
			Skills []struct {
				Name        string            `json:"name"`
				Description string            `json:"description"`
				Origin      string            `json:"origin"`
				Files       map[string]string `json:"files"`
			} `json:"skills"`
		}
		if err := json.Unmarshal(b.Bundle, &bundle); err != nil {
			t.Fatalf("%s: %v", b.Key, err)
		}
		references := bundle.Files["PLAYBOOKS.md"] + bundle.Files["HEARTBEAT.md"]
		for _, sk := range bundle.Skills {
			if sk.Files["SKILL.md"] == "" {
				t.Errorf("%s / %s: SKILL.md missing or empty", b.Key, sk.Name)
			}
			if sk.Description == "" {
				t.Errorf("%s / %s: description missing", b.Key, sk.Name)
			}
			if sk.Origin != "agent" && sk.Origin != "library" {
				t.Errorf("%s / %s: origin=%q (expected agent or library)", b.Key, sk.Name, sk.Origin)
			}
			if !strings.Contains(references, sk.Name) {
				t.Errorf("%s: neither PLAYBOOKS.md nor HEARTBEAT.md points at skill %q — it is never pulled",
					b.Key, sk.Name)
			}
		}
	}
}

// TestBuiltinMeldetNachOben pins the colleague channel (spec/21).
//
// It is the one of the three causes the evidence is genuinely poor at: a wrong
// assignment produces runs that look perfectly ordinary, and a limit somebody
// worked around silently leaves no trace at all. What closes that gap is not a
// mechanism — `covey/create_task` carries it already, fail-closed in all three
// directions that matter — but a section in every colleague's playbook. A
// section that only some bundles carry is a channel that only sometimes exists,
// which is why this is pinned rather than trusted.
//
// The improvement engineer is exempt: it is the recipient.
func TestBuiltinMeldetNachOben(t *testing.T) {
	// Two markers, one per language — the section is written out in each
	// bundle, in its own language, and both name the action.
	marker := []string{
		"Wenn dich etwas aufhält, das nicht dein Auftrag war",
		"When something that was not your assignment holds you up",
	}
	for _, b := range Builtins() {
		if b.Key == "builtin:improvement-engineer" {
			continue
		}
		var probe struct {
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal(b.Bundle, &probe); err != nil {
			t.Fatalf("%s: %v", b.Key, err)
		}
		pb := probe.Files["PLAYBOOKS.md"]
		var found bool
		for _, m := range marker {
			if strings.Contains(pb, m) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: PLAYBOOKS.md has no section on reporting what held the agent up", b.Key)
		}
		if !strings.Contains(pb, "covey/create_task") {
			t.Errorf("%s: the section has to name the action it runs through", b.Key)
		}
	}
}
