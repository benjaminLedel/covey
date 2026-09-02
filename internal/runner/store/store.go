// Package store keeps the runners in Postgres: the row a runner authenticates
// against, and the built-in runner an organisation gets from the platform
// itself (spec/16-runner.md).
//
// Deliberately its own package next to internal/runner. That one holds the
// protocol and the runner side of it and must stay free of the database —
// on a remote host the runner is precisely the component that must not be a
// database client. internal/daemon carries the same cut for the same reason.
package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Kinds of runner. The built-in one runs inside the covey serve process and is
// created by the platform itself; a remote one has joined with a registration
// token.
const (
	KindBuiltin = "builtin"
	KindRemote  = "remote"
)

// ErrNotFound: no such runner — or not in this organisation.
var ErrNotFound = errors.New("runner not found")

// Runner is an execution node. It belongs to exactly one organisation: it holds
// homes and daemon tokens, and both are the property of one tenant (spec/16,
// "One runner, one organisation").
type Runner struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Kind        string    `json:"kind"`
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	Tags        []string  `json:"tags,omitempty"`
	// ExtraTags are what an operator assigned in the interface — ADDITIVE to
	// what the runner reports about itself. A tag says what a host IS (arm64,
	// gpu); this says what it is FOR (build, frankfurt), and it can be changed
	// without touching the machine.
	ExtraTags []string `json:"extra_tags,omitempty"`
	// AssignedImages REPLACES the claim the runner reports when it is set. nil
	// = the operator has not decided and the runner's own claim applies; an
	// empty array = no claim at all, this host provides every workplace and
	// fetches what it does not have. Without that difference a claim a
	// registration invented could never be taken back.
	AssignedImages []string `json:"assigned_images,omitempty"`
	// ImagesDecided says which of the two the empty array means.
	ImagesDecided bool `json:"images_decided"`
	// Version, Arch and Protocol are what the runner reported when it
	// connected — the basis for making version drift visible.
	Version    string     `json:"version,omitempty"`
	Arch       string     `json:"arch,omitempty"`
	Protocol   int        `json:"protocol,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	// PausedAt: this host takes no new sandboxes. Set by an operator, not by a
	// rule — a maintenance window, or the decision that the control plane's
	// own machine is not to carry compute. Everything else about the runner
	// stays: token, tags, working copies. nil = it works.
	PausedAt *time.Time `json:"paused_at,omitempty"`
	// LogLevel is what this runner is asked to report at — "info" normally,
	// "debug" while somebody is looking into a problem on this one host. It
	// lives on the row and not only in the message, so that a runner which
	// reconnects comes back at the level the interface shows.
	LogLevel string `json:"log_level"`
	// UpdateTo is the version this host is to be lifted to as soon as it
	// carries nothing. Empty = nothing planned.
	//
	// It sits on the row rather than in the process because the wait may be
	// long: an agent working for half an hour is a normal reason for a runner
	// to be busy, and a control plane that restarts in the meantime must not
	// forget what an operator asked for.
	UpdateTo        string     `json:"update_to,omitempty"`
	UpdatePlannedAt *time.Time `json:"update_planned_at,omitempty"`
}

// Paused is the question the scheduler asks; the timestamp is for the sentence
// in the interface.
func (r Runner) Paused() bool { return r.PausedAt != nil }

type Store struct {
	pool *pgxpool.Pool
	// seenAt is when each runner's last_seen_at was last written (see Seen).
	seenAt sync.Map
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// HashToken is the storage form of a runner token. Deliberately a plain SHA-256
// and not Argon2id: the token is 256 bits of entropy from a random source, so
// there is no dictionary to slow down — the cost of a KDF would buy nothing and
// would be paid on every allowlist request.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// NewToken generates a runner token.
func NewToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

const columns = `id, org_id, kind, name, description, tags, extra_tags, assigned_images, version, arch, protocol, created_at, last_seen_at, paused_at, log_level, update_to, update_planned_at`

func scan(row pgx.Row) (Runner, error) {
	var r Runner
	var images []string
	err := row.Scan(&r.ID, &r.OrgID, &r.Kind, &r.Name, &r.Description, &r.Tags, &r.ExtraTags, &images,
		&r.Version, &r.Arch, &r.Protocol, &r.CreatedAt, &r.LastSeenAt, &r.PausedAt, &r.LogLevel,
		&r.UpdateTo, &r.UpdatePlannedAt)
	if images != nil {
		r.AssignedImages, r.ImagesDecided = images, true
	}
	return r, err
}

// PlanUpdate records "lift this host to that version at the next gap", or
// clears the wish when the version is empty. Two writes and no read: whoever
// plans an update last has said the last word, and a plan that had to be read
// before it could be replaced would be a race between two operators.
func (s *Store) PlanUpdate(ctx context.Context, id uuid.UUID, version string) error {
	version = strings.TrimSpace(version)
	if version == "" {
		_, err := s.pool.Exec(ctx,
			`UPDATE runners SET update_to='', update_planned_at=NULL WHERE id=$1`, id)
		return err
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE runners SET update_to=$2, update_planned_at=now() WHERE id=$1`, id, version)
	return err
}

// PlannedUpdate is what the pool asks when a host has become idle. Empty = the
// host is to stay where it is.
func (s *Store) PlannedUpdate(ctx context.Context, id uuid.UUID) (string, error) {
	var version string
	err := s.pool.QueryRow(ctx, `SELECT update_to FROM runners WHERE id=$1`, id).Scan(&version)
	if err == pgx.ErrNoRows {
		return "", nil
	}
	return version, err
}

// Patch is what the interface may change about a runner. Every field is a
// pointer because "not sent" and "set to empty" are different answers, and for
// the images the difference is the whole feature: nil = the runner's own claim
// applies, a pointer to an empty slice = the decision "no claim at all".
type Patch struct {
	ExtraTags   *[]string
	Images      *[]string
	Name        *string
	Description *string
	// Paused: true stamps the moment, false clears it. The moment and not a
	// flag, because "since when" is the first thing anybody asks about a host
	// that is standing still.
	Paused *bool
}

// Update writes what an operator decided about a host. Tags and images were
// properties of the machine's configuration file until now, sent once at
// connect — changing them meant editing a file there and restarting the runner.
// The name was not changeable at all: a host registered as "Build host
// Frankfurt" carried that string until somebody deleted and re-registered it.
func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, p Patch) (Runner, error) {
	var extraTags, images any
	if p.ExtraTags != nil {
		tags := *p.ExtraTags
		if tags == nil {
			tags = []string{}
		}
		extraTags = tags
	}
	if p.Images != nil {
		images = *p.Images
	}
	r, err := scan(s.pool.QueryRow(ctx,
		`UPDATE runners SET
		     extra_tags      = coalesce($3, extra_tags),
		     assigned_images = CASE WHEN $4::boolean THEN $5 ELSE assigned_images END,
		     name            = coalesce($6, name),
		     description     = coalesce($7, description),
		     paused_at       = CASE
		                         WHEN $8::boolean IS NULL THEN paused_at
		                         WHEN $8::boolean THEN coalesce(paused_at, now())
		                         ELSE NULL
		                       END
		 WHERE id = $1 AND org_id = $2 RETURNING `+columns,
		id, orgID, extraTags, p.Images != nil, images, p.Name, p.Description, p.Paused))
	if errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, ErrNotFound
	}
	return r, err
}

// ByID reads one runner — for the places that have an id and need the name a
// person gave it. Not for a request: a handler that acts for an organisation
// asks with ByIDForOrg, so that a foreign id is "not found" rather than a row.
func (s *Store) ByID(ctx context.Context, id uuid.UUID) (Runner, error) {
	r, err := scan(s.pool.QueryRow(ctx, `SELECT `+columns+` FROM runners WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, ErrNotFound
	}
	return r, err
}

// ByIDForOrg reads one runner of THIS organisation. The organisation is a
// property of the runner, not of the request (spec/16, "Trust boundary"), and
// a runner of another tenant is not a runner the caller may not touch — it is
// none (#159).
func (s *Store) ByIDForOrg(ctx context.Context, orgID, id uuid.UUID) (Runner, error) {
	r, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE id = $1 AND org_id = $2`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, ErrNotFound
	}
	return r, err
}

// Capabilities is what the pool asks when a runner attaches: what an operator
// assigned to this host, independently of what the host says about itself —
// including whether it is paused, which a reconnect must not silently undo.
func (s *Store) Capabilities(ctx context.Context, id uuid.UUID) (extraTags []string, images []string, decided, paused bool, err error) {
	var raw []string
	var pausedAt *time.Time
	err = s.pool.QueryRow(ctx,
		`SELECT extra_tags, assigned_images, paused_at FROM runners WHERE id = $1`, id).Scan(&extraTags, &raw, &pausedAt)
	if err != nil {
		return nil, nil, false, false, err
	}
	if raw != nil {
		return extraTags, raw, true, pausedAt != nil, nil
	}
	return extraTags, nil, false, pausedAt != nil, nil
}

// EnsureBuiltin returns an organisation's built-in runner and creates it if it
// is missing — for organisations that came into being after migration 0051.
func (s *Store) EnsureBuiltin(ctx context.Context, orgID uuid.UUID) (Runner, error) {
	r, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE org_id = $1 AND kind = 'builtin'`, orgID))
	if err == nil {
		return r, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, err
	}
	// ON CONFLICT: two control planes on the same database may start at the
	// same time, and the partial unique index is what decides. Whoever loses
	// reads the winner's row instead of failing.
	_, err = s.pool.Exec(ctx,
		`INSERT INTO runners (id, org_id, kind) VALUES ($1, $2, 'builtin') ON CONFLICT DO NOTHING`,
		uuid.New(), orgID)
	if err != nil {
		return Runner{}, err
	}
	return scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE org_id = $1 AND kind = 'builtin'`, orgID))
}

// SetTokenHash replaces a runner's token. The built-in runner rotates its token
// on every start of the control plane.
func (s *Store) SetTokenHash(ctx context.Context, id uuid.UUID, hash string) error {
	_, err := s.pool.Exec(ctx, `UPDATE runners SET token_hash = $2 WHERE id = $1`, id, hash)
	return err
}

// ByToken resolves a runner token to its runner. An empty hash never matches:
// that is the state of a runner whose token has not been set yet, and it must
// not be reachable with an empty Authorization header.
func (s *Store) ByToken(ctx context.Context, token string) (Runner, error) {
	if token == "" {
		return Runner{}, ErrNotFound
	}
	r, err := scan(s.pool.QueryRow(ctx,
		`SELECT `+columns+` FROM runners WHERE token_hash = $1 AND token_hash <> ''`, HashToken(token)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, ErrNotFound
	}
	return r, err
}

// Seen records a sign of life. Failures are the caller's to ignore — a missing
// timestamp is a display flaw, not a reason to reject a request.
func (s *Store) Seen(ctx context.Context, id uuid.UUID) error {
	// At most once per beat per runner. The call comes per HTTP request and
	// per WebSocket message — during a sync that is one per block, six
	// figures of them — and "last seen" is not a figure that needs to be
	// exact to the block (#165).
	now := time.Now()
	if last, ok := s.seenAt.Load(id); ok && now.Sub(last.(time.Time)) < seenEvery {
		return nil
	}
	s.seenAt.Store(id, now)
	_, err := s.pool.Exec(ctx, `UPDATE runners SET last_seen_at = now() WHERE id = $1`, id)
	return err
}

// seenEvery is how often "last seen" is actually written per runner.
const seenEvery = 30 * time.Second

// ListForOrg returns an organisation's runners, the built-in one first.
func (s *Store) ListForOrg(ctx context.Context, orgID uuid.UUID) ([]Runner, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+columns+` FROM runners WHERE org_id = $1
		 ORDER BY (kind = 'builtin') DESC, created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Runner
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Registration (spec/16) ---

// ErrTokenInvalid: no usable registration token.
var ErrTokenInvalid = errors.New("registration token invalid, expired or revoked")

// RegistrationTokenTTL is how long a registration token can enrol a host.
// Enrolling is a moment — creating the token and pasting it into `register` —
// not a standing permission, and a token that leaked into a config repository
// used to enrol runners for as long as its row existed (#163).
const RegistrationTokenTTL = 24 * time.Hour

// RegistrationToken is one enrolment token as the interface lists it: never
// the token itself, which exists in the clear exactly once.
type RegistrationToken struct {
	ID          uuid.UUID  `json:"id"`
	Description string     `json:"description"`
	CreatedAt   time.Time  `json:"created_at"`
	ExpiresAt   time.Time  `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
}

// Usable: neither revoked nor expired.
func (t RegistrationToken) Usable(now time.Time) bool {
	return t.RevokedAt == nil && now.Before(t.ExpiresAt)
}

// CreateRegistrationToken issues an organisation's registration token and
// returns it in the clear — the only moment it exists outside a hash.
func (s *Store) CreateRegistrationToken(ctx context.Context, orgID uuid.UUID, description string, createdBy *uuid.UUID) (string, error) {
	token, err := NewToken()
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO runner_registration_tokens (id, org_id, token_hash, description, created_by, expires_at)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		uuid.New(), orgID, HashToken(token), description, createdBy, time.Now().Add(RegistrationTokenTTL))
	if err != nil {
		return "", err
	}
	return token, nil
}

// ListRegistrationTokens lists an organisation's enrolment tokens, newest
// first — the revoked and expired ones included, because "which token did that
// host come in on" is a question that outlives the token.
func (s *Store) ListRegistrationTokens(ctx context.Context, orgID uuid.UUID) ([]RegistrationToken, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, description, created_at, expires_at, revoked_at
		   FROM runner_registration_tokens WHERE org_id = $1
		  ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RegistrationToken
	for rows.Next() {
		var t RegistrationToken
		if err := rows.Scan(&t.ID, &t.Description, &t.CreatedAt, &t.ExpiresAt, &t.RevokedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeRegistrationToken makes a token unusable without removing it: whoever
// wants to know which token a runner came in on should still be able to answer
// that afterwards.
func (s *Store) RevokeRegistrationToken(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE runner_registration_tokens SET revoked_at = now()
		  WHERE id = $1 AND org_id = $2 AND revoked_at IS NULL`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Register turns a registration token into a runner and its own token. The
// runner inherits the organisation from the registration token and cannot
// change it.
func (s *Store) Register(ctx context.Context, registrationToken, description string, tags []string) (Runner, string, error) {
	var orgID uuid.UUID
	err := s.pool.QueryRow(ctx,
		`SELECT org_id FROM runner_registration_tokens
		  WHERE token_hash = $1 AND revoked_at IS NULL AND expires_at > now()`, HashToken(registrationToken)).Scan(&orgID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Runner{}, "", ErrTokenInvalid
	}
	if err != nil {
		return Runner{}, "", err
	}

	token, err := NewToken()
	if err != nil {
		return Runner{}, "", err
	}
	if tags == nil {
		// A runner without tags is the normal case, and nil would land in the
		// NOT NULL column as NULL — the registration would then fail for the
		// most ordinary host there is.
		tags = []string{}
	}
	r, err := scan(s.pool.QueryRow(ctx,
		`INSERT INTO runners (id, org_id, kind, name, token_hash, description, tags)
		 VALUES ($1,$2,'remote',$3,$4,$3,$5) RETURNING `+columns,
		uuid.New(), orgID, description, HashToken(token), tags))
	if err != nil {
		return Runner{}, "", err
	}
	return r, token, nil
}

// NoteCapabilities records what a runner reported when it connected — version,
// architecture and protocol version, so that version drift becomes visible
// instead of merely being suspected.
func (s *Store) NoteCapabilities(ctx context.Context, id uuid.UUID, version, arch string, protocol int) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE runners SET version = $2, arch = $3, protocol = $4, last_seen_at = now() WHERE id = $1`,
		id, version, arch, protocol)
	return err
}

// HasRemote answers whether an organisation has registered a runner of its
// own. It is the whole of the rule for the built-in one: an organisation has
// it exactly as long as it has no registered runner (spec/16). Counted are
// registered runners, not connected ones — a maintenance window must not move
// the whole workforce back onto the control plane's host.
func (s *Store) HasRemote(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM runners WHERE org_id = $1 AND kind = 'remote'`, orgID).Scan(&n)
	return n > 0, err
}

// Delete removes a runner. Its local working copy goes with it, no platform
// state — everything that mattered is in the home store.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM runners WHERE id = $1 AND org_id = $2 AND kind = 'remote'`, id, orgID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}
