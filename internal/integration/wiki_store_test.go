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
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "kunde-acme", Title: "Kunde ACME", Body: body, Source: "agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "acme-alt", Title: "Kunde ACME", Body: body, Source: "agent"}); err != nil {
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

	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "projekt-x", Title: "Projekt X", Body: "Startphase. Team um [[kollegin-zabel]].", Source: "agent"}); err != nil {
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
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "projekt-x", Title: "Projekt X", Body: "Umsetzungsphase. Siehe [[meilenstein-2]].", Source: "agent"}); err != nil {
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
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "leer", Title: "Leer", Body: "   ", Source: "agent"}); !errors.Is(err, memory.ErrNoContent) {
		t.Fatalf("leerer Body muss ErrNoContent liefern, got %v", err)
	}

	// Unbekannter Slug → pgx.ErrNoRows.
	if _, err := s.mem.Read(ctx, a.ID, "gibt-es-nicht"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("unbekannte Seite muss ErrNoRows liefern, got %v", err)
	}
}

// TestWikiMaintenanceConsolidation: der getaktete Wartungs-Job (spec/05,
// aufgabenunabhängig) verschmilzt Duplikate über ConsolidateWikis; ein zweiter
// Lauf lässt den bereits konsolidierten Bestand unangetastet.
func TestWikiMaintenanceConsolidation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("maint-agent")

	const body = "Kunde ACME ist nur telefonisch erreichbar. Siehe [[projekt-x]]."
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "p1", Title: "ACME", Body: body, Source: "agent"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "p2", Title: "ACME", Body: body, Source: "agent"}); err != nil {
		t.Fatal(err)
	}

	n, err := s.orch.ConsolidateWikis(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Fatalf("Wartung muss mindestens 1 Duplikat verschmelzen, got %d", n)
	}
	if pages, _ := s.mem.List(ctx, a.ID, 10); len(pages) != 1 {
		t.Fatalf("nach der Wartung muss 1 Seite bleiben, got %d", len(pages))
	}

	// Zweiter Lauf: der konsolidierte Bestand des Agenten bleibt bei 1 Seite.
	if _, err := s.orch.ConsolidateWikis(ctx); err != nil {
		t.Fatal(err)
	}
	if pages, _ := s.mem.List(ctx, a.ID, 10); len(pages) != 1 {
		t.Fatalf("zweiter Lauf darf nichts kaputtmachen, got %d Seiten", len(pages))
	}
}

func pageSlugs(pages []memory.Entry) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.Slug
	}
	return out
}

// TestWikiAppendKeepsPage: Ergänzen darf den Rest der Seite nicht verlieren —
// vor wiki_append hieß "ergänzen" immer, die ganze Seite neu zu schreiben.
func TestWikiAppendKeepsPage(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("append-agent")

	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{
		Slug: "kunde-acme", Title: "Kunde ACME", Type: "kunde",
		Body: "Nur telefonisch erreichbar. Siehe [[projekt-x]].", Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mem.Append(ctx, a.ID, "kunde-acme", "Rechnungen gehen an [[frau-zabel]]."); err != nil {
		t.Fatal(err)
	}
	page, err := s.mem.Read(ctx, a.ID, "kunde-acme")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(page.Content, "Nur telefonisch") || !strings.Contains(page.Content, "Rechnungen gehen") {
		t.Fatalf("Append muss beides behalten, got %q", page.Content)
	}
	// Verweise aus altem und neuem Text stehen danach beide drin.
	if !slices.Contains(page.Links, "projekt-x") || !slices.Contains(page.Links, "frau-zabel") {
		t.Fatalf("Links nach Append: %v", page.Links)
	}
	// Typ überlebt das Ergänzen.
	if page.Type != "kunde" {
		t.Fatalf("Typ verloren: %q", page.Type)
	}
	// Zweimal derselbe Absatz doppelt nicht.
	before := page.Content
	if _, err := s.mem.Append(ctx, a.ID, "kunde-acme", "Rechnungen gehen an [[frau-zabel]]."); err != nil {
		t.Fatal(err)
	}
	if page, _ = s.mem.Read(ctx, a.ID, "kunde-acme"); page.Content != before {
		t.Fatalf("identischer Absatz darf nicht erneut angehängt werden")
	}
	// Unbekannte Seite: Append legt sie an, statt zu scheitern.
	if _, err := s.mem.Append(ctx, a.ID, "neue-seite", "Erster Absatz einer neuen Seite."); err != nil {
		t.Fatalf("Append auf unbekannte Seite muss sie anlegen: %v", err)
	}
}

// TestWikiHealthFindings: die Qualitätsbefunde erkennen genau die Muster, an
// denen ein Agenten-Wiki verwahrlost (spec/05).
func TestWikiHealthFindings(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("health-agent")

	// Saubere Entitätsseite mit lebendem Verweis …
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{
		Slug: "kunde-acme", Title: "Kunde ACME", Type: "kunde", Source: "agent",
		Body: "ACME zahlt per Rechnung und ist nur telefonisch erreichbar. Betreut von [[frau-zabel]] aus dem Innendienst.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{
		Slug: "frau-zabel", Title: "Frau Zabel", Type: "person", Source: "agent",
		Body: "Innendienst, betreut [[kunde-acme]] und meldet sich nur vormittags zurück.",
	}); err != nil {
		t.Fatal(err)
	}
	// … eine Tagebuchseite ohne Typ, ohne Verweise, mit totem Link und Datum …
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{
		Slug:   "am-29-07-2026-hat-der-kunde-das-ticket-4711-geschlossen-und-nichts",
		Title:  "Am 29.07.2026 hat der Kunde das Ticket 4711 geschlossen und nichts weiter gemeldet",
		Body:   "Vorgang vom 29.07.2026, abgeschlossen. Verweist auf [[gibt-es-nicht]] und sonst nirgendwohin.",
		Source: "agent",
	}); err != nil {
		t.Fatal(err)
	}

	h, err := s.mem.CheckHealth(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	if h.Pages != 3 {
		t.Fatalf("Seitenzahl: %d", h.Pages)
	}
	if h.DeadLinks != 1 {
		t.Fatalf("toter Verweis nicht erkannt: %d", h.DeadLinks)
	}
	if h.Untyped != 1 {
		t.Fatalf("Seite ohne Typ nicht erkannt: %d", h.Untyped)
	}
	if h.Episodic != 1 {
		t.Fatalf("Tagebuch-Titel nicht erkannt: %d", h.Episodic)
	}
	// Die Tagebuchseite hat keinen lebenden Verweis hinein oder hinaus.
	if h.Orphans != 1 {
		t.Fatalf("verwaiste Seite nicht erkannt: %d", h.Orphans)
	}
	// Die beiden verlinkten Entitätsseiten gelten NICHT als verwaist.
	for _, f := range h.Findings {
		if f.Kind == "orphan" && (f.Slug == "kunde-acme" || f.Slug == "frau-zabel") {
			t.Fatalf("verlinkte Seite fälschlich als verwaist gemeldet: %s", f.Slug)
		}
	}
	if h.Links != 2 {
		t.Fatalf("lebende Verweise: %d", h.Links)
	}
}
