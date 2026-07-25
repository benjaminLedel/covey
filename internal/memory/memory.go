// Package memory ist das semantische Gedächtnis der Agenten (spec/05): ein
// LLM-gepflegtes Wiki aus verlinkten Markdown-Seiten mit pgvector-Index. Die
// Seite ist die Einheit, Wikilinks tragen die Beziehungen. Abgefragt im
// triage-Schritt, gefüttert im done-Schritt und über die covey/wiki_*-Tools;
// ein Konsolidierungs-Pass hält es widerspruchsarm. Graphiti (temporaler Graph)
// kann post-Wiki über dasselbe Interface-Muster nachrüsten.
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

// Dim ist die Vektor-Dimension des Schemas (migrations/0002_memory.up.sql).
const Dim = 256

// mergeThreshold: ab dieser Kosinus-Ähnlichkeit ordnet Ingest eine neue
// Erkenntnis einer bestehenden Seite zu, statt eine neue anzulegen.
const mergeThreshold = 0.80

// dupThreshold: ab hier gelten zwei Seiten dem Konsolidierungs-Pass als
// Beinahe-Duplikate und werden verschmolzen.
const dupThreshold = 0.93

// ErrNoContent: Inhalt ist leer oder eine Floskel (IsNoise) — nicht speicherbar.
var ErrNoContent = errors.New("kein verwertbarer inhalt")

// Embedder ist der Port für Text→Vektor. builtin ist ein deterministisches
// Hash-Embedding (offline, dependency-frei) — ehrlich begrenzt, aber für den
// MVP-Durchstich ausreichend und über dieses Interface durch einen echten
// Embedding-Provider (API) austauschbar.
type Embedder interface {
	Embed(text string) [Dim]float32
}

// Entry ist die Sicht auf eine Wiki-Seite (Content = Body, für Rückwärts-
// kompatibilität so benannt). Score wird bei Query/Search gesetzt.
type Entry struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Slug      string    `json:"slug"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	Links     []string  `json:"links,omitempty"`
	Source    string    `json:"source,omitempty"`
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

// noisePhrases sind normalisierte Floskeln ohne Informationswert. Nur exakte
// Treffer (nach Normalisierung) gelten als Noise — "keine neuen Erkenntnisse,
// aber Kunde X reagiert nur auf Anrufe" enthält Substanz und bleibt erhalten.
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

// IsNoise erkennt Episoden ohne Informationswert ("Keine neuen Erkenntnisse",
// "n/a", "-") — solche Inhalte sind unsinnvoll zu speichern und werden beim
// Ingest verworfen.
func IsNoise(content string) bool {
	normalized := strings.Join(tokenize(content), " ")
	return normalized == "" || noisePhrases[normalized]
}

var (
	slugStrip = regexp.MustCompile(`[^a-z0-9]+`)
	linkRe    = regexp.MustCompile(`\[\[([^\]|]+)(?:\|[^\]]*)?\]\]`)
	umlaut    = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")
)

// slugify macht aus einem Titel einen dateinamen-tauglichen Slug.
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

// truncRunes kürzt rune-sicher auf n Zeichen (kein Schnitt mitten in einem
// UTF-8-Rune — wichtig bei Umlauten).
func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// deriveTitle zieht einen Seitentitel aus einer freien Erkenntnis: erster Satz
// bzw. die ersten ~80 Zeichen, einzeilig.
func deriveTitle(content string) string {
	t := strings.Join(strings.Fields(content), " ")
	if i := strings.IndexAny(t, ".!?\n"); i > 0 && i < 80 {
		t = t[:i] // Satzzeichen sind ASCII → Byte-Index ist Rune-Grenze
	}
	if len([]rune(t)) > 80 {
		t = strings.TrimSpace(truncRunes(t, 80))
	}
	return t
}

// extractLinks liest die [[slug]]-Wikilinks aus einem Body.
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

// uniqueSlug garantiert Eindeutigkeit pro Agent (Slug-Kollisionen bei
// unterschiedlichen Seiten mit ähnlichem Titel).
func (s *Store) uniqueSlug(ctx context.Context, agentID uuid.UUID, base string) string {
	base = slugify(base)
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM wiki_pages WHERE agent_id=$1 AND slug=$2)`,
		agentID, base).Scan(&exists); err == nil && !exists {
		return base
	}
	return base + "-" + strings.ReplaceAll(uuid.NewString(), "-", "")[:6]
}

// Ingest ordnet eine freie Erkenntnis der passenden Wiki-Seite zu: Ist eine
// hinreichend ähnliche Seite vorhanden, wird die Erkenntnis dort angehängt und
// die Seite neu eingebettet; sonst entsteht eine neue Seite. Leere und
// nichtssagende Inhalte (IsNoise) werden still verworfen.
func (s *Store) Ingest(ctx context.Context, agentID uuid.UUID, content string, metadata map[string]string) error {
	content = strings.TrimSpace(content)
	if content == "" || IsNoise(content) {
		return nil
	}
	source := "agent"
	if metadata != nil && metadata["source"] != "" {
		source = metadata["source"]
	}
	vec := s.embedder.Embed(content)

	// Passende Seite suchen (nur diese Agenten-Seiten).
	var (
		pid   uuid.UUID
		slug  string
		body  string
		score float64
	)
	err := s.pool.QueryRow(ctx, `SELECT id, slug, body, 1 - (embedding <=> $2::vector) AS score
		FROM wiki_pages WHERE agent_id=$1 ORDER BY embedding <=> $2::vector LIMIT 1`,
		agentID, vectorLiteral(vec)).Scan(&pid, &slug, &body, &score)
	switch {
	case err == nil && score >= mergeThreshold:
		merged := strings.TrimSpace(body) + "\n\n" + content
		links := extractLinks(merged)
		mv := s.embedder.Embed(merged)
		if _, err := s.pool.Exec(ctx, `UPDATE wiki_pages
			SET body=$2, links=$3, embedding=$4::vector, updated_at=now() WHERE id=$1`,
			pid, merged, links, vectorLiteral(mv)); err != nil {
			return err
		}
		s.logOp(ctx, agentID, "ingest", slug, "ergänzt: "+deriveTitle(content))
		return nil
	case err != nil && !errors.Is(err, pgx.ErrNoRows):
		return err
	}

	// Neue Seite.
	title := deriveTitle(content)
	newSlug := s.uniqueSlug(ctx, agentID, title)
	meta, _ := json.Marshal(orEmpty(metadata))
	if _, err := s.pool.Exec(ctx, `INSERT INTO wiki_pages
		(id, agent_id, slug, title, body, links, source, metadata, embedding)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::vector)`,
		uuid.New(), agentID, newSlug, title, content, extractLinks(content), source, meta, vectorLiteral(vec)); err != nil {
		return err
	}
	s.logOp(ctx, agentID, "ingest", newSlug, "neue Seite: "+title)
	return nil
}

func orEmpty(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}

// Query liefert die relevantesten Seiten (triage-Schritt).
func (s *Store) Query(ctx context.Context, agentID uuid.UUID, query string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 5
	}
	vec := s.embedder.Embed(query)
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, slug, title, body, links, source, created_at, updated_at,
		1 - (embedding <=> $2::vector) AS score
		FROM wiki_pages WHERE agent_id=$1
		ORDER BY embedding <=> $2::vector LIMIT $3`,
		agentID, vectorLiteral(vec), limit)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// Search ist die Tool-Sicht (covey/wiki_search): dieselbe Vektorsuche, für die
// Rückgabe an den Agenten gedacht.
func (s *Store) Search(ctx context.Context, agentID uuid.UUID, query string, limit int) ([]Entry, error) {
	return s.Query(ctx, agentID, query, limit)
}

// Read liefert eine Seite per Slug (covey/wiki_read).
func (s *Store) Read(ctx context.Context, agentID uuid.UUID, slug string) (Entry, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, slug, title, body, links, source, created_at, updated_at, 0
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

// Write legt eine Seite an oder aktualisiert sie (covey/wiki_write, manuelle
// Pflege). Wikilinks werden aus dem Body extrahiert.
func (s *Store) Write(ctx context.Context, agentID uuid.UUID, slug, title, body, source string) (Entry, error) {
	body = strings.TrimSpace(body)
	if body == "" || IsNoise(body) {
		return Entry{}, ErrNoContent
	}
	slug = slugify(slug)
	if slug == "seite" || slug == "" {
		slug = s.uniqueSlug(ctx, agentID, title)
	}
	if strings.TrimSpace(title) == "" {
		title = deriveTitle(body)
	}
	if source == "" {
		source = "agent"
	}
	links := extractLinks(body)
	vec := s.embedder.Embed(title + " " + body)
	_, err := s.pool.Exec(ctx, `INSERT INTO wiki_pages
		(id, agent_id, slug, title, body, links, source, embedding)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8::vector)
		ON CONFLICT (agent_id, slug) DO UPDATE
		SET title=EXCLUDED.title, body=EXCLUDED.body, links=EXCLUDED.links,
		    embedding=EXCLUDED.embedding, updated_at=now()`,
		uuid.New(), agentID, slug, title, body, links, source, vectorLiteral(vec))
	if err != nil {
		return Entry{}, err
	}
	s.logOp(ctx, agentID, "write", slug, title)
	return s.Read(ctx, agentID, slug)
}

// Update ersetzt den Body einer Seite per ID (manuelle Pflege / Tool-Edit) und
// bettet neu ein. Liefert pgx.ErrNoRows, wenn die Seite nicht existiert.
func (s *Store) Update(ctx context.Context, id uuid.UUID, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || IsNoise(content) {
		return ErrNoContent
	}
	var title string
	if err := s.pool.QueryRow(ctx, `SELECT title FROM wiki_pages WHERE id=$1`, id).Scan(&title); err != nil {
		return err
	}
	vec := s.embedder.Embed(title + " " + content)
	tag, err := s.pool.Exec(ctx, `UPDATE wiki_pages
		SET body=$2, links=$3, embedding=$4::vector, updated_at=now() WHERE id=$1`,
		id, content, extractLinks(content), vectorLiteral(vec))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Delete entfernt eine Seite endgültig (manuelle Pflege).
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM wiki_pages WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// List liefert die zuletzt geänderten Seiten (index.md-Sicht, UI).
func (s *Store) List(ctx context.Context, agentID uuid.UUID, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, slug, title, body, links, source, created_at, updated_at, 0
		FROM wiki_pages WHERE agent_id=$1 ORDER BY updated_at DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	return scanEntries(rows)
}

// Consolidate ist der Lint-/Konsolidierungs-Pass (spec/05): Beinahe-Duplikat-
// Seiten werden verschmolzen. Gibt die Zahl der Verschmelzungen zurück.
func (s *Store) Consolidate(ctx context.Context, agentID uuid.UUID) (int, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id, b.id
		FROM wiki_pages a JOIN wiki_pages b
		  ON a.agent_id=b.agent_id AND a.id < b.id
		WHERE a.agent_id=$1 AND 1 - (a.embedding <=> b.embedding) >= $2
		ORDER BY a.id`, agentID, dupThreshold)
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
	vec := s.embedder.Embed(body)
	if _, err := s.pool.Exec(ctx, `UPDATE wiki_pages
		SET body=$2, links=$3, embedding=$4::vector, updated_at=now() WHERE id=$1`,
		keep, body, links, vectorLiteral(vec)); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `DELETE FROM wiki_pages WHERE id=$1`, drop); err != nil {
		return err
	}
	s.logOp(ctx, agentID, "merge", keepSlug, "Duplikat verschmolzen")
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
			&e.Source, &e.CreatedAt, &e.UpdatedAt, &e.Score); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FormatForPrompt macht aus Treffern den Wiki-Kontextblock für triage.
func FormatForPrompt(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Relevantes aus deinem Wiki\n\n")
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
			b.WriteString("Verwandt: ")
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
