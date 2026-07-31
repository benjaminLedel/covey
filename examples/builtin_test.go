package examples

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinsLoad(t *testing.T) {
	bs := Builtins()
	if len(bs) != len(manifest) {
		t.Fatalf("erwartet %d Builtins, bekam %d", len(manifest), len(bs))
	}
	seen := map[string]bool{}
	for _, b := range bs {
		if b.ID.String() == "00000000-0000-0000-0000-000000000000" {
			t.Errorf("%s: leere ID", b.Key)
		}
		if seen[b.ID.String()] {
			t.Errorf("%s: doppelte ID %s", b.Key, b.ID)
		}
		seen[b.ID.String()] = true
		if b.Name == "" {
			t.Errorf("%s: kein Name", b.Key)
		}
		// Das Bundle muss ein instanziierbares agent-config sein.
		var probe struct {
			Kind  string `json:"kind"`
			Agent struct {
				Slug string `json:"slug"`
			} `json:"agent"`
			Files map[string]string `json:"files"`
		}
		if err := json.Unmarshal(b.Bundle, &probe); err != nil {
			t.Fatalf("%s: Bundle nicht lesbar: %v", b.Key, err)
		}
		if probe.Kind != "covey.agent-config" {
			t.Errorf("%s: kind=%q, erwartet covey.agent-config", b.Key, probe.Kind)
		}
		if probe.Agent.Slug == "" {
			t.Errorf("%s: agent.slug fehlt", b.Key)
		}
		if len(probe.Files) == 0 {
			t.Errorf("%s: keine files", b.Key)
		}
	}
}

// TestBuiltinIDsStable hält die festen IDs fest: ändern sie sich, brechen
// bestehende Instanziierungs-Links — bewusst als Regressionsschutz.
func TestBuiltinIDsStable(t *testing.T) {
	want := map[string]string{
		"builtin:coding-agent":     "",
		"builtin:qa-agent":         "",
		"builtin:delivery-lead":    "",
		"builtin:log-triage-agent": "",
		"builtin:web-researcher":   "",
	}
	for _, b := range Builtins() {
		if _, ok := want[b.Key]; !ok {
			t.Errorf("unerwarteter Key %q", b.Key)
		}
	}
}

// TestBuiltinSkills hält die Regeln fest, an denen ein Skill in einer
// mitgelieferten Vorlage scheitert, ohne dass es jemandem auffiele:
//
//   - Ohne SKILL.md erkennt die Runtime das Verzeichnis nicht als Skill, der
//     Agent bekäme still gar nichts.
//   - Ohne Beschreibung entscheidet nichts darüber, ob der Skill geladen wird.
//   - Und ein Skill, auf den weder PLAYBOOKS.md noch HEARTBEAT.md zeigt, wird
//     nie gezogen: Er kostet dann dauerhaft seine Beschreibung im Kontext und
//     trägt nichts. Das ist der eigentliche Fallstrick beim Umstellen einer
//     Vorlage — der Ablauf wandert in den Skill, und der Verweis bleibt aus.
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
		verweise := bundle.Files["PLAYBOOKS.md"] + bundle.Files["HEARTBEAT.md"]
		for _, sk := range bundle.Skills {
			if sk.Files["SKILL.md"] == "" {
				t.Errorf("%s / %s: SKILL.md fehlt oder ist leer", b.Key, sk.Name)
			}
			if sk.Description == "" {
				t.Errorf("%s / %s: description fehlt", b.Key, sk.Name)
			}
			if sk.Origin != "agent" && sk.Origin != "library" {
				t.Errorf("%s / %s: origin=%q (erwartet agent oder library)", b.Key, sk.Name, sk.Origin)
			}
			if !strings.Contains(verweise, sk.Name) {
				t.Errorf("%s: auf Skill %q zeigt weder PLAYBOOKS.md noch HEARTBEAT.md — er wird nie gezogen",
					b.Key, sk.Name)
			}
		}
	}
}
