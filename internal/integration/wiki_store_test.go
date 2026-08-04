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

// TestWikiIngestRouting: ingest assigns an insight to the most similar page
// (appending) or creates a new one — the wiki's core routing logic (spec/05).
func TestWikiIngestRouting(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("route-agent")

	// First fact → new page.
	if err := s.mem.Ingest(ctx, a.ID, "Kunde ACME ist nur telefonisch erreichbar.", nil); err != nil {
		t.Fatal(err)
	}
	// Identical fact → appended to the same page (similarity 1.0 ≥ threshold).
	if err := s.mem.Ingest(ctx, a.ID, "Kunde ACME ist nur telefonisch erreichbar.", nil); err != nil {
		t.Fatal(err)
	}
	// A completely different topic → its own page.
	if err := s.mem.Ingest(ctx, a.ID, "Der Rechnungslauf startet stets am Monatsersten.", nil); err != nil {
		t.Fatal(err)
	}

	pages, err := s.mem.List(ctx, a.ID, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(pages) != 2 {
		t.Fatalf("expected 2 pages (merge + standalone), got %d: %v", len(pages), pageSlugs(pages))
	}

	// Noise is discarded — no new page.
	if err := s.mem.Ingest(ctx, a.ID, "Keine neuen Erkenntnisse", nil); err != nil {
		t.Fatal(err)
	}
	if pages, _ = s.mem.List(ctx, a.ID, 50); len(pages) != 2 {
		t.Fatalf("noise must not create a page, got %d", len(pages))
	}
}

// TestWikiConsolidate: the lint pass merges near-duplicate pages, unites their
// wikilinks and logs the merge.
func TestWikiConsolidate(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("consolidate-agent")

	// Two pages with identical title/body (⇒ similarity 1.0) but different slugs —
	// a genuine near-duplicate. (The union of *differing* link lists is checked
	// deterministically by TestUnionSlugs; the hash embedder is too sensitive to
	// reliably push two diverging bodies over the dupThreshold.)
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
		t.Fatalf("expected exactly 1 merge, got %d", n)
	}

	pages, _ := s.mem.List(ctx, a.ID, 50)
	if len(pages) != 1 {
		t.Fatalf("after the consolidation 1 page has to remain, got %d: %v", len(pages), pageSlugs(pages))
	}
	if !slices.Contains(pages[0].Links, "kollegin-zabel") || !slices.Contains(pages[0].Links, "projekt-x") {
		t.Fatalf("the wikilinks of both pages have to be united, got %+v", pages[0].Links)
	}

	// The merge is logged (log.md equivalent).
	var merges int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM wiki_log WHERE agent_id=$1 AND op='merge'`, a.ID).Scan(&merges); err != nil {
		t.Fatal(err)
	}
	if merges != 1 {
		t.Fatalf("expected 1 merge log entry, got %d", merges)
	}

	// Idempotency: a second run merges nothing any more.
	if n, _ = s.mem.Consolidate(ctx, a.ID); n != 0 {
		t.Fatalf("a second run must not merge anything any more, got %d", n)
	}
}

// TestWikiAgentIsolation: the wiki is per agent — one agent sees another's pages
// neither through List nor through Query (spec/05 scoping default).
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
		t.Fatalf("agent B must not see A's pages, got %v", pageSlugs(bPages))
	}
	hits, _ := s.mem.Query(ctx, b.ID, "Halle 7 Server", 5)
	for _, h := range hits {
		if strings.Contains(h.Content, "Halle 7") {
			t.Fatalf("B's query must not deliver A's knowledge, got %+v", h)
		}
	}
}

// TestWikiWriteUpsert: Write creates, updates the same page in place and
// extracts wikilinks from the body; an empty/meaningless body is rejected.
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
		t.Fatalf("the wikilink has to be extracted from the body, got %+v", page.Links)
	}

	// Same slug → in-place update, no second page.
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "projekt-x", Title: "Projekt X", Body: "Umsetzungsphase. Siehe [[meilenstein-2]].", Source: "agent"}); err != nil {
		t.Fatal(err)
	}
	pages, _ := s.mem.List(ctx, a.ID, 50)
	if len(pages) != 1 {
		t.Fatalf("the upsert must not create a second page, got %d", len(pages))
	}
	page, _ = s.mem.Read(ctx, a.ID, "projekt-x")
	if !strings.Contains(page.Content, "Umsetzungsphase") || slices.Contains(page.Links, "kollegin-zabel") {
		t.Fatalf("the page has to be replaced (new body, new links), got %+v", page)
	}

	// An empty/meaningless body → ErrNoContent.
	if _, err := s.mem.Write(ctx, a.ID, memory.PageInput{Slug: "leer", Title: "Leer", Body: "   ", Source: "agent"}); !errors.Is(err, memory.ErrNoContent) {
		t.Fatalf("an empty body has to return ErrNoContent, got %v", err)
	}

	// An unknown slug → pgx.ErrNoRows.
	if _, err := s.mem.Read(ctx, a.ID, "gibt-es-nicht"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("an unknown page has to return ErrNoRows, got %v", err)
	}
}

// TestWikiMaintenanceConsolidation: the periodic maintenance job (spec/05,
// independent of tasks) merges duplicates through ConsolidateWikis; a second run
// leaves the already consolidated stock untouched.
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
		t.Fatalf("the maintenance has to merge at least 1 duplicate, got %d", n)
	}
	if pages, _ := s.mem.List(ctx, a.ID, 10); len(pages) != 1 {
		t.Fatalf("after the maintenance 1 page has to remain, got %d", len(pages))
	}

	// Second run: the agent's consolidated stock stays at 1 page.
	if _, err := s.orch.ConsolidateWikis(ctx); err != nil {
		t.Fatal(err)
	}
	if pages, _ := s.mem.List(ctx, a.ID, 10); len(pages) != 1 {
		t.Fatalf("a second run must not break anything, got %d pages", len(pages))
	}
}

func pageSlugs(pages []memory.Entry) []string {
	out := make([]string, len(pages))
	for i, p := range pages {
		out[i] = p.Slug
	}
	return out
}

// TestWikiAppendKeepsPage: appending must not lose the rest of the page — before
// wiki_append, "appending" always meant rewriting the whole page.
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
		t.Fatalf("the append has to keep both, got %q", page.Content)
	}
	// References from the old and the new text are both in there afterwards.
	if !slices.Contains(page.Links, "projekt-x") || !slices.Contains(page.Links, "frau-zabel") {
		t.Fatalf("links after the append: %v", page.Links)
	}
	// The type survives the append.
	if page.Type != "kunde" {
		t.Fatalf("type lost: %q", page.Type)
	}
	// The same paragraph twice does not duplicate.
	before := page.Content
	if _, err := s.mem.Append(ctx, a.ID, "kunde-acme", "Rechnungen gehen an [[frau-zabel]]."); err != nil {
		t.Fatal(err)
	}
	if page, _ = s.mem.Read(ctx, a.ID, "kunde-acme"); page.Content != before {
		t.Fatalf("an identical paragraph must not be appended again")
	}
	// An unknown page: append creates it instead of failing.
	if _, err := s.mem.Append(ctx, a.ID, "neue-seite", "Erster Absatz einer neuen Seite."); err != nil {
		t.Fatalf("appending to an unknown page has to create it: %v", err)
	}
}

// TestWikiHealthFindings: the quality findings recognize exactly the patterns an
// agent wiki decays by (spec/05).
func TestWikiHealthFindings(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	a := s.newSupportAgent("health-agent")

	// A clean entity page with a live reference …
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
	// … a diary page without a type, without references, with a dead link and a date …
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
		t.Fatalf("page count: %d", h.Pages)
	}
	if h.DeadLinks != 1 {
		t.Fatalf("the dead reference was not recognized: %d", h.DeadLinks)
	}
	if h.Untyped != 1 {
		t.Fatalf("the page without a type was not recognized: %d", h.Untyped)
	}
	if h.Episodic != 1 {
		t.Fatalf("the diary title was not recognized: %d", h.Episodic)
	}
	// The diary page has no live reference in or out.
	if h.Orphans != 1 {
		t.Fatalf("the orphaned page was not recognized: %d", h.Orphans)
	}
	// The two linked entity pages do NOT count as orphaned.
	for _, f := range h.Findings {
		if f.Kind == "orphan" && (f.Slug == "kunde-acme" || f.Slug == "frau-zabel") {
			t.Fatalf("a linked page was wrongly reported as orphaned: %s", f.Slug)
		}
	}
	if h.Links != 2 {
		t.Fatalf("live references: %d", h.Links)
	}
}
