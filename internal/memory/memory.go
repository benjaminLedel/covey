// Package memory is the agents' semantic memory (spec/05): an LLM-maintained
// wiki of linked Markdown pages with a pgvector index. The page is the unit,
// wikilinks carry the relationships. Queried in the triage step, fed in the done
// step and through the covey/wiki_* tools; a consolidation pass keeps it free of
// contradictions. Graphiti (temporal graph) can be retrofitted post-wiki through
// the same interface pattern.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dim is the vector dimension of the schema (migrations/0002_memory.up.sql).
const Dim = 256

// mergeThreshold: from this cosine similarity upwards, Ingest assigns a new
// insight to an existing page instead of creating a new one.
const mergeThreshold = 0.80

// dupThreshold: from here upwards the consolidation pass considers two pages
// near-duplicates and merges them.
const dupThreshold = 0.93

// ErrNoContent: the content is empty or a stock phrase (IsNoise) — not storable.
var ErrNoContent = errors.New("no usable content")

// PageTypes is the controlled vocabulary of page types (spec/05: "one page per
// entity"). Deliberately closed and short — the kanban columns showed that
// freely invented labels proliferate within days and render the structure
// worthless. Whatever does not fit ends up in "thema".
var PageTypes = []string{"kunde", "projekt", "system", "person", "problem", "thema"}

// NormalizeType maps a type designation onto the vocabulary. Unknown and empty
// input yields "" — unassigned, a quality finding, not an error.
func NormalizeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return ""
	}
	// Catch common spellings and synonyms instead of discarding them.
	switch t {
	case "kollege", "kollegin", "mitarbeiter", "mensch", "people", "colleague":
		t = "person"
	case "kunde", "kundin", "customer", "client":
		t = "kunde"
	case "projekt", "project", "repo", "repository":
		t = "projekt"
	case "system", "tool", "werkzeug", "zielsystem", "service":
		t = "system"
	case "problem", "fehler", "bug", "loesung", "lösung", "runbook":
		t = "problem"
	case "thema", "topic", "notiz", "sonstiges":
		t = "thema"
	}
	for _, known := range PageTypes {
		if t == known {
			return t
		}
	}
	return "thema"
}

// normalizeTags drops empty entries and duplicates and lowercases.
func normalizeTags(tags []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(t, "#")))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// Embedder is the port for text→vector ("batteries included, but swappable"):
// builtin is the hash embedding (offline, but purely lexical), APIEmbedder talks
// to a real embedding provider. Name() is the model's fingerprint — it ends up
// in wiki_pages.embed_model so that ReembedStale can tell which pages have to be
// re-embedded after a switch.
type Embedder interface {
	Embed(ctx context.Context, text string) ([Dim]float32, error)
	Name() string
}

// Entry is the view onto a wiki page (Content = body, named that way for
// backwards compatibility). Score is set by Query/Search.
type Entry struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Links     []string  `json:"links,omitempty"`
	Source    string    `json:"source,omitempty"`
	Type      string    `json:"type,omitempty"`
	Tags      []string  `json:"tags,omitempty"`
	Score     float64   `json:"score,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Store struct {
	pool     *pgxpool.Pool
	embedder Embedder
}

func NewStore(pool *pgxpool.Pool, e Embedder) *Store {
	if e == nil {
		e = HashEmbedder{}
	}
	return &Store{pool: pool, embedder: e}
}

func vectorLiteral(v [Dim]float32) string {
	parts := make([]string, Dim)
	for i, f := range v {
		parts[i] = fmt.Sprintf("%g", f)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

// noisePhrases are normalized stock phrases without informational value. Only
// exact matches (after normalization) count as noise — "no new insights, but
// customer X only responds to phone calls" carries substance and is kept. The
// German phrases stay in the list: agents write their insights in the language
// of their target system, and that is what has to be recognized here.
var noisePhrases = map[string]bool{
	"keine neuen erkenntnisse": true,
	"keine erkenntnisse":       true,
	"keine neuen erkenntnis":   true,
	"nichts neues":             true,
	"nichts neues gelernt":     true,
	"nichts gelernt":           true,
	"keine besonderheiten":     true,
	"nichts zu vermerken":      true,
	"keine":                    true,
	"nichts":                   true,
	"n a":                      true,
	"na":                       true,
	"none":                     true,
	"nothing":                  true,
	"nothing new":              true,
	"no new insights":          true,
	"no new learnings":         true,
}

// IsNoise detects episodes without informational value ("No new insights",
// "n/a", "-") — storing such content makes no sense and it is discarded on
// ingest.
func IsNoise(content string) bool {
	normalized := strings.Join(tokenize(content), " ")
	return normalized == "" || noisePhrases[normalized]
}

var (
	slugStrip = regexp.MustCompile(`[^a-z0-9]+`)
	linkRe    = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)
	umlaut    = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")
)

// slugify turns a title into a filename-safe slug.
func slugify(s string) string {
	s = umlaut.Replace(strings.ToLower(strings.TrimSpace(s)))
	s = strings.Trim(slugStrip.ReplaceAllString(s, "-"), "-")
	if len(s) > 64 {
		s = strings.Trim(s[:64], "-")
	}
	if s == "" {
		return "seite"
	}
	return s
}

// truncRunes truncates rune-safely to n characters (no cut in the middle of a
// UTF-8 rune — important for umlauts and other multi-byte characters).
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// deriveTitle derives a page title from a free-form insight: first sentence, or
// the first ~80 characters, on one line. A punctuation mark only counts as the
// end of a sentence when a space or the end of the text follows — otherwise a
// dot inside a domain, an abbreviation or a decimal number (x.educa-portal.de,
// e.g., 3.5 GB) would chop the title down to a one-word fragment. On top of
// that, a first "sentence" that is too short (< minTitleLen) is skipped.
const minTitleLen = 12

func deriveTitle(content string) string {
	t := strings.Join(strings.Fields(content), " ")
	for i := 0; i < len(t) && i < 80; i++ {
		if c := t[i]; c == '.' || c == '!' || c == '?' {
			atBoundary := i+1 >= len(t) || t[i+1] == ' '
			if atBoundary && i >= minTitleLen && !isAbbrevDot(t, i) {
				t = t[:i] // punctuation is ASCII → the byte index is a rune boundary
				break
			}
		}
	}
	if len([]rune(t)) > 80 {
		t = strings.TrimSpace(truncRunes(t, 80))
	}
	return t
}

// isAbbrevDot detects a dot that belongs to an abbreviation instead of ending a
// sentence: if a single letter precedes it ("z. B.", "u. a.", "e. g."), it is
// not a sentence end. Without this check, "Zugesagte Rückmeldungen (z. B. …)"
// produced the title "Zugesagte Rückmeldungen (z".
func isAbbrevDot(t string, i int) bool {
	if t[i] != '.' {
		return false
	}
	start := i
	for start > 0 && t[start-1] != ' ' {
		start--
	}
	// The word in front of the dot, without opening brackets/quotes.
	word := strings.TrimLeft(t[start:i], "(\"'„«[")
	return len([]rune(word)) <= 1
}

// extractLinks reads the [[slug]] wikilinks out of a body.
func extractLinks(body string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, m := range linkRe.FindAllStringSubmatch(body, -1) {
		s := slugify(m[1])
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

func (s *Store) logOp(ctx context.Context, agentID uuid.UUID, op, slug, summary string) {
	_, _ = s.pool.Exec(ctx, `INSERT INTO wiki_log (agent_id, op, page_slug, summary) VALUES ($1,$2,$3,$4)`,
		agentID, op, slug, summary)
}

// uniqueSlug guarantees uniqueness per agent (slug collisions between different
// pages with similar titles).
func (s *Store) uniqueSlug(ctx context.Context, agentID uuid.UUID, base string) string {
	base = slugify(base)
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE agent_id=$1 AND slug=$2)`,
		agentID, base).Scan(&exists); err == nil && !exists {
		return base
	}
	return base + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:6]
}

// Ingest assigns a free-form insight to the matching wiki page: if a
// sufficiently similar page exists, the insight is appended there and the page
// is re-embedded; otherwise a new page is created. Empty and vacuous content
// (IsNoise) is discarded silently.
func (s *Store) Ingest(ctx context.Context, agentID uuid.UUID, content string, metadata map[string]string) error {
	content = strings.TrimSpace(content)
	if content == "" || IsNoise(content) {
		return nil
	}
	source := "agent"
	if metadata != nil && metadata["source"] != "" {
		source = metadata["source"]
	}
	vec, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("embedding: %w", err)
	}

	// Look for a matching page (only this agent's pages).
	var (
		pid   uuid.UUID
		slug  string
		body  string
		score float64
	)
	err = s.pool.QueryRow(ctx, `SELECT id, slug, body, 1 - (embedding <=> $2::vector) AS score
		FROM wiki_pages WHERE agent_id=$1 AND embed_model=$3
		ORDER BY embedding <=> $2::vector LIMIT 1`,
		agentID, vectorLiteral(vec), s.embedder.Name()).Scan(&pid, &slug, &body, &score)
	switch {
	case err == nil && score >= mergeThreshold:
		merged := strings.TrimSpace(body) + "\n\n" + content
		links := extractLinks(merged)
		mv, err := s.embedder.Embed(ctx, merged)
		if err != nil {
			return fmt.Errorf("embedding: %w", err)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE wiki_pages
			SET body=$2, links=$3, embedding=$4::vector, embed_model=$5, updated_at=now() WHERE id=$1`,
			pid, merged, links, vectorLiteral(mv), s.embedder.Name()); err != nil {
			return err
		}
		s.logOp(ctx, agentID, "ingest", slug, "appended: "+deriveTitle(content))
		return nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return err
	}

	// New page.
	title := deriveTitle(content)
	newSlug := s.uniqueSlug(ctx, agentID, title)
	meta, _ := json.Marshal(orEmpty(metadata))
	if _, err := s.pool.Exec(ctx, `INSERT INTO wiki_pages
		(id, agent_id, slug, title, body, links, source, metadata, embedding, embed_model)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::vector,$10)`,
		uuid.New(), agentID, newSlug, title, content, extractLinks(content), source, meta,
		vectorLiteral(vec), s.embedder.Name()); err != nil {
		return err
	}
	s.logOp(ctx, agentID, "ingest", newSlug, "new page: "+title)
	return nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// Query returns the most relevant pages (triage step).
func (s *Store) Query(ctx context.Context, agentID uuid.UUID, query string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 5
	}
	vec, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("embedding: %w", err)
	}
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, slug, title, body, links, source, type, tags, created_at, updated_at,
		1 - (embedding <=> $2::vector) AS score
		FROM wiki_pages WHERE agent_id=$1 AND embed_model=$4
		ORDER BY embedding <=> $2::vector LIMIT $3`,
		agentID, vectorLiteral(vec), limit, s.embedder.Name())
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// Search is the tool view (covey/wiki_search): the same vector search, meant to
// be returned to the agent.
func (s *Store) Search(ctx context.Context, agentID uuid.UUID, query string, limit int) ([]Entry, error) {
	return s.Query(ctx, agentID, query, limit)
}

// Read returns a page by slug (covey/wiki_read).
func (s *Store) Read(ctx context.Context, agentID uuid.UUID, slug string) (Entry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, slug, title, body, links, source, type, tags, created_at, updated_at, 0
		FROM wiki_pages WHERE agent_id=$1 AND slug=$2`, agentID, slugify(slug))
	if err != nil {
		return Entry{}, err
	}
	list, err := scanEntries(rows)
	if err != nil {
		return Entry{}, err
	}
	if len(list) == 0 {
		return Entry{}, pgx.ErrNoRows
	}
	return list[0], nil
}

// PageInput describes a page to be written. A struct instead of six positional
// parameters: slug, title, type and source are all strings, and a swapped order
// would not be caught at compile time.
type PageInput struct {
	Slug   string
	Title  string
	Body   string
	Source string // agent | manual
	Type   string // vocabulary see PageTypes; empty = unassigned
	Tags   []string
}

// Write creates or updates a page (covey/wiki_write, manual maintenance).
// Wikilinks are extracted from the body.
//
// Type and tags are only overwritten when they are supplied — otherwise an agent
// that merely adds text to a page would erase its classification.
func (s *Store) Write(ctx context.Context, agentID uuid.UUID, in PageInput) (Entry, error) {
	body := strings.TrimSpace(in.Body)
	if body == "" || IsNoise(body) {
		return Entry{}, ErrNoContent
	}
	slug := slugify(in.Slug)
	if slug == "seite" || slug == "" {
		slug = s.uniqueSlug(ctx, agentID, in.Title)
	}
	title := in.Title
	if strings.TrimSpace(title) == "" {
		title = deriveTitle(body)
	}
	source := in.Source
	if source == "" {
		source = "agent"
	}
	links := extractLinks(body)
	vec, err := s.embedder.Embed(ctx, title+" "+body)
	if err != nil {
		return Entry{}, fmt.Errorf("embedding: %w", err)
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO wiki_pages
		(id, agent_id, slug, title, body, links, source, type, tags, embedding, embed_model)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10::vector,$11)
		ON CONFLICT (agent_id, slug) DO UPDATE
		SET title=EXCLUDED.title, body=EXCLUDED.body, links=EXCLUDED.links,
		    type=CASE WHEN EXCLUDED.type <> '' THEN EXCLUDED.type ELSE wiki_pages.type END,
		    tags=CASE WHEN cardinality(EXCLUDED.tags) > 0 THEN EXCLUDED.tags ELSE wiki_pages.tags END,
		    embedding=EXCLUDED.embedding, embed_model=EXCLUDED.embed_model, updated_at=now()`,
		uuid.New(), agentID, slug, title, body, links, source,
		NormalizeType(in.Type), normalizeTags(in.Tags), vectorLiteral(vec), s.embedder.Name())
	if err != nil {
		return Entry{}, err
	}
	s.logOp(ctx, agentID, "write", slug, title)
	return s.Read(ctx, agentID, slug)
}

// Append adds a paragraph to an existing page (covey/wiki_append).
//
// Without it, "add to a page" always meant: read the whole page, append text,
// write the whole page back — every addition an opportunity to lose the rest of
// the page. If the page does not exist, it is created with this paragraph as its
// content.
func (s *Store) Append(ctx context.Context, agentID uuid.UUID, slug, text string) (Entry, error) {
	text = strings.TrimSpace(text)
	if text == "" || IsNoise(text) {
		return Entry{}, ErrNoContent
	}
	cur, err := s.Read(ctx, agentID, slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return s.Write(ctx, agentID, PageInput{Slug: slug, Body: text})
	}
	if err != nil {
		return Entry{}, err
	}
	if strings.Contains(cur.Content, text) {
		return cur, nil // already there — do not duplicate
	}
	merged := strings.TrimRight(cur.Content, "\n") + "\n\n" + text
	links := extractLinks(merged)
	vec, err := s.embedder.Embed(ctx, cur.Title+" "+merged)
	if err != nil {
		return Entry{}, fmt.Errorf("embedding: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE wiki_pages
		SET body=$2, links=$3, embedding=$4::vector, embed_model=$5, updated_at=now()
		WHERE id=$1`,
		cur.ID, merged, links, vectorLiteral(vec), s.embedder.Name()); err != nil {
		return Entry{}, err
	}
	s.logOp(ctx, agentID, "append", cur.Slug, "appended: "+deriveTitle(text))
	return s.Read(ctx, agentID, cur.Slug)
}

// UpdatePage replaces title and body of a page by ID (manual maintenance /
// tool edit) and re-embeds it. An empty title keeps the existing one.
// Returns pgx.ErrNoRows when the page does not exist.
func (s *Store) UpdatePage(ctx context.Context, id uuid.UUID, title, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || IsNoise(content) {
		return ErrNoContent
	}
	var curTitle, slug string
	var agentID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT agent_id, title, slug FROM wiki_pages WHERE id=$1`, id).
		Scan(&agentID, &curTitle, &slug); err != nil {
		return err
	}
	if strings.TrimSpace(title) == "" {
		title = curTitle
	}
	vec, err := s.embedder.Embed(ctx, title+" "+content)
	if err != nil {
		return fmt.Errorf("embedding: %w", err)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE wiki_pages
		SET title=$2, body=$3, links=$4, embedding=$5::vector, embed_model=$6, updated_at=now() WHERE id=$1`,
		id, title, content, extractLinks(content), vectorLiteral(vec), s.embedder.Name())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.logOp(ctx, agentID, "write", slug, "edited: "+title)
	return nil
}

// Delete removes a page for good (manual maintenance).
// PageInOrg checks whether a wiki page belongs to this organization. Pages hang
// off the agent; the organization is only settled after the join.
func (s *Store) PageInOrg(ctx context.Context, orgID, pageID uuid.UUID) bool {
	var one int
	err := s.pool.QueryRow(ctx, `SELECT 1 FROM wiki_pages p JOIN agents a ON a.id = p.agent_id
		WHERE p.id=$1 AND a.org_id=$2`, pageID, orgID).Scan(&one)
	return err == nil
}

func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	var agentID uuid.UUID
	var slug, title string
	_ = s.pool.QueryRow(ctx, `SELECT agent_id, slug, title FROM wiki_pages WHERE id=$1`, id).
		Scan(&agentID, &slug, &title)
	tag, err := s.pool.Exec(ctx, `DELETE FROM wiki_pages WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	s.logOp(ctx, agentID, "delete", slug, "deleted: "+title)
	return nil
}

// DeleteBySlug removes one of the agent's pages by its slug. Unlike Delete the
// operation is agent-scoped (WHERE agent_id) — it is the enforcement point for
// the agent tool wiki_delete, so that an agent can only delete within its own
// wiki.
func (s *Store) DeleteBySlug(ctx context.Context, agentID uuid.UUID, slug string) error {
	slug = slugify(slug)
	var title string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM wiki_pages WHERE agent_id=$1 AND slug=$2 RETURNING title`,
		agentID, slug).Scan(&title)
	if err != nil {
		return err // pgx.ErrNoRows when no page with that slug exists
	}
	s.logOp(ctx, agentID, "delete", slug, "deleted: "+title)
	return nil
}

// EmbedderName is the fingerprint of the active embedding model.
func (s *Store) EmbedderName() string { return s.embedder.Name() }

// StaleCount counts the pages that are still embedded with a model other than
// the active one.
func (s *Store) StaleCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM wiki_pages WHERE embed_model <> $1`,
		s.embedder.Name()).Scan(&n)
	return n, err
}

// ReembedStale re-embeds pages that still come from another model — switching to
// a different embedder (or the first switch away from the hash embedding) would
// otherwise render the existing index useless, because pages of different models
// cannot be compared with each other.
//
// Works in batches and aborts on the first provider error: with an expired key
// or a rate limit, carrying on is merely expensive. The call is idempotent and
// can be run again at any time. Returns how many pages were re-embedded.
func (s *Store) ReembedStale(ctx context.Context, batch int) (int, error) {
	if batch <= 0 {
		batch = 50
	}
	name := s.embedder.Name()
	done := 0
	for {
		rows, err := s.pool.Query(ctx, `SELECT id, title, body FROM wiki_pages
			WHERE embed_model <> $1 ORDER BY updated_at DESC LIMIT $2`, name, batch)
		if err != nil {
			return done, err
		}
		type page struct {
			id          uuid.UUID
			title, body string
		}
		var pages []page
		for rows.Next() {
			var p page
			if err := rows.Scan(&p.id, &p.title, &p.body); err != nil {
				rows.Close()
				return done, err
			}
			pages = append(pages, p)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return done, err
		}
		if len(pages) == 0 {
			return done, nil
		}
		for _, p := range pages {
			vec, err := s.embedder.Embed(ctx, p.title+" "+p.body)
			if err != nil {
				return done, fmt.Errorf("embedding %q: %w", p.title, err)
			}
			if _, err := s.pool.Exec(ctx, `UPDATE wiki_pages
				SET embedding=$2::vector, embed_model=$3 WHERE id=$1`,
				p.id, vectorLiteral(vec), name); err != nil {
				return done, err
			}
			done++
		}
	}
}

// ── Quality ─────────────────────────────────────────────────────────────────

// Finding is a quality finding about an agent's wiki.
type Finding struct {
	Kind    string   `json:"kind"`             // orphan | dead_link | untyped | episodic | duplicate | stub
	Slug    string   `json:"slug"`             // the page concerned
	Title   string   `json:"title,omitempty"`  // display name of the page
	Detail  string   `json:"detail,omitempty"` // target slug, partner page or similar
	Score   float64  `json:"score,omitempty"`  // duplicate only: similarity
	Related []string `json:"related,omitempty"`
}

// Health is the quality view onto a wiki: the metrics for a first impression,
// the findings to put next to them.
type Health struct {
	Pages     int       `json:"pages"`
	Links     int       `json:"links"`
	Orphans   int       `json:"orphans"`
	DeadLinks int       `json:"dead_links"`
	Untyped   int       `json:"untyped"`
	Episodic  int       `json:"episodic"`
	Duplicate int       `json:"duplicate"`
	Stubs     int       `json:"stubs"`
	Findings  []Finding `json:"findings"`
}

// episodicTitleLen is deriveTitle's truncation limit: a title that reaches it is
// by construction a cut-off sentence, not the name of an entity.
//
// Deliberately exactly this limit and not "anything above 60": measured against
// real data, 25 out of 43 titles sit exactly on the truncation (auto-generated),
// while the threshold of 60 also caught usable headings like "Sandbox: PHP 8.2
// vorhanden — Laravel-Queries ohne das educa-Repo prüfen". A finding that flags
// 88 % of the pages no longer tells anybody where to intervene.
const episodicTitleLen = 80

// stubBodyLen: below this, a page of its own carries nothing that would not be
// better placed as a paragraph on an entity page.
const stubBodyLen = 120

// dupFindingThreshold deliberately sits below dupThreshold: the consolidation
// pass only merges automatically from dupThreshold upwards, but a human look is
// already worthwhile just below it.
const dupFindingThreshold = 0.88

// isEpisodicTitle detects titles that name an event instead of an entity: too
// long, or carrying a concrete date.
var dateInTitle = regexp.MustCompile(`\d{1,2}[./-]\d{1,2}[./-]\d{2,4}|\d{4}-\d{2}-\d{2}`)

func isEpisodicTitle(title string) bool {
	t := strings.TrimSpace(title)
	return len([]rune(t)) >= episodicTitleLen || dateInTitle.MatchString(t)
}

// NeedsRetitle says whether a page should be handed to the title pass of the
// wiki maintenance. Deliberately the same heuristic as the episodic finding:
// what the quality bar shows as a diary title is exactly what the pass is meant
// to clean up — two thresholds for the same thing would drift apart.
func NeedsRetitle(title string) bool { return isEpisodicTitle(title) }

// CheckHealth collects the quality findings of a wiki (spec/05). Read-only — the
// findings are a basis for decisions, nothing is changed automatically.
func (s *Store) CheckHealth(ctx context.Context, agentID uuid.UUID) (Health, error) {
	pages, err := s.List(ctx, agentID, 5000)
	if err != nil {
		return Health{}, err
	}
	h := Health{Pages: len(pages), Findings: []Finding{}}
	exists := map[string]Entry{}
	for _, p := range pages {
		exists[p.Slug] = p
	}
	inbound := map[string]int{}
	for _, p := range pages {
		for _, l := range p.Links {
			if _, ok := exists[l]; ok {
				inbound[l]++
			}
		}
	}

	add := func(f Finding) { h.Findings = append(h.Findings, f) }
	for _, p := range pages {
		live := 0
		for _, l := range p.Links {
			if _, ok := exists[l]; ok {
				live++
				continue
			}
			h.DeadLinks++
			add(Finding{Kind: "dead_link", Slug: p.Slug, Title: p.Title, Detail: l})
		}
		h.Links += live
		if live == 0 && inbound[p.Slug] == 0 {
			h.Orphans++
			add(Finding{Kind: "orphan", Slug: p.Slug, Title: p.Title})
		}
		if p.Type == "" {
			h.Untyped++
			add(Finding{Kind: "untyped", Slug: p.Slug, Title: p.Title})
		}
		if isEpisodicTitle(p.Title) {
			h.Episodic++
			add(Finding{Kind: "episodic", Slug: p.Slug, Title: p.Title})
		}
		if len([]rune(strings.TrimSpace(p.Content))) < stubBodyLen {
			h.Stubs++
			add(Finding{Kind: "stub", Slug: p.Slug, Title: p.Title})
		}
	}

	// Suspected duplicates via the vector index — only within the same model,
	// vectors of different models are not comparable.
	rows, err := s.pool.Query(ctx, `SELECT a.slug, a.title, b.slug, b.title,
		1 - (a.embedding <=> b.embedding) AS score
		FROM wiki_pages a JOIN wiki_pages b
		  ON a.agent_id=b.agent_id AND a.id < b.id
		WHERE a.agent_id=$1 AND a.embed_model=$3 AND b.embed_model=$3
		  AND 1 - (a.embedding <=> b.embedding) >= $2
		ORDER BY score DESC LIMIT 50`, agentID, dupFindingThreshold, s.embedder.Name())
	if err != nil {
		return h, err
	}
	defer rows.Close()
	for rows.Next() {
		var aSlug, aTitle, bSlug, bTitle string
		var score float64
		if err := rows.Scan(&aSlug, &aTitle, &bSlug, &bTitle, &score); err != nil {
			return h, err
		}
		h.Duplicate++
		add(Finding{Kind: "duplicate", Slug: aSlug, Title: aTitle,
			Detail: bTitle, Score: score, Related: []string{bSlug}})
	}
	return h, rows.Err()
}

// LogEntry is an entry of the wiki log (log.md equivalent, spec/05).
type LogEntry struct {
	ID        int64     `json:"id"`
	Op        string    `json:"op"`
	PageSlug  string    `json:"page_slug,omitempty"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// Log returns an agent's chronological wiki log (newest first).
func (s *Store) Log(ctx context.Context, agentID uuid.UUID, limit int) ([]LogEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id, op, coalesce(page_slug,''), summary, created_at
		FROM wiki_log WHERE agent_id=$1 ORDER BY created_at DESC, id DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogEntry{}
	for rows.Next() {
		var e LogEntry
		if err := rows.Scan(&e.ID, &e.Op, &e.PageSlug, &e.Summary, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// List returns the most recently changed pages (index.md view, UI).
func (s *Store) List(ctx context.Context, agentID uuid.UUID, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, slug, title, body, links, source, type, tags, created_at, updated_at, 0
		FROM wiki_pages WHERE agent_id=$1 ORDER BY updated_at DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// Consolidate is the lint/consolidation pass (spec/05): near-duplicate pages are
// merged. Returns the number of merges.
func (s *Store) Consolidate(ctx context.Context, agentID uuid.UUID) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id, b.id
		FROM wiki_pages a JOIN wiki_pages b
		  ON a.agent_id=b.agent_id AND a.id < b.id
		WHERE a.agent_id=$1 AND 1 - (a.embedding <=> b.embedding) >= $2
		  AND a.embed_model=$3 AND b.embed_model=$3
		ORDER BY a.id`, agentID, dupThreshold, s.embedder.Name())
	if err != nil {
		return 0, err
	}
	var pairs [][2]uuid.UUID
	for rows.Next() {
		var keep, drop uuid.UUID
		if err := rows.Scan(&keep, &drop); err != nil {
			rows.Close()
			return 0, err
		}
		pairs = append(pairs, [2]uuid.UUID{keep, drop})
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	merged := 0
	gone := map[uuid.UUID]bool{}
	for _, p := range pairs {
		keep, drop := p[0], p[1]
		if gone[keep] || gone[drop] {
			continue
		}
		if err := s.mergePages(ctx, agentID, keep, drop); err != nil {
			return merged, err
		}
		gone[drop] = true
		merged++
	}
	return merged, nil
}

func (s *Store) mergePages(ctx context.Context, agentID, keep, drop uuid.UUID) error {
	var keepBody, dropBody, keepSlug string
	var keepLinks, dropLinks []string
	if err := s.pool.QueryRow(ctx, `SELECT body, links, slug FROM wiki_pages WHERE id=$1`, keep).
		Scan(&keepBody, &keepLinks, &keepSlug); err != nil {
		return err
	}
	if err := s.pool.QueryRow(ctx, `SELECT body, links FROM wiki_pages WHERE id=$1`, drop).
		Scan(&dropBody, &dropLinks); err != nil {
		return err
	}
	body := strings.TrimSpace(keepBody)
	if extra := strings.TrimSpace(dropBody); extra != "" && !strings.Contains(body, extra) {
		body = body + "\n\n" + extra
	}
	links := unionSlugs(keepLinks, dropLinks, extractLinks(body))
	vec, err := s.embedder.Embed(ctx, body)
	if err != nil {
		return fmt.Errorf("embedding: %w", err)
	}
	if _, err := s.pool.Exec(ctx, `UPDATE wiki_pages
		SET body=$2, links=$3, embedding=$4::vector, embed_model=$5, updated_at=now() WHERE id=$1`,
		keep, body, links, vectorLiteral(vec), s.embedder.Name()); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM wiki_pages WHERE id=$1`, drop); err != nil {
		return err
	}
	s.logOp(ctx, agentID, "merge", keepSlug, "duplicate merged")
	return nil
}

func unionSlugs(lists ...[]string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, l := range lists {
		for _, x := range l {
			if x != "" && !seen[x] {
				seen[x] = true
				out = append(out, x)
			}
		}
	}
	return out
}

func scanEntries(rows pgx.Rows) ([]Entry, error) {
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Slug, &e.Title, &e.Content, &e.Links,
			&e.Source, &e.Type, &e.Tags, &e.CreatedAt, &e.UpdatedAt, &e.Score); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FormatForPrompt turns hits into the wiki context block for triage.
func FormatForPrompt(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Relevant from your wiki\n\n")
	for _, e := range entries {
		b.WriteString("### ")
		b.WriteString(strings.TrimSpace(e.Title))
		b.WriteString(" [[")
		b.WriteString(e.Slug)
		b.WriteString("]]\n")
		body := strings.TrimSpace(e.Content)
		if len([]rune(body)) > 600 {
			body = strings.TrimSpace(truncRunes(body, 600)) + " …"
		}
		b.WriteString(body)
		b.WriteString("\n")
		if len(e.Links) > 0 {
			b.WriteString("Related: ")
			for i, l := range e.Links {
				if i > 0 {
					b.WriteString(", ")
				}
				b.WriteString("[[" + l + "]]")
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// FormatIndexForPrompt turns the pages into the compact wiki index (title +
// slug) for the triage context: this way the agent sees its entire body of
// knowledge at a glance, not just the vector hits — which helps it navigate and
// avoid duplicates.
func FormatIndexForPrompt(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Your wiki (index)\n\n")
	b.WriteString("You already have these pages — extend an existing one (covey/wiki_read + wiki_write) instead of duplicating:\n")
	for _, e := range entries {
		b.WriteString("- [[" + e.Slug + "]] — " + strings.TrimSpace(e.Title) + "\n")
	}
	return b.String()
}
