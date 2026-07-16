// Package memory ist das episodische Gedächtnis (M7, spec/05): pgvector-Store
// mit Embedder-Port. Abgefragt im triage-Schritt, gefüttert im done-Schritt.
// Graphiti (Knowledge-Graph) kommt post-MVP über dasselbe Interface-Muster.
package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Dim ist die Vektor-Dimension des Schemas (migrations/0002_memory.up.sql).
const Dim = 256

// ErrNoContent: Inhalt ist leer oder eine Floskel (IsNoise) — nicht speicherbar.
var ErrNoContent = errors.New("kein verwertbarer inhalt")

// Embedder ist der Port für Text→Vektor. builtin ist ein deterministisches
// Hash-Embedding (offline, dependency-frei) — ehrlich begrenzt, aber für den
// MVP-Durchstich ausreichend und über dieses Interface durch einen echten
// Embedding-Provider (API) austauschbar.
type Embedder interface {
	Embed(text string) [Dim]float32
}

type Entry struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Content   string    `json:"content"`
	Score     float64   `json:"score,omitempty"`
	CreatedAt time.Time `json:"created_at"`
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

// Ingest speist eine Episode ein (done-Schritt). Leere und nichtssagende
// Inhalte (IsNoise) werden still verworfen.
func (s *Store) Ingest(ctx context.Context, agentID uuid.UUID, content string, metadata map[string]string) error {
	content = strings.TrimSpace(content)
	if content == "" || IsNoise(content) {
		return nil
	}
	if metadata == nil {
		metadata = map[string]string{}
	}
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO memories (id, agent_id, content, embedding, metadata)
		VALUES ($1,$2,$3,$4::vector,$5)`,
		uuid.New(), agentID, content, vectorLiteral(s.embedder.Embed(content)), meta)
	return err
}

// Query liefert die ähnlichsten Episoden (triage-Schritt).
func (s *Store) Query(ctx context.Context, agentID uuid.UUID, query string, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, content, created_at,
		1 - (embedding <=> $2::vector) AS score
		FROM memories WHERE agent_id=$1
		ORDER BY embedding <=> $2::vector LIMIT $3`,
		agentID, vectorLiteral(s.embedder.Embed(query)), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Content, &e.CreatedAt, &e.Score); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Update ersetzt den Inhalt einer Episode (manuelle Pflege) und berechnet
// das Embedding neu. Liefert pgx.ErrNoRows, wenn die Episode nicht existiert.
func (s *Store) Update(ctx context.Context, id uuid.UUID, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || IsNoise(content) {
		return ErrNoContent
	}
	tag, err := s.pool.Exec(ctx, `UPDATE memories SET content=$2, embedding=$3::vector WHERE id=$1`,
		id, content, vectorLiteral(s.embedder.Embed(content)))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

// Delete entfernt eine Episode endgültig (manuelle Pflege).
func (s *Store) Delete(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM memories WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}

func (s *Store) List(ctx context.Context, agentID uuid.UUID, limit int) ([]Entry, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, content, created_at, 0
		FROM memories WHERE agent_id=$1 ORDER BY created_at DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		if err := rows.Scan(&e.ID, &e.AgentID, &e.Content, &e.CreatedAt, &e.Score); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// FormatForPrompt macht aus Treffern den Memory-Block für den triage-Kontext.
func FormatForPrompt(entries []Entry) string {
	if len(entries) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Relevantes aus deinem Gedächtnis\n\n")
	for _, e := range entries {
		b.WriteString("- ")
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(e.Content), "\n", " "))
		b.WriteString("\n")
	}
	return b.String()
}
