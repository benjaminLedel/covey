package voice

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/style"
)

var (
	ErrNotFound = errors.New("voice not found")
	ErrExists   = errors.New("a voice with this name already exists")
	// ErrInvalid is the caller's mistake — no name, no text, a document that is
	// not prose. Wrapped so the HTTP layer answers 400 rather than a 500 that
	// reads like a server fault.
	ErrInvalid = errors.New("invalid voice")
)

// maxDocumentBytes caps one uploaded text. A corpus is prose somebody wrote, not
// an archive: the whole of it is measured on every build and the exemplars are
// chosen from it.
const maxDocumentBytes = 1 << 20

// Voice is the stored object.
type Voice struct {
	ID        uuid.UUID     `json:"id"`
	OrgID     uuid.UUID     `json:"org_id"`
	Name      string        `json:"name"`
	Language  string        `json:"language"`
	Version   int           `json:"version"`
	Profile   style.Profile `json:"profile"`
	Exemplars []Exemplar    `json:"exemplars"`
	Contrast  []Contrast    `json:"contrast"`
	Notes     []string      `json:"notes"`
	// Card is the draft the last build produced, ReleasedCard what a person
	// signed. Only the released one reaches a prompt.
	Card         string     `json:"card"`
	ReleasedCard string     `json:"released_card"`
	ReleasedAt   *time.Time `json:"released_at,omitempty"`
	Words        int        `json:"words"`
	Documents    int        `json:"documents"`
	BuiltAt      *time.Time `json:"built_at,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
	// Agents are the agents carrying this voice — named, not counted, for the
	// same reason the workplaces name theirs: whoever rebuilds or deletes one
	// wants to know whom it concerns.
	Agents []AgentRef `json:"agents,omitempty"`
}

// AgentRef is an agent carrying a voice.
type AgentRef struct {
	ID          uuid.UUID `json:"id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
}

// StoredDocument is one text of the corpus, without its body.
type StoredDocument struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Kind is KindAuthor or KindReference — the author's own texts, or the AI
	// text the contrast is measured against.
	Kind      string    `json:"kind"`
	Words     int       `json:"words"`
	CreatedAt time.Time `json:"created_at"`
}

// The two poles of a corpus.
const (
	KindAuthor    = "author"
	KindReference = "reference"
)

// Built reports whether a build has run. An unbuilt voice can be assigned to
// nobody — there is nothing to write into a TONE.md.
func (v Voice) BuiltOK() bool { return v.Version > 0 && len(v.Profile.Bands) > 0 }

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Create registers a voice. It has no artefacts yet: texts are uploaded, then
// it is built.
func (s *Store) Create(ctx context.Context, orgID uuid.UUID, name, language string) (Voice, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Voice{}, fmt.Errorf("%w: a voice needs a name", ErrInvalid)
	}
	v := Voice{ID: uuid.New(), OrgID: orgID, Name: name, Language: strings.TrimSpace(language)}
	err := s.pool.QueryRow(ctx, `INSERT INTO voices (id, org_id, name, language)
		VALUES ($1,$2,$3,$4) RETURNING created_at, updated_at`,
		v.ID, v.OrgID, v.Name, v.Language).Scan(&v.CreatedAt, &v.UpdatedAt)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return Voice{}, ErrExists
	}
	if err != nil {
		return Voice{}, err
	}
	return v, nil
}

// List returns the organisation's voices, with the agents carrying each.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Voice, error) {
	rows, err := s.pool.Query(ctx, selectVoices+` WHERE v.org_id=$1 ORDER BY lower(v.name)`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Voice
	for rows.Next() {
		v, err := scanVoice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Agents, err = s.carriers(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Get one voice of this organisation.
func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Voice, error) {
	rows, err := s.pool.Query(ctx, selectVoices+` WHERE v.org_id=$1 AND v.id=$2`, orgID, id)
	if err != nil {
		return Voice{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return Voice{}, ErrNotFound
	}
	v, err := scanVoice(rows)
	if err != nil {
		return Voice{}, err
	}
	rows.Close()
	if v.Agents, err = s.carriers(ctx, v.ID); err != nil {
		return Voice{}, err
	}
	return v, nil
}

// Delete removes a voice and its corpus. The TONE.md of an agent that carried
// it stays where it is: it is a config version like any other, and deleting a
// voice must not silently change how an agent writes.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM voices WHERE org_id=$1 AND id=$2`, orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// AddDocument stores one text of the corpus.
func (s *Store) AddDocument(ctx context.Context, orgID, voiceID uuid.UUID, name, body, kind string) (StoredDocument, error) {
	if _, err := s.Get(ctx, orgID, voiceID); err != nil {
		return StoredDocument{}, err
	}
	switch kind {
	case "", KindAuthor:
		kind = KindAuthor
	case KindReference:
	default:
		return StoredDocument{}, fmt.Errorf("%w: a document is %q or %q", ErrInvalid, KindAuthor, KindReference)
	}
	name = strings.TrimSpace(name)
	body = strings.TrimSpace(body)
	if name == "" || body == "" {
		return StoredDocument{}, fmt.Errorf("%w: a document needs a name and a text", ErrInvalid)
	}
	if len(body) > maxDocumentBytes {
		return StoredDocument{}, fmt.Errorf("%w: the text is longer than %d bytes", ErrInvalid, maxDocumentBytes)
	}
	// Refused here rather than silently measured: a table of contents or a
	// changelog measures as prose with no anchors and drags every band with it.
	if !style.IsProse(body) {
		return StoredDocument{}, fmt.Errorf("%w: %q does not read as prose — a corpus is running text, "+
			"not a list or a table", ErrInvalid, name)
	}
	d := StoredDocument{ID: uuid.New(), Name: name, Kind: kind, Words: style.WordCount(body)}
	err := s.pool.QueryRow(ctx, `INSERT INTO voice_documents (id, voice_id, name, body, words, kind)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		d.ID, voiceID, d.Name, body, d.Words, d.Kind).Scan(&d.CreatedAt)
	return d, err
}

// Documents lists the corpus without the bodies — the library page shows names
// and lengths, not the texts.
func (s *Store) Documents(ctx context.Context, voiceID uuid.UUID) ([]StoredDocument, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, kind, words, created_at FROM voice_documents WHERE voice_id=$1 ORDER BY kind, created_at`, voiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredDocument
	for rows.Next() {
		var d StoredDocument
		if err := rows.Scan(&d.ID, &d.Name, &d.Kind, &d.Words, &d.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Corpus reads the texts of one pole for a build.
func (s *Store) Corpus(ctx context.Context, voiceID uuid.UUID, kind string) ([]Document, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, body FROM voice_documents WHERE voice_id=$1 AND kind=$2 ORDER BY created_at`, voiceID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Document
	for rows.Next() {
		var d Document
		if err := rows.Scan(&d.Name, &d.Text); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// DeleteDocument removes one text of the corpus. The artefacts stay as they
// were until the next build — what a voice IS is what was last built, not what
// happens to lie in its corpus right now.
func (s *Store) DeleteDocument(ctx context.Context, orgID, voiceID, docID uuid.UUID) error {
	if _, err := s.Get(ctx, orgID, voiceID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM voice_documents WHERE voice_id=$1 AND id=$2`, voiceID, docID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SaveBuild stores one pass of the build. The released card is untouched: a
// build proposes a new draft, a person decides whether it replaces the one that
// acts.
func (s *Store) SaveBuild(ctx context.Context, orgID, id uuid.UUID, b Built, card string) (Voice, error) {
	profile, err := json.Marshal(b.Profile)
	if err != nil {
		return Voice{}, err
	}
	exemplars, err := json.Marshal(b.Exemplars)
	if err != nil {
		return Voice{}, err
	}
	contrast, err := json.Marshal(orEmpty(b.Contrast))
	if err != nil {
		return Voice{}, err
	}
	notes, err := json.Marshal(orEmptyStrings(b.Notes))
	if err != nil {
		return Voice{}, err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE voices SET version=version+1, profile=$3, exemplars=$4,
		contrast=$5, notes=$6, card=$7, language=$8, words=$9, documents=$10,
		built_at=now(), updated_at=now() WHERE org_id=$1 AND id=$2`,
		orgID, id, profile, exemplars, contrast, notes, card, b.Profile.Language, b.Words, b.Docs)
	if err != nil {
		return Voice{}, err
	}
	if tag.RowsAffected() == 0 {
		return Voice{}, ErrNotFound
	}
	return s.Get(ctx, orgID, id)
}

// Release makes a card the one that acts. Passing an edited text is deliberate:
// whoever releases a description of their own hand may correct it first, and
// what they release is then what they wrote.
func (s *Store) Release(ctx context.Context, orgID, id, by uuid.UUID, card string) (Voice, error) {
	card = strings.TrimSpace(card)
	if card == "" {
		return Voice{}, fmt.Errorf("%w: an empty card releases nothing", ErrInvalid)
	}
	var byArg any = by
	if by == uuid.Nil {
		byArg = nil
	}
	tag, err := s.pool.Exec(ctx, `UPDATE voices SET released_card=$3, released_at=now(),
		released_by=$4, updated_at=now() WHERE org_id=$1 AND id=$2`, orgID, id, card, byArg)
	if err != nil {
		return Voice{}, err
	}
	if tag.RowsAffected() == 0 {
		return Voice{}, ErrNotFound
	}
	return s.Get(ctx, orgID, id)
}

// SetAgentVoice records which voice an agent carries. Writing the TONE.md is
// the caller's job — it is a config version, and versions are the registry's.
func (s *Store) SetAgentVoice(ctx context.Context, agentID uuid.UUID, voiceID *uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE agents SET voice_id=$2 WHERE id=$1`, agentID, voiceID)
	return err
}

// carriers names the agents carrying a voice.
func (s *Store) carriers(ctx context.Context, voiceID uuid.UUID) ([]AgentRef, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, slug, display_name FROM agents WHERE voice_id=$1 AND NOT killed ORDER BY display_name`, voiceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AgentRef
	for rows.Next() {
		var a AgentRef
		if err := rows.Scan(&a.ID, &a.Slug, &a.DisplayName); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

const selectVoices = `SELECT v.id, v.org_id, v.name, v.language, v.version, v.profile, v.exemplars,
	v.contrast, v.notes, v.card, v.released_card, v.released_at, v.words, v.documents,
	v.built_at, v.created_at, v.updated_at FROM voices v`

func scanVoice(rows pgx.Rows) (Voice, error) {
	var v Voice
	var profile, exemplars, contrast, notes []byte
	if err := rows.Scan(&v.ID, &v.OrgID, &v.Name, &v.Language, &v.Version, &profile, &exemplars,
		&contrast, &notes, &v.Card, &v.ReleasedCard, &v.ReleasedAt, &v.Words, &v.Documents,
		&v.BuiltAt, &v.CreatedAt, &v.UpdatedAt); err != nil {
		return Voice{}, err
	}
	_ = json.Unmarshal(profile, &v.Profile)
	_ = json.Unmarshal(exemplars, &v.Exemplars)
	_ = json.Unmarshal(contrast, &v.Contrast)
	_ = json.Unmarshal(notes, &v.Notes)
	return v, nil
}

func orEmpty(in []Contrast) []Contrast {
	if in == nil {
		return []Contrast{}
	}
	return in
}

func orEmptyStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}
