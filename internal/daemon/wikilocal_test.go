package daemon

import (
	"os"
	"path/filepath"
	"strings"
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
	if pg := parseWikiFile(string(raw)); pg.Title != "Kunde ACME" || pg.Body != "Nur telefonisch erreichbar.\n" {
		t.Fatalf("parseWikiFile unerwartet: title=%q body=%q", pg.Title, pg.Body)
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

// TestWikiOrphansPruned: Seiten, die es in der Control Plane nicht mehr gibt,
// verschwinden beim Materialisieren aus dem Home — sonst schreibt der Rücksync
// sie wieder an (spec/05).
func TestWikiOrphansPruned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wiki")
	if _, err := writeWikiFiles(dir, []wikiPage{
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Nur telefonisch."},
		{Slug: "dublette", Title: "Dublette", Body: "Wird gleich verschmolzen."},
	}); err != nil {
		t.Fatal(err)
	}
	// Nächster Lauf: "dublette" wurde in der Control Plane gelöscht.
	snap, err := writeWikiFiles(dir, []wikiPage{
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Nur telefonisch."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dublette.md")); !os.IsNotExist(err) {
		t.Fatalf("verwaiste Datei dublette.md hätte entfernt werden müssen (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kunde-acme.md")); err != nil {
		t.Fatalf("lebende Seite darf nicht entfernt werden: %v", err)
	}
	if edits, _ := readWikiEdits(dir, snap); len(edits) != 0 {
		t.Fatalf("nach dem Aufräumen darf nichts zurückgesynct werden, got %+v", edits)
	}
}

// TestPartitionWikiEditsKeepsDeletionsDeleted: der Rücksync darf eine Seite,
// die der Agent im Lauf gelöscht hat, nicht wieder auferstehen lassen — auch
// dann nicht, wenn er ihre Home-Datei vorher noch angefasst hat.
func TestPartitionWikiEditsKeepsDeletionsDeleted(t *testing.T) {
	snap := map[string]string{"dublette": "hash-a", "kunde-acme": "hash-b"}
	edits := []wikiPage{
		{Slug: "dublette", Title: "Dublette", Body: "zusammengeführt in [[kunde-acme]]"},
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Jetzt auch per E-Mail."},
		{Slug: "kollegin-zabel", Title: "Kollegin Zabel", Body: "Betreut ACME."},
	}
	live := map[string]bool{"kunde-acme": true} // "dublette" wurde im Lauf gelöscht

	writes, stale := partitionWikiEdits(edits, snap, live, true)
	if len(stale) != 1 || stale[0] != "dublette" {
		t.Fatalf("gelöschte Seite sollte als Karteileiche gelten, got %v", stale)
	}
	got := map[string]bool{}
	for _, w := range writes {
		got[w.Slug] = true
	}
	if len(writes) != 2 || !got["kunde-acme"] || !got["kollegin-zabel"] {
		t.Fatalf("bearbeitete und neue Seite müssen zurückgeschrieben werden, got %+v", writes)
	}

	// Ohne Live-Liste (Abruf fehlgeschlagen): fail-safe, alles schreiben.
	writes, stale = partitionWikiEdits(edits, snap, nil, false)
	if len(writes) != 3 || len(stale) != 0 {
		t.Fatalf("ohne Live-Liste muss fail-safe alles geschrieben werden, got writes=%d stale=%v", len(writes), stale)
	}
}

func TestParseWikiFileNoFrontmatter(t *testing.T) {
	pg := parseWikiFile("Nur Text, kein Frontmatter.")
	if pg.Title != "" || pg.Body != "Nur Text, kein Frontmatter." {
		t.Fatalf("ohne Frontmatter: title=%q body=%q", pg.Title, pg.Body)
	}
}

// Typ und Tags müssen die Runde Home → Control Plane überstehen: bis hierher
// verwarf der Parser alles außer dem Titel, und der Agent konnte eine Seite
// nicht einordnen, selbst wenn er es versuchte.
func TestWikiFrontmatterRoundTrip(t *testing.T) {
	in := wikiPage{Slug: "kunde-acme", Title: "Kunde ACME", Type: "kunde",
		Tags: []string{"abrechnung", "eskalation"}, Body: "Nur telefonisch."}
	pg := parseWikiFile(renderWikiFile(in))
	if pg.Title != in.Title || pg.Type != "kunde" {
		t.Fatalf("Titel/Typ verloren: %+v", pg)
	}
	if len(pg.Tags) != 2 || pg.Tags[0] != "abrechnung" || pg.Tags[1] != "eskalation" {
		t.Fatalf("Tags verloren: %v", pg.Tags)
	}
	if strings.TrimSpace(pg.Body) != "Nur telefonisch." {
		t.Fatalf("Body verändert: %q", pg.Body)
	}
	// Ohne Typ/Tags steht auch keine leere Zeile im Kopf.
	plain := renderWikiFile(wikiPage{Title: "X", Body: "y"})
	if strings.Contains(plain, "type:") || strings.Contains(plain, "tags:") {
		t.Fatalf("leere Felder gehören nicht ins Frontmatter: %q", plain)
	}
}

// YAML-Listenform der Tags akzeptieren, nicht nur die Kommaform.
func TestParseWikiTagsListForm(t *testing.T) {
	pg := parseWikiFile("---\ntitle: T\ntags: [alpha, beta]\n---\nText\n")
	if len(pg.Tags) != 2 || pg.Tags[0] != "alpha" || pg.Tags[1] != "beta" {
		t.Fatalf("Tags in Listenform: %v", pg.Tags)
	}
}
