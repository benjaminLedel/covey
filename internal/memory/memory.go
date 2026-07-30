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

// PageTypes ist das kontrollierte Vokabular der Seitentypen (spec/05: "eine
// Seite pro Entität"). Bewusst geschlossen und kurz — bei den Kanban-Spalten
// hat sich gezeigt, dass frei erfundene Bezeichner binnen Tagen ausufern und
// die Struktur wertlos machen. Was nicht passt, landet in "thema".
var PageTypes = []string{"kunde", "projekt", "system", "person", "problem", "thema"}

// NormalizeType bringt eine Typangabe auf das Vokabular. Unbekanntes und Leeres
// ergibt "" — nicht zugeordnet, ein Qualitätsbefund, kein Fehler.
func NormalizeType(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	if t == "" {
		return ""
	}
	// Gängige Schreibweisen und Synonyme einfangen, statt sie zu verwerfen.
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

// normalizeTags entfernt Leeres und Duplikate und schreibt klein.
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

// Embedder ist der Port für Text→Vektor ("batteries included, but swappable"):
// builtin ist das Hash-Embedding (offline, aber nur lexikalisch), APIEmbedder
// spricht einen echten Embedding-Provider. Name() ist der Fingerabdruck des
// Modells — er landet in wiki_pages.embed_model, damit ReembedStale erkennt,
// welche Seiten nach einem Wechsel neu eingebettet werden müssen.
type Embedder interface {
	Embed(ctx context.Context, text string) ([Dim]float32, error)
	Name() string
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
// bzw. die ersten ~80 Zeichen, einzeilig. Ein Satzzeichen zählt nur als
// Satzende, wenn ein Leerzeichen oder das Textende folgt — sonst würde ein
// Punkt in einer Domain, Abkürzung oder Dezimalzahl (x.educa-portal.de, z.B.,
// 3.5 GB) den Titel zum 1-Wort-Fragment zerhacken. Zusätzlich wird ein zu
// kurzer erster „Satz" (< minTitleLen) übersprungen.
const minTitleLen = 12

func deriveTitle(content string) string {
	t := strings.Join(strings.Fields(content), " ")
	for i := 0; i < len(t) && i < 80; i++ {
		if c := t[i]; c == '.' || c == '!' || c == '?' {
			atBoundary := i+1 >= len(t) || t[i+1] == ' '
			if atBoundary && i >= minTitleLen && !isAbbrevDot(t, i) {
				t = t[:i] // Satzzeichen sind ASCII → Byte-Index ist Rune-Grenze
				break
			}
		}
	}
	if len([]rune(t)) > 80 {
		t = strings.TrimSpace(truncRunes(t, 80))
	}
	return t
}

// isAbbrevDot erkennt einen Punkt, der zu einer Abkürzung gehört statt einen
// Satz zu beenden: steht davor ein einzelner Buchstabe ("z. B.", "u. a."), ist
// es kein Satzende. Ohne diese Prüfung entstand aus "Zugesagte Rückmeldungen
// (z. B. …)" der Titel "Zugesagte Rückmeldungen (z".
func isAbbrevDot(t string, i int) bool {
	if t[i] != '.' {
		return false
	}
	start := i
	for start > 0 && t[start-1] != ' ' {
		start--
	}
	// Das Wort vor dem Punkt, ohne öffnende Klammern/Anführungszeichen.
	word := strings.TrimLeft(t[start:i], "(\"'„«[")
	return len([]rune(word)) <= 1
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
	vec, err := s.embedder.Embed(ctx, content)
	if err != nil {
		return fmt.Errorf("embedding: %w", err)
	}

	// Passende Seite suchen (nur diese Agenten-Seiten).
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
		(id, agent_id, slug, title, body, links, source, metadata, embedding, embed_model)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9::vector,$10)`,
		uuid.New(), agentID, newSlug, title, content, extractLinks(content), source, meta,
		vectorLiteral(vec), s.embedder.Name()); err != nil {
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

// Search ist die Tool-Sicht (covey/wiki_search): dieselbe Vektorsuche, für die
// Rückgabe an den Agenten gedacht.
func (s *Store) Search(ctx context.Context, agentID uuid.UUID, query string, limit int) ([]Entry, error) {
	return s.Query(ctx, agentID, query, limit)
}

// Read liefert eine Seite per Slug (covey/wiki_read).
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

// PageInput beschreibt eine zu schreibende Seite. Als Struct statt sechs
// Stellungsparametern: Slug, Titel, Typ und Quelle sind allesamt Strings, und
// eine vertauschte Reihenfolge fiele beim Kompilieren nicht auf.
type PageInput struct {
	Slug   string
	Title  string
	Body   string
	Source string // agent | manual
	Type   string // Vokabular siehe PageTypes; leer = nicht zugeordnet
	Tags   []string
}

// Write legt eine Seite an oder aktualisiert sie (covey/wiki_write, manuelle
// Pflege). Wikilinks werden aus dem Body extrahiert.
//
// Typ und Tags werden nur überschrieben, wenn welche mitkommen — sonst würde
// ein Agent, der bloß den Text einer Seite ergänzt, deren Einordnung löschen.
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

// Append hängt einen Absatz an eine bestehende Seite (covey/wiki_append).
//
// Ohne das hieß "eine Seite ergänzen" immer: ganze Seite lesen, Text anhängen,
// ganze Seite zurückschreiben — bei jeder Ergänzung die Gelegenheit, den Rest
// der Seite zu verlieren. Existiert die Seite nicht, entsteht sie mit diesem
// Absatz als Inhalt.
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
		return cur, nil // schon da — nicht doppeln
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
	s.logOp(ctx, agentID, "append", cur.Slug, "ergänzt: "+deriveTitle(text))
	return s.Read(ctx, agentID, cur.Slug)
}

// UpdatePage ersetzt Titel und Body einer Seite per ID (manuelle Pflege /
// Tool-Edit) und bettet neu ein. Leerer title behält den bestehenden Titel.
// Liefert pgx.ErrNoRows, wenn die Seite nicht existiert.
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
	s.logOp(ctx, agentID, "write", slug, "bearbeitet: "+title)
	return nil
}

// Delete entfernt eine Seite endgültig (manuelle Pflege).
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
	s.logOp(ctx, agentID, "delete", slug, "gelöscht: "+title)
	return nil
}

// DeleteBySlug entfernt eine Seite des Agenten anhand ihres Slugs. Anders als
// Delete ist die Operation agent-gescopt (WHERE agent_id) — sie ist der
// Enforcement-Punkt für das Agenten-Tool wiki_delete, damit ein Agent nur im
// eigenen Wiki löschen kann.
func (s *Store) DeleteBySlug(ctx context.Context, agentID uuid.UUID, slug string) error {
	slug = slugify(slug)
	var title string
	err := s.pool.QueryRow(ctx,
		`DELETE FROM wiki_pages WHERE agent_id=$1 AND slug=$2 RETURNING title`,
		agentID, slug).Scan(&title)
	if err != nil {
		return err // pgx.ErrNoRows, wenn keine Seite mit dem Slug existiert
	}
	s.logOp(ctx, agentID, "delete", slug, "gelöscht: "+title)
	return nil
}

// EmbedderName ist der Fingerabdruck des aktiven Embedding-Modells.
func (s *Store) EmbedderName() string { return s.embedder.Name() }

// StaleCount zählt die Seiten, die noch mit einem anderen Modell eingebettet
// sind als dem aktiven.
func (s *Store) StaleCount(ctx context.Context) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM wiki_pages WHERE embed_model <> $1`,
		s.embedder.Name()).Scan(&n)
	return n, err
}

// ReembedStale bettet Seiten neu ein, die noch von einem anderen Modell stammen
// — der Umstieg auf einen anderen Embedder (oder der erste Umstieg weg vom
// Hash-Embedding) macht den bestehenden Index sonst unbrauchbar, weil Seiten
// verschiedener Modelle nicht miteinander verglichen werden können.
//
// Arbeitet in Häppchen und bricht beim ersten Provider-Fehler ab: bei einem
// abgelaufenen Schlüssel oder Rate-Limit ist Weitermachen nur teuer. Der Aufruf
// ist idempotent und kann jederzeit erneut laufen. Gibt zurück, wie viele
// Seiten neu eingebettet wurden.
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

// ── Qualität ────────────────────────────────────────────────────────────────

// Finding ist ein Qualitätsbefund über das Wiki eines Agenten.
type Finding struct {
	Kind    string   `json:"kind"`             // orphan | dead_link | untyped | episodic | duplicate | stub
	Slug    string   `json:"slug"`             // betroffene Seite
	Title   string   `json:"title,omitempty"`  // Anzeigename der Seite
	Detail  string   `json:"detail,omitempty"` // Ziel-Slug, Partnerseite o. Ä.
	Score   float64  `json:"score,omitempty"`  // nur duplicate: Ähnlichkeit
	Related []string `json:"related,omitempty"`
}

// Health ist die Qualitätssicht auf ein Wiki: die Kennzahlen fürs Erste,
// die Befunde zum Danebenlegen.
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

// episodicTitleLen ist die Kappungsgrenze von deriveTitle: ein Titel, der sie
// erreicht, ist per Konstruktion ein abgeschnittener Satz und kein Entitätsname.
//
// Bewusst genau diese Grenze und nicht "irgendwas über 60": an echten Daten
// gemessen liegen 25 von 43 Titeln exakt auf der Kappung (auto-erzeugt), während
// die 60er-Schwelle auch brauchbare Überschriften wie "Sandbox: PHP 8.2
// vorhanden — Laravel-Queries ohne das educa-Repo prüfen" eingefangen hat. Ein
// Befund, der 88 % der Seiten markiert, sagt niemandem mehr, wo anzufassen ist.
const episodicTitleLen = 80

// stubBodyLen: darunter trägt eine eigene Seite nichts, was nicht als Absatz auf
// einer Entitätsseite besser aufgehoben wäre.
const stubBodyLen = 120

// dupFindingThreshold liegt bewusst unter dupThreshold: der Konsolidierungs-Pass
// verschmilzt automatisch erst ab dupThreshold, aber schon knapp darunter lohnt
// der menschliche Blick.
const dupFindingThreshold = 0.88

// isEpisodicTitle erkennt Titel, die einen Vorgang statt einer Entität benennen:
// zu lang, oder mit einem konkreten Datum versehen.
var dateInTitle = regexp.MustCompile(`\d{1,2}[./-]\d{1,2}[./-]\d{2,4}|\d{4}-\d{2}-\d{2}`)

func isEpisodicTitle(title string) bool {
	t := strings.TrimSpace(title)
	return len([]rune(t)) >= episodicTitleLen || dateInTitle.MatchString(t)
}

// CheckHealth sammelt die Qualitätsbefunde eines Wikis (spec/05). Rein lesend —
// die Befunde sind Entscheidungsgrundlage, nichts wird automatisch geändert.
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

	// Dubletten-Verdacht über den Vektorindex — nur innerhalb desselben Modells,
	// Vektoren verschiedener Modelle sind nicht vergleichbar.
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

// LogEntry ist ein Eintrag des Wiki-Protokolls (log.md-Äquivalent, spec/05).
type LogEntry struct {
	ID        int64     `json:"id"`
	Op        string    `json:"op"`
	PageSlug  string    `json:"page_slug,omitempty"`
	Summary   string    `json:"summary"`
	CreatedAt time.Time `json:"created_at"`
}

// Log liefert das chronologische Wiki-Protokoll eines Agenten (neueste zuerst).
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

// List liefert die zuletzt geänderten Seiten (index.md-Sicht, UI).
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

// Consolidate ist der Lint-/Konsolidierungs-Pass (spec/05): Beinahe-Duplikat-
// Seiten werden verschmolzen. Gibt die Zahl der Verschmelzungen zurück.
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
			&e.Source, &e.Type, &e.Tags, &e.CreatedAt, &e.UpdatedAt, &e.Score); err != nil {
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

// FormatIndexForPrompt macht aus den Seiten den kompakten Wiki-Index (Titel +
// Slug) für den triage-Kontext: der Agent sieht so seinen gesamten Wissens-
// bestand auf einen Blick, nicht nur die Vektor-Treffer — das hilft ihm zu
// navigieren und Duplikate zu vermeiden.
func FormatIndexForPrompt(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Dein Wiki (Index)\n\n")
	b.WriteString("Diese Seiten hast du schon — ergänze eine bestehende (covey/wiki_read + wiki_write), statt zu duplizieren:\n")
	for _, e := range entries {
		b.WriteString("- [[" + e.Slug + "]] — " + strings.TrimSpace(e.Title) + "\n")
	}
	return b.String()
}
