package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/identity"
)

// API keys are the second badge for the human API — the one a browser cannot
// hand out and a script cannot do without.
//
// The session cookie is HttpOnly and SameSite=Strict on purpose: exactly what a
// browser needs, and unreachable for everything else. Anything outside a
// browser — a pipeline, a script, the agent skill that creates an agent through
// the API — therefore had no route in at all. That is why this sits in the
// binary and not in somebody's CI: whoever installs covey from the repository
// gets the same means as whoever runs the instance it came from.
//
// Two decisions shape everything below:
//
//   - A key carries the rights of its SEAT, not more and not less. There is no
//     scope of its own. A scope that only exists on paper is worse than none —
//     it reads like a restriction and enforces nothing. The seat's role is the
//     boundary, and it is one that already exists everywhere in the API.
//   - A key may not manage keys and may not change the password. A leaked
//     credential must not be able to entrench itself: minting a second key or
//     locking the owner out are both moves that need the browser session, i.e.
//     the password. That is what sessionOnly further down enforces.
const (
	// apiKeyPrefix makes a key recognisable — in a log, in a config file, to a
	// secret scanner. Without it a leaked key looks like any other string.
	apiKeyPrefix = "covey_"
	// apiKeyShownPrefix is how much of the token stays visible in the list.
	// Enough to tell two keys apart, far too little to guess one.
	apiKeyShownPrefix = len(apiKeyPrefix) + 8
)

// errAPIKeyAuth is what every failed key lookup answers with. Deliberately one
// error for all cases (unknown, expired, seat gone): whoever probes must not be
// able to tell them apart.
var errAPIKeyAuth = errors.New("api key invalid or expired")

// errAPIKeyNotFound: the id names no key of this account — the same answer
// whether it never existed or belongs to somebody else.
var errAPIKeyNotFound = errors.New("api key not found")

type apiKeyStore struct{ pool *pgxpool.Pool }

func (s *Server) apiKeys() apiKeyStore { return apiKeyStore{pool: s.Pool} }

// APIKey is one entry of the key list. The token itself is NOT in here — it
// exists exactly once, in the answer to the creating call.
type APIKey struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	ExpiresAt  *time.Time `json:"expires_at"`

	// The seat the key was minted for. A key is bound to ONE organisation
	// (Principal joins humans on human_id), and since an account can hold
	// seats in several of them (#262) the listing has to say which — the name
	// is otherwise the only thing telling two keys apart, and a name is what
	// somebody typed, not what the key can reach.
	//
	// Filled by List, empty in the answer that creates a key: there the
	// organisation is the one the operator is looking at, and the list behind
	// the card re-fetches anyway. omitempty rather than a second lookup path.
	OrgID   uuid.UUID `json:"org_id,omitempty"`
	OrgName string    `json:"org_name,omitempty"`
}

// newAPIKeyToken mints a token: the prefix plus 32 random bytes, url-safe so
// that it survives a header, a query string and a YAML file unescaped.
func newAPIKeyToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return apiKeyPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// Create records a key and returns its stored form. The caller keeps the token.
func (st apiKeyStore) Create(ctx context.Context, accountID, humanID uuid.UUID, name, token string, expires *time.Time) (APIKey, error) {
	k := APIKey{
		ID:        uuid.New(),
		Name:      name,
		Prefix:    token[:apiKeyShownPrefix],
		ExpiresAt: expires,
	}
	err := st.pool.QueryRow(ctx, `INSERT INTO api_keys (id, account_id, human_id, name, prefix, token_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,
		k.ID, accountID, humanID, k.Name, k.Prefix, hashToken(token), expires).Scan(&k.CreatedAt)
	return k, err
}

// Principal resolves a key to the seat it was minted for. Expiry is checked
// inside the query so it cannot be forgotten, and the join to humans is an
// INNER one: a key whose seat is gone is gone with it.
func (st apiKeyStore) Principal(ctx context.Context, tokenHash string) (identity.Principal, uuid.UUID, error) {
	var p identity.Principal
	var keyID uuid.UUID
	var role string
	var lastUsed *time.Time
	err := st.pool.QueryRow(ctx, `SELECT k.id, k.last_used_at, a.id, a.email, a.display_name, a.platform_role,
			h.id, h.org_id, h.role
		FROM api_keys k
		JOIN accounts a ON a.id = k.account_id
		JOIN humans h ON h.id = k.human_id
		WHERE k.token_hash = $1 AND (k.expires_at IS NULL OR k.expires_at > now())`, tokenHash).
		Scan(&keyID, &lastUsed, &p.AccountID, &p.Email, &p.DisplayName, &p.PlatformRole,
			&p.ID, &p.OrgID, &role)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return identity.Principal{}, uuid.Nil, errAPIKeyAuth
		}
		return identity.Principal{}, uuid.Nil, err
	}
	p.Role = identity.NormalizeRole(role)
	p.ViaAPIKey = true

	// "Last used" is worth having and not worth a write per request. An
	// interface that polls would otherwise turn every key into a write load of
	// its own; five minutes of resolution answer the only question anybody asks
	// of this column — is this key still in use, and since when not.
	if lastUsed == nil || time.Since(*lastUsed) > 5*time.Minute {
		_, _ = st.pool.Exec(ctx, "UPDATE api_keys SET last_used_at=now() WHERE id=$1", keyID)
	}
	return p, keyID, nil
}

// List returns an account's keys, newest first — per ACCOUNT, like the session
// list: it answers "what can act in my name", and that is a question about the
// person, not about one of their organisations.
//
// And precisely because it spans the organisations, each row carries the one it
// belongs to. The two joins cannot lose a key: api_keys.human_id is NOT NULL
// with ON DELETE CASCADE (migration 0077), so a seat that goes takes its keys
// with it and every remaining row has both a seat and an organisation.
func (st apiKeyStore) List(ctx context.Context, accountID uuid.UUID) ([]APIKey, error) {
	rows, err := st.pool.Query(ctx, `SELECT k.id, k.name, k.prefix, k.created_at,
			k.last_used_at, k.expires_at, h.org_id, o.name
		FROM api_keys k
		JOIN humans h ON h.id = k.human_id
		JOIN organizations o ON o.id = h.org_id
		WHERE k.account_id=$1 ORDER BY k.created_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []APIKey{}
	for rows.Next() {
		var k APIKey
		if err := rows.Scan(&k.ID, &k.Name, &k.Prefix, &k.CreatedAt, &k.LastUsedAt,
			&k.ExpiresAt, &k.OrgID, &k.OrgName); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Delete revokes a key. Scoped to the account, so that an id from somewhere
// else revokes nothing.
func (st apiKeyStore) Delete(ctx context.Context, accountID, id uuid.UUID) (bool, error) {
	tag, err := st.pool.Exec(ctx, "DELETE FROM api_keys WHERE id=$1 AND account_id=$2", id, accountID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

// Rotate replaces a key's token and nothing else that identifies it: the name
// and the seat stay, the lifetime stays (a ninety-day key comes out of this as
// a ninety-day key, counted from now), and the old token stops in the same
// transaction — there is no moment with two live tokens for one purpose.
//
// It is a new row, deliberately. The token exists once, in the answer, and
// only its hash is stored (migration 0077); rewriting the hash in place would
// leave created_at and last_used_at describing a credential that no longer
// exists. A fresh row says what is true: this key was created now and has not
// been used yet. The old id dies with the old token, which is what "revoked"
// has always meant here.
//
// expires is the lifetime the caller asked for; nil means "the same as before".
// Scoped to the account like Delete, so that an id from somewhere else rotates
// nothing.
func (st apiKeyStore) Rotate(ctx context.Context, accountID, id uuid.UUID, token string, expires *time.Time, keepLifetime bool) (APIKey, error) {
	tx, err := st.pool.Begin(ctx)
	if err != nil {
		return APIKey{}, err
	}
	defer tx.Rollback(ctx)

	var humanID uuid.UUID
	var name string
	var createdAt time.Time
	var oldExpires *time.Time
	err = tx.QueryRow(ctx, `SELECT human_id, name, created_at, expires_at FROM api_keys
		WHERE id=$1 AND account_id=$2 FOR UPDATE`, id, accountID).
		Scan(&humanID, &name, &createdAt, &oldExpires)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return APIKey{}, errAPIKeyNotFound
		}
		return APIKey{}, err
	}
	if keepLifetime && oldExpires != nil {
		// The lifetime is what the operator decided, not the remaining span:
		// a key rotated on its last day gets its full term again, and one that
		// has already expired comes back alive — rotation is the remedy for
		// both, and a new key that is dead on arrival would be furniture.
		t := time.Now().Add(oldExpires.Sub(createdAt))
		expires = &t
	}

	k := APIKey{
		ID:        uuid.New(),
		Name:      name,
		Prefix:    token[:apiKeyShownPrefix],
		ExpiresAt: expires,
	}
	if err := tx.QueryRow(ctx, `INSERT INTO api_keys (id, account_id, human_id, name, prefix, token_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7) RETURNING created_at`,
		k.ID, accountID, humanID, k.Name, k.Prefix, hashToken(token), expires).Scan(&k.CreatedAt); err != nil {
		return APIKey{}, err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM api_keys WHERE id=$1", id); err != nil {
		return APIKey{}, err
	}
	return k, tx.Commit(ctx)
}

// --- handlers ---

func (s *Server) handleListAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.apiKeys().List(r.Context(), principalFrom(r).AccountID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, keys)
}

// handleCreateAPIKey mints a key for the seat the caller currently works from.
// The token is in the answer and nowhere else — this is the one moment it
// exists in readable form.
func (s *Server) handleCreateAPIKey(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	if !p.HasOrg() {
		writeErr(w, http.StatusConflict, "this account does not belong to an organisation yet")
		return
	}
	var body struct {
		Name          string `json:"name"`
		ExpiresInDays int    `json:"expires_in_days"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable: "+err.Error())
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		writeErr(w, http.StatusBadRequest, "name is missing — a key without a name cannot be revoked with any confidence")
		return
	}
	if len(name) > 80 {
		writeErr(w, http.StatusBadRequest, "name is longer than 80 characters")
		return
	}
	var expires *time.Time
	if body.ExpiresInDays > 0 {
		if body.ExpiresInDays > 3650 {
			writeErr(w, http.StatusBadRequest, "expires_in_days is above ten years")
			return
		}
		t := time.Now().AddDate(0, 0, body.ExpiresInDays)
		expires = &t
	}
	token, err := newAPIKeyToken()
	if err != nil {
		mapErr(w, err)
		return
	}
	key, err := s.apiKeys().Create(r.Context(), p.AccountID, p.ID, name, token, expires)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		APIKey
		Token string `json:"token"`
	}{APIKey: key, Token: token})
}

func (s *Server) handleDeleteAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	ok, err := s.apiKeys().Delete(r.Context(), principalFrom(r).AccountID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"revoked": true})
}

// handleRotateAPIKey exchanges the token behind a key. Session only, like
// create and revoke: a key that could give itself a new token would outlive
// every attempt to revoke it.
//
// The body is optional. Without expires_in_days the new key keeps the old
// one's lifetime; with it, the caller sets a new one (0 = never). The
// distinction between "absent" and "0" is why the field is a pointer.
func (s *Server) handleRotateAPIKey(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var body struct {
		ExpiresInDays *int `json:"expires_in_days"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &body); err != nil {
			writeErr(w, http.StatusBadRequest, "body not readable: "+err.Error())
			return
		}
	}
	var expires *time.Time
	keepLifetime := body.ExpiresInDays == nil
	if !keepLifetime && *body.ExpiresInDays > 0 {
		if *body.ExpiresInDays > 3650 {
			writeErr(w, http.StatusBadRequest, "expires_in_days is above ten years")
			return
		}
		t := time.Now().AddDate(0, 0, *body.ExpiresInDays)
		expires = &t
	}
	token, err := newAPIKeyToken()
	if err != nil {
		mapErr(w, err)
		return
	}
	key, err := s.apiKeys().Rotate(r.Context(), principalFrom(r).AccountID, id, token, expires, keepLifetime)
	if err != nil {
		if errors.Is(err, errAPIKeyNotFound) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, struct {
		APIKey
		Token string `json:"token"`
	}{APIKey: key, Token: token})
}
