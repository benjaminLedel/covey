package integration

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"covey/internal/memory"
)

// TestWikiIngestRouting: Ingest ordnet eine Erkenntnis der ähnlichsten Seite zu
// (anhängen) oder legt eine neue an — die Kern-Routing-Logik des Wikis (spec/05).
func TestWikiIngestRouting(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("route-agent")

	// Erster Fakt → neue Seite.
	if err := s.mem.Ingest(ctx, a.ID, "Kunde ACME ist nur telefonisch erreichbar.", nil); err != nil {
		t.Fatal(err)
	}
	// Identischer Fakt → derselben Seite angehängt (Ähnlichkeit 1.0 ≥ Schwelle).
	if err := s.mem.Ingest(ctx, a.ID, "Kunde ACME ist nur telefonisch erreichbar.", nil); err != nil {
		t.Fatal(err)
	}
	// Ganz anderes Thema → eigenständige Seite.
	if err := s.mem.Ingest(ctx, a.ID, "Der Rechnungslauf startet stets am Monatsersten.", nil); err != nil {
		t.Fatal(err)
	}

	pages, err := s.mem.List(ctx, a.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("erwartet 2 Seiten (Merge + eigenständig), got %d: %v", len(pages), pageSlugs(pages))
	}

	// Noise wird verworfen — keine neue Seite.
	if err := s.mem.Ingest(ctx, a.ID, "Keine neuen Erkenntnisse", nil); err != nil {
		t.Fatal(err)
	}
	if pages, _ = s.mem.List(ctx, a.ID, 50); len(pages) != 2 {
		t.Fatalf("Noise darf keine Seite anlegen, got %d", len(pages))
	}
}

// TestWikiConsolidate: der Lint-Pass verschmilzt Beinahe-Duplikat-Seiten,
// vereint deren Wikilinks und protokolliert die Verschmelzung.
func TestWikiConsolidate(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("consolidate-agent")

	// Zwei Seiten mit identischem Titel/Body (⇒ Ähnlichkeit 1.0), aber
	// verschiedenen Slugs — ein echtes Beinahe-Duplikat. (Die Vereinigung
	// *unterschiedlicher* Linklisten prüft TestUnionSlugs deterministisch; der
	// Hash-Embedder ist zu empfindlich, um zwei abweichende Bodies verlässlich
	// über die dupThreshold zu bringen.)
	const body = "Kunde ACME ist nur telefonisch erreichbar. Betreut von [[kollegin-zabel]], siehe [[projekt-x]]."
	if _, err := s.mem.Write(ctx, a.ID, "kunde-acme", "Kunde ACME", body, "agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mem.Write(ctx, a.ID, "acme-alt", "Kunde ACME", body, "agent"); err != nil {
		t.Fatal(err)
	}

	n, err := s.mem.Consolidate(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("erwartet genau 1 Verschmelzung, got %d", n)
	}

	pages, _ := s.mem.List(ctx, a.ID, 50)
	if len(pages) != 1 {
		t.Fatalf("nach Konsolidierung muss 1 Seite bleiben, got %d: %v", len(pages), pageSlugs(pages))
	}
	if !slices.Contains(pages[0].Links, "kollegin-zabel") || !slices.Contains(pages[0].Links, "projekt-x") {
		t.Fatalf("Wikilinks beider Seiten müssen vereint sein, got %+v", pages[0].Links)
	}

	// Verschmelzung ist protokolliert (log.md-Äquivalent).
	var merges int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM wiki_log WHERE agent_id=$1 AND op='merge'`, a.ID).Scan(&merges); err != nil {
		t.Fatal(err)
	}
	if merges != 1 {
		t.Fatalf("erwartet 1 merge-Log-Eintrag, got %d", merges)
	}

	// Idempotenz: ein zweiter Lauf verschmilzt nichts mehr.
	if n, _ = s.mem.Consolidate(ctx, a.ID); n != 0 {
		t.Fatalf("zweiter Lauf darf nichts mehr verschmelzen, got %d", n)
	}
}

// TestWikiAgentIsolation: das Wiki ist pro Agent — ein Agent sieht die Seiten
// eines anderen weder per List noch per Query (spec/05 Scoping-Default).
func TestWikiAgentIsolation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("iso-a")
	b := s.newSupportAgent("iso-b")

	if err := s.mem.Ingest(ctx, a.ID, "Geheimnis von Agent A: Server steht in Halle 7.", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.mem.Ingest(ctx, b.ID, "Agent B kennt nur den Drucker im Flur.", nil); err != nil {
		t.Fatal(err)
	}

	bPages, _ := s.mem.List(ctx, b.ID, 50)
	if len(bPages) != 1 || strings.Contains(bPages[0].Content, "Halle 7") {
		t.Fatalf("Agent B darf A's Seiten nicht sehen, got %v", pageSlugs(bPages))
	}
	hits, _ := s.mem.Query(ctx, b.ID, "Halle 7 Server", 5)
	for _, h := range hits {
		if strings.Contains(h.Content, "Halle 7") {
			t.Fatalf("Query von B darf A's Wissen nicht liefern, got %+v", h)
		}
	}
}

// TestWikiWriteUpsert: Write legt an, aktualisiert dieselbe Seite in-place und
// extrahiert Wikilinks aus dem Body; leerer/nichtssagender Body wird abgelehnt.
func TestWikiWriteUpsert(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("upsert-agent")

	if _, err := s.mem.Write(ctx, a.ID, "projekt-x", "Projekt X", "Startphase. Team um [[kollegin-zabel]].", "agent"); err != nil {
		t.Fatal(err)
	}
	page, err := s.mem.Read(ctx, a.ID, "projekt-x")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(page.Links, "kollegin-zabel") {
		t.Fatalf("Wikilink muss aus dem Body extrahiert sein, got %+v", page.Links)
	}

	// Gleicher Slug → in-place-Update, keine zweite Seite.
	if _, err := s.mem.Write(ctx, a.ID, "projekt-x", "Projekt X", "Umsetzungsphase. Siehe [[meilenstein-2]].", "agent"); err != nil {
		t.Fatal(err)
	}
	pages, _ := s.mem.List(ctx, a.ID, 50)
	if len(pages) != 1 {
		t.Fatalf("Upsert darf keine zweite Seite anlegen, got %d", len(pages))
	}
	page, _ = s.mem.Read(ctx, a.ID, "projekt-x")
	if !strings.Contains(page.Content, "Umsetzungsphase") || slices.Contains(page.Links, "kollegin-zabel") {
		t.Fatalf("Seite muss ersetzt sein (neuer Body, neue Links), got %+v", page)
	}

	// Leerer/nichtssagender Body → ErrNoContent.
	if _, err := s.mem.Write(ctx, a.ID, "leer", "Leer", "   ", "agent"); !errors.Is(err, memory.ErrNoContent) {
		t.Fatalf("leerer Body muss ErrNoContent liefern, got %v", err)
	}

	// Unbekannter Slug → pgx.ErrNoRows.
	if _, err := s.mem.Read(ctx, a.ID, "gibt-es-nicht"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unbekannte Seite muss ErrNoRows liefern, got %v", err)
	}
}

func pageSlugs(pages []memory.Entry) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.Slug
	}
	return out
}
