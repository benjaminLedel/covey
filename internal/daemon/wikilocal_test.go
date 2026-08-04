package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestWikiWorkingCopy checks the materialize→edit→sync-back cycle of the home
// working copy (spec/05) on the file level, without daemon/WebSocket.
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
		t.Fatalf("snapshot should hold 2 pages, got %d", len(snap))
	}
	// Files + index exist.
	for _, f := range []string{"kunde-acme.md", "projekt-x.md", "index.md", "README.txt"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			t.Fatalf("expected file %s is missing: %v", f, err)
		}
	}

	// Frontmatter is parsed correctly.
	raw, _ := os.ReadFile(filepath.Join(dir, "kunde-acme.md"))
	if pg := parseWikiFile(string(raw)); pg.Title != "Kunde ACME" || pg.Body != "Nur telefonisch erreichbar.\n" {
		t.Fatalf("parseWikiFile unexpected: title=%q body=%q", pg.Title, pg.Body)
	}

	// Without a change: no sync-back.
	if edits, _ := readWikiEdits(dir, snap); len(edits) != 0 {
		t.Fatalf("an unchanged copy must sync nothing back, got %+v", edits)
	}

	// The agent edits one page and creates a new one.
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
		t.Fatalf("expected 2 changes (edited + new), got %d: %+v", len(got), edits)
	}
	if got["kunde-acme"] == "" || got["kollegin-zabel"] == "" {
		t.Fatalf("changed/new page missing: %+v", got)
	}
	if _, ok := got["projekt-x"]; ok {
		t.Fatalf("an unchanged page must not be synced back")
	}
}

// TestWikiOrphansPruned: pages that no longer exist in the control plane vanish
// from the home while materializing — otherwise the sync-back would write them
// again (spec/05).
func TestWikiOrphansPruned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "wiki")
	if _, err := writeWikiFiles(dir, []wikiPage{
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Nur telefonisch."},
		{Slug: "dublette", Title: "Dublette", Body: "Wird gleich verschmolzen."},
	}); err != nil {
		t.Fatal(err)
	}
	// Next run: "dublette" was deleted in the control plane.
	snap, err := writeWikiFiles(dir, []wikiPage{
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Nur telefonisch."},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "dublette.md")); !os.IsNotExist(err) {
		t.Fatalf("the orphaned file dublette.md should have been removed (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "kunde-acme.md")); err != nil {
		t.Fatalf("a live page must not be removed: %v", err)
	}
	if edits, _ := readWikiEdits(dir, snap); len(edits) != 0 {
		t.Fatalf("after the cleanup nothing may be synced back, got %+v", edits)
	}
}

// TestPartitionWikiEditsKeepsDeletionsDeleted: the sync-back must not resurrect
// a page the agent deleted during the run — not even if it touched its home file
// beforehand.
func TestPartitionWikiEditsKeepsDeletionsDeleted(t *testing.T) {
	snap := map[string]string{"dublette": "hash-a", "kunde-acme": "hash-b"}
	edits := []wikiPage{
		{Slug: "dublette", Title: "Dublette", Body: "zusammengeführt in [[kunde-acme]]"},
		{Slug: "kunde-acme", Title: "Kunde ACME", Body: "Jetzt auch per E-Mail."},
		{Slug: "kollegin-zabel", Title: "Kollegin Zabel", Body: "Betreut ACME."},
	}
	live := map[string]bool{"kunde-acme": true} // "dublette" was deleted during the run

	writes, stale := partitionWikiEdits(edits, snap, live, true)
	if len(stale) != 1 || stale[0] != "dublette" {
		t.Fatalf("the deleted page should count as a dead entry, got %v", stale)
	}
	got := map[string]bool{}
	for _, w := range writes {
		got[w.Slug] = true
	}
	if len(writes) != 2 || !got["kunde-acme"] || !got["kollegin-zabel"] {
		t.Fatalf("edited and new page must be written back, got %+v", writes)
	}

	// Without a live list (fetch failed): fail-safe, write everything.
	writes, stale = partitionWikiEdits(edits, snap, nil, false)
	if len(writes) != 3 || len(stale) != 0 {
		t.Fatalf("without a live list everything must be written fail-safe, got writes=%d stale=%v", len(writes), stale)
	}
}

func TestParseWikiFileNoFrontmatter(t *testing.T) {
	pg := parseWikiFile("Nur Text, kein Frontmatter.")
	if pg.Title != "" || pg.Body != "Nur Text, kein Frontmatter." {
		t.Fatalf("without frontmatter: title=%q body=%q", pg.Title, pg.Body)
	}
}

// Type and tags must survive the round trip home → control plane: until now the
// parser discarded everything but the title, and the agent could not categorize
// a page even when it tried.
func TestWikiFrontmatterRoundTrip(t *testing.T) {
	in := wikiPage{Slug: "kunde-acme", Title: "Kunde ACME", Type: "kunde",
		Tags: []string{"abrechnung", "eskalation"}, Body: "Nur telefonisch."}
	pg := parseWikiFile(renderWikiFile(in))
	if pg.Title != in.Title || pg.Type != "kunde" {
		t.Fatalf("title/type lost: %+v", pg)
	}
	if len(pg.Tags) != 2 || pg.Tags[0] != "abrechnung" || pg.Tags[1] != "eskalation" {
		t.Fatalf("tags lost: %v", pg.Tags)
	}
	if strings.TrimSpace(pg.Body) != "Nur telefonisch." {
		t.Fatalf("body changed: %q", pg.Body)
	}
	// Without type/tags there is no empty line in the head either.
	plain := renderWikiFile(wikiPage{Title: "X", Body: "y"})
	if strings.Contains(plain, "type:") || strings.Contains(plain, "tags:") {
		t.Fatalf("empty fields do not belong in the frontmatter: %q", plain)
	}
}

// Accept the YAML list form of the tags, not only the comma form.
func TestParseWikiTagsListForm(t *testing.T) {
	pg := parseWikiFile("---\ntitle: T\ntags: [alpha, beta]\n---\nText\n")
	if len(pg.Tags) != 2 || pg.Tags[0] != "alpha" || pg.Tags[1] != "beta" {
		t.Fatalf("tags in list form: %v", pg.Tags)
	}
}
