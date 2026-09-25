// Package notes is the notetaker's store (#336, spec/14): what a person
// captures for themselves — a typed note, a voice note, a meeting.
//
// Every query is scoped to one seat. There is no org-wide read and no role
// that widens it: a note is the person's, and the first thing spec/14 says
// about the brain dump is that it is private by default. Sharing it with the
// person's agents is a later step and not a column here — a flag that nothing
// reads would promise what the platform does not do.
package notes

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound: no note with this id belongs to this seat — the same answer
// whether it does not exist or is somebody else's.
var ErrNotFound = errors.New("note not found")

// ErrInvalid: the note as given cannot be stored.
var ErrInvalid = errors.New("invalid note")

const (
	KindText    = "text"
	KindVoice   = "voice"
	KindMeeting = "meeting"

	// MaxBody bounds one note. Two hours of meeting come to roughly 120 000
	// characters of transcript; this leaves room and still stops a paste of
	// a database dump.
	MaxBody  = 400_000
	maxTitle = 200
)

type Note struct {
	ID              uuid.UUID `json:"id"`
	Kind            string    `json:"kind"`
	Title           string    `json:"title"`
	Body            string    `json:"body"`
	Summary         string    `json:"summary"`
	DurationSeconds int       `json:"duration_seconds"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, kind, title, body, summary, duration_seconds, created_at, updated_at`

func scan(row pgx.Row) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Summary, &n.DurationSeconds, &n.CreatedAt, &n.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Note{}, ErrNotFound
	}
	return n, err
}

func validKind(k string) bool { return k == KindText || k == KindVoice || k == KindMeeting }

func clean(title, body string) (string, string, error) {
	title, body = strings.TrimSpace(title), strings.TrimSpace(body)
	if body == "" || len(body) > MaxBody || len([]rune(title)) > maxTitle {
		return "", "", ErrInvalid
	}
	return title, body, nil
}

// Create stores a note for the seat.
func (s *Store) Create(ctx context.Context, orgID, humanID uuid.UUID, kind, title, body string, duration int) (Note, error) {
	if !validKind(kind) || duration < 0 {
		return Note{}, ErrInvalid
	}
	title, body, err := clean(title, body)
	if err != nil {
		return Note{}, err
	}
	return scan(s.pool.QueryRow(ctx, `INSERT INTO human_notes (id, org_id, human_id, kind, title, body, duration_seconds)
		VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING `+cols, uuid.New(), orgID, humanID, kind, title, body, duration))
}

// List returns the seat's notes, newest first. q narrows to notes whose
// title, text or summary contain it.
func (s *Store) List(ctx context.Context, humanID uuid.UUID, q string, limit int) ([]Note, error) {
	q = strings.TrimSpace(q)
	rows, err := s.pool.Query(ctx, `SELECT `+cols+` FROM human_notes
		WHERE human_id=$1 AND ($2 = '' OR title ILIKE '%'||$2||'%' OR body ILIKE '%'||$2||'%' OR summary ILIKE '%'||$2||'%')
		ORDER BY created_at DESC LIMIT $3`, humanID, escapeLike(q), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// Get returns one note of the seat.
func (s *Store) Get(ctx context.Context, humanID, id uuid.UUID) (Note, error) {
	return scan(s.pool.QueryRow(ctx, `SELECT `+cols+` FROM human_notes WHERE id=$1 AND human_id=$2`, id, humanID))
}

// Update changes title and text; nil leaves a field as it is.
func (s *Store) Update(ctx context.Context, humanID, id uuid.UUID, title, body *string) (Note, error) {
	cur, err := s.Get(ctx, humanID, id)
	if err != nil {
		return Note{}, err
	}
	t, b := cur.Title, cur.Body
	if title != nil {
		t = *title
	}
	if body != nil {
		b = *body
	}
	t, b, err = clean(t, b)
	if err != nil {
		return Note{}, err
	}
	return scan(s.pool.QueryRow(ctx, `UPDATE human_notes SET title=$3, body=$4, updated_at=now()
		WHERE id=$1 AND human_id=$2 RETURNING `+cols, id, humanID, t, b))
}

// SetSummary stores what "Summarise" wrote.
func (s *Store) SetSummary(ctx context.Context, humanID, id uuid.UUID, summary string) (Note, error) {
	return scan(s.pool.QueryRow(ctx, `UPDATE human_notes SET summary=$3, updated_at=now()
		WHERE id=$1 AND human_id=$2 RETURNING `+cols, id, humanID, strings.TrimSpace(summary)))
}

// Delete removes a note of the seat.
func (s *Store) Delete(ctx context.Context, humanID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM human_notes WHERE id=$1 AND human_id=$2`, id, humanID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// escapeLike keeps % and _ in a search term literal.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}
