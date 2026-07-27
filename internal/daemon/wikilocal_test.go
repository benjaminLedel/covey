package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWikiWorkingCopy prüft den Materialisieren→Bearbeiten→Zurücksyncen-Zyklus
// der Home-Arbeitskopie (spec/05) auf Dateiebene, ohne Daemon/WebSocket.
func TestWikiWorkingCopy(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wiki")
	pages := []wikiPage{
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Nur telefonisch erreichbar."},
		{Slug: "projekt-x", Title: "Projekt X", Body: "Läuft bis Q3. Siehe [[kunde-acme]]."},
	}
	snap, err := writeWikiFiles(dir, pages)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap) != 2 {
		t.Fatalf("snapshot sollte 2 Seiten haben, got %d", len(snap))
	}
	// Dateien + Index existieren.
	for _, f := range []string{"kunde-acme.md", "projekt-x.md", "index.md", "README.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("erwartete Datei %s fehlt: %v", f, err)
		}
	}

	// Frontmatter wird korrekt geparst.
	raw, _ := os.ReadFile(filepath.Join(dir, "kunde-acme.md"))
	if title, body := parseWikiFile(string(raw)); title != "Kunde ACME" || body != "Nur telefonisch erreichbar.\n" {
		t.Fatalf("parseWikiFile unerwartet: title=%q body=%q", title, body)
	}

	// Ohne Änderung: kein Rücksync.
	if edits, _ := readWikiEdits(dir, snap); len(edits) != 0 {
		t.Fatalf("unveränderte Kopie darf nichts zurücksyncen, got %+v", edits)
	}

	// Agent bearbeitet eine Seite und legt eine neue an.
	os.WriteFile(filepath.Join(dir, "kunde-acme.md"),
		[]byte("---\ntitle: Kunde ACME\n---\nJetzt auch per E-Mail erreichbar.\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "kollegin-zabel.md"),
		[]byte("---\ntitle: Kollegin Zabel\n---\nBetreut ACME.\n"), 0o600)

	edits, err := readWikiEdits(dir, snap)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range edits {
		got[e.Slug] = e.Body
	}
	if len(got) != 2 {
		t.Fatalf("erwartet 2 Änderungen (bearbeitet + neu), got %d: %+v", len(got), edits)
	}
	if got["kunde-acme"] == "" || got["kollegin-zabel"] == "" {
		t.Fatalf("geänderte/neue Seite fehlt: %+v", got)
	}
	if _, ok := got["projekt-x"]; ok {
		t.Fatalf("unveränderte Seite darf nicht zurückgesynct werden")
	}
}

func TestParseWikiFileNoFrontmatter(t *testing.T) {
	title, body := parseWikiFile("Nur Text, kein Frontmatter.")
	if title != "" || body != "Nur Text, kein Frontmatter." {
		t.Fatalf("ohne Frontmatter: title=%q body=%q", title, body)
	}
}
