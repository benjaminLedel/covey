package examples

import (
	"encoding/json"
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
