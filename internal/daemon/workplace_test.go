package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

/* Ein Agent stand in einer Werkstatt und bekam nicht gesagt, was darin steht.
   In einem Home lagen `tools/jdk`, `tools/jdk21` und `tools/flutter` — 2,7 GB
   Werkzeuge, die das Image seit Wochen mitbringt (#102). Das Home wird nach
   jedem Lauf zurückgeschrieben; die Doppelung kostet nicht einmal, sondern
   immer. */

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
	// Die Versionsmanager stehen sortiert — sonst sähe derselbe Arbeitsplatz
	// bei jedem Lauf anders aus, und ein Prompt, der sich ohne Grund ändert,
	// wirft den Cache der Runtime weg.
	if strings.Index(text, "fvm:") > strings.Index(text, "uv:") {
		t.Fatalf("die Versionsmanager stehen unsortiert:\n%s", text)
	}
}

// Ein fremdes Image bringt keine Beschreibung mit. Dann sagt der Prompt dazu
// nichts — statt etwas zu behaupten, was nicht stimmt.
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

// Angehängt, nicht ersetzt: Die Konfiguration des Agenten (SOUL, Playbooks)
// steht vorne, der Arbeitsplatz dahinter.
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

	// Und ohne Konfiguration steht er allein da, statt mit zwei Leerzeilen zu
	// beginnen.
	if got := withWorkplace(""); !strings.HasPrefix(got, "## Your workplace") {
		t.Fatalf("ohne Prompt beginnt es mit Leerraum: %q", got)
	}
}
