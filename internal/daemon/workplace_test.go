package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* An agent stood in a workshop and was not told what stands in it.
   In one home lay `tools/jdk`, `tools/jdk21` and `tools/flutter` — 2.7 GB
   of tools that the image has shipped for weeks (#102). The home is written
   back after every run; the duplication costs not once, but
   always. */

func schreibeArbeitsplatz(t *testing.T, inhalt string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "workplace.json")
	if err := os.WriteFile(p, []byte(inhalt), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDerArbeitsplatzNenntSeineWerkzeuge(t *testing.T) {
	p := schreibeArbeitsplatz(t, `{
	  "profile":"dev",
	  "summary":"Die Werkbank.",
	  "tools":[{"name":"php","version":"8.2","note":"mit pcov"},{"name":"openjdk","version":"21"}],
	  "sdk_dirs":{"fvm":"~/fvm — Flutter-SDKs","uv":"~/.local/share/uv"},
	  "notes":["Kein root, kein apt."]
	}`)
	text := readWorkplace(p)

	for _, muss := range []string{"dev", "Die Werkbank.", "php 8.2", "mit pcov", "openjdk 21", "~/fvm", "Kein root"} {
		if !strings.Contains(text, muss) {
			t.Fatalf("%q fehlt in der Beschreibung:\n%s", muss, text)
		}
	}
	// The version managers stand sorted — otherwise the same workplace would
	// look different on every run, and a prompt that changes without a reason
	// throws away the cache of the runtime.
	if strings.Index(text, "fvm:") > strings.Index(text, "uv:") {
		t.Fatalf("die Versionsmanager stehen unsortiert:\n%s", text)
	}
}

// A foreign image brings no description along. Then the prompt says nothing
// about it — instead of claiming something that is not true.
func TestOhneBeschreibungBleibtDerPromptWieErWar(t *testing.T) {
	if got := readWorkplace(filepath.Join(t.TempDir(), "gibtsnicht.json")); got != "" {
		t.Fatalf("aus dem Nichts wurde %q", got)
	}
	if got := readWorkplace(schreibeArbeitsplatz(t, "{kein json")); got != "" {
		t.Fatalf("aus kaputtem JSON wurde %q", got)
	}
	if got := readWorkplace(schreibeArbeitsplatz(t, `{"profile":""}`)); got != "" {
		t.Fatalf("aus einer leeren Beschreibung wurde %q", got)
	}
}

// Appended, not replaced: the configuration of the agent (SOUL, playbooks)
// stands in front, the workplace behind it.
func TestDerArbeitsplatzHaengtHintenAn(t *testing.T) {
	alt := workplaceSource
	workplaceSource = func() string { return "## Your workplace (dev)\n\n- php 8.2" }
	defer func() { workplaceSource = alt }()

	got := withWorkplace("Du bist Brunhilde.")
	if !strings.HasPrefix(got, "Du bist Brunhilde.") {
		t.Fatalf("die Konfiguration des Agenten steht nicht mehr vorne:\n%s", got)
	}
	if !strings.Contains(got, "php") {
		t.Fatalf("der Arbeitsplatz fehlt:\n%s", got)
	}

	// And without configuration it stands there alone, instead of
	// starting with two blank lines.
	if got := withWorkplace(""); !strings.HasPrefix(got, "## Your workplace") {
		t.Fatalf("ohne Prompt beginnt es mit Leerraum: %q", got)
	}
}
