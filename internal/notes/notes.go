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
	// ReviewDay is the day a daily review is about (#368), YYYY-MM-DD; nil
	// for every other note.
	ReviewDay *string `json:"review_day,omitempty"`
	// ReviewThrough is the end of the last session the review covers (#369).
	ReviewThrough *time.Time `json:"review_through,omitempty"`
	// Icon is an emoji; Cover a built-in gradient ("gradient:<name>") or a
	// picture of the note's media ("covey-media://<id>") (#372). Empty:
	// none.
	Icon  string `json:"icon"`
	Cover string `json:"cover"`
	// Status is empty, todo, doing or done; Due a date (YYYY-MM-DD) or nil;
	// Tags a few short words (#373).
	Status string   `json:"status"`
	Due    *string  `json:"due"`
	Tags   []string `json:"tags"`
}

// ReviewMeta is what a daily review was written from (#369).
type ReviewMeta struct {
	Lang    string    // the language it is written in
	Zone    string    // "tz:<IANA name>" or "offset:<minutes east of UTC>"
	Through time.Time // the end of the last session it covers
}

// ReviewRef is a review with what is needed to write it again.
type ReviewRef struct {
	ID      uuid.UUID
	OrgID   uuid.UUID
	Day     string
	Title   string
	Updated time.Time
	ReviewMeta
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const cols = `id, kind, title, body, summary, duration_seconds, created_at, updated_at, to_char(review_day, 'YYYY-MM-DD'), review_through, icon, cover, status, to_char(due, 'YYYY-MM-DD'), tags`

func scan(row pgx.Row) (Note, error) {
	var n Note
	err := row.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &n.Summary, &n.DurationSeconds, &n.CreatedAt, &n.UpdatedAt, &n.ReviewDay, &n.ReviewThrough, &n.Icon, &n.Cover, &n.Status, &n.Due, &n.Tags)
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

// SetReview writes the daily review of day (#368): into the seat's review
// note of that day when there is one — body replaced, an old summary
// cleared, the title kept when title is empty — otherwise into a new note.
// meta records what it was written from (#369).
func (s *Store) SetReview(ctx context.Context, orgID, humanID uuid.UUID, day time.Time, title, body string, meta ReviewMeta) (Note, error) {
	title, body, err := clean(title, body)
	if err != nil {
		return Note{}, err
	}
	var through any
	if !meta.Through.IsZero() {
		through = meta.Through
	}
	return scan(s.pool.QueryRow(ctx, `INSERT INTO human_notes
			(id, org_id, human_id, kind, title, body, review_day, review_lang, review_zone, review_through)
		VALUES ($1, $2, $3, 'text', $4, $5, $6, $7, $8, $9)
		ON CONFLICT (human_id, review_day) WHERE review_day IS NOT NULL
		DO UPDATE SET title = CASE WHEN EXCLUDED.title = '' THEN human_notes.title ELSE EXCLUDED.title END,
			body = EXCLUDED.body, summary = '', review_lang = EXCLUDED.review_lang,
			review_zone = EXCLUDED.review_zone, review_through = EXCLUDED.review_through, updated_at = now()
		RETURNING `+cols, uuid.New(), orgID, humanID, title, body, day.Format("2006-01-02"),
		meta.Lang, meta.Zone, through))
}

// ReviewsSince returns the seat's reviews of days from since on, with what
// is needed to write them again (#369).
func (s *Store) ReviewsSince(ctx context.Context, humanID uuid.UUID, since time.Time) ([]ReviewRef, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, to_char(review_day, 'YYYY-MM-DD'), title, updated_at,
			review_lang, review_zone, coalesce(review_through, 'epoch'::timestamptz)
		FROM human_notes WHERE human_id=$1 AND review_day >= $2::date ORDER BY review_day`,
		humanID, since.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReviewRef
	for rows.Next() {
		var r ReviewRef
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Day, &r.Title, &r.Updated, &r.Lang, &r.Zone, &r.Through); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Reviews maps the seat's review days (YYYY-MM-DD) to their notes and what
// they cover.
func (s *Store) Reviews(ctx context.Context, humanID uuid.UUID) (map[string]ReviewRef, error) {
	refs, err := s.ReviewsSince(ctx, humanID, time.Time{})
	if err != nil {
		return nil, err
	}
	out := make(map[string]ReviewRef, len(refs))
	for _, r := range refs {
		out[r.Day] = r
	}
	return out, nil
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
func (s *Store) Update(ctx context.Context, humanID, id uuid.UUID, p Patch) (Note, error) {
	cur, err := s.Get(ctx, humanID, id)
	if err != nil {
		return Note{}, err
	}
	t, b, icon, cover, status, tags := cur.Title, cur.Body, cur.Icon, cur.Cover, cur.Status, cur.Tags
	due := cur.Due
	if p.Title != nil {
		t = *p.Title
	}
	if p.Body != nil {
		b = *p.Body
	}
	if p.Icon != nil {
		icon = strings.TrimSpace(*p.Icon)
	}
	if p.Cover != nil {
		cover = strings.TrimSpace(*p.Cover)
	}
	if p.Status != nil {
		status = strings.TrimSpace(*p.Status)
	}
	if p.Due != nil {
		d := strings.TrimSpace(*p.Due)
		if d == "" {
			due = nil
		} else if _, err := time.Parse("2006-01-02", d); err != nil {
			return Note{}, ErrInvalid
		} else {
			due = &d
		}
	}
	if p.Tags != nil {
		if tags, err = cleanTags(*p.Tags); err != nil {
			return Note{}, err
		}
	}
	t, b, err = clean(t, b)
	if err != nil {
		return Note{}, err
	}
	if !validIcon(icon) || !validCover(cover) || !validStatus(status) {
		return Note{}, ErrInvalid
	}
	if tags == nil {
		tags = []string{}
	}
	return scan(s.pool.QueryRow(ctx, `UPDATE human_notes
		SET title=$3, body=$4, icon=$5, cover=$6, status=$7, due=$8::date, tags=$9, updated_at=now()
		WHERE id=$1 AND human_id=$2 RETURNING `+cols, id, humanID, t, b, icon, cover, status, due, tags))
}

func validStatus(s string) bool { return s == "" || s == "todo" || s == "doing" || s == "done" }

// cleanTags trims, drops empty and repeated tags; at most 10, each at most
// 30 characters.
func cleanTags(in []string) ([]string, error) {
	out := []string{}
	seen := map[string]bool{}
	for _, t := range in {
		t = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "#"))
		if t == "" || seen[strings.ToLower(t)] {
			continue
		}
		if len([]rune(t)) > 30 {
			return nil, ErrInvalid
		}
		seen[strings.ToLower(t)] = true
		out = append(out, t)
	}
	if len(out) > 10 {
		return nil, ErrInvalid
	}
	return out, nil
}

// Patch is what a change of a note sets; nil leaves a field as it is.
type Patch struct {
	Title, Body, Icon, Cover, Status, Due *string
	Tags                                  *[]string
}

// An icon is one emoji — a few code points at most (skin tones, flags,
// joined sequences) — or nothing.
func validIcon(s string) bool { return len([]rune(s)) <= 12 && !strings.ContainsAny(s, " \n\t") }

// Covers the notes offer (#372): the built-in gradients, and pictures of
// the note's media.
var gradients = map[string]bool{"clay": true, "dusk": true, "sea": true, "moss": true, "sand": true, "night": true}

func validCover(s string) bool {
	if s == "" {
		return true
	}
	if g, ok := strings.CutPrefix(s, "gradient:"); ok {
		return gradients[g]
	}
	if id, ok := strings.CutPrefix(s, MediaScheme); ok {
		_, err := uuid.Parse(id)
		return err == nil
	}
	return false
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
