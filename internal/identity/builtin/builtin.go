// Package builtin is the DB-backed IdentityProvider: signed JWTs (Ed25519,
// short TTL, scoped) for agents, Argon2id for human logins. Crypto primitives
// from proven libraries, no OAuth/OIDC stack of our own (spec/10).
package builtin

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/argon2"

	"covey/internal/identity"
)

var ErrInvalidCredentials = errors.New("invalid credentials")

type Provider struct {
	pool *pgxpool.Pool
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// New creates the provider with a process-local signing key. The tokens it
// makes are deliberately short-lived — a restart invalidates them, which is
// fine for daemon tokens (TTL in minutes).
func New(pool *pgxpool.Pool) (*Provider, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &Provider{pool: pool, priv: priv, pub: pub}, nil
}

type agentClaims struct {
	jwt.RegisteredClaims
	System string   `json:"system,omitempty"`
	Scopes []string `json:"scopes,omitempty"`
}

func (p *Provider) IssueAgentToken(ctx context.Context, agentID uuid.UUID, scope identity.Scope, ttl time.Duration) (identity.Token, error) {
	now := time.Now()
	claims := agentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   agentID.String(),
			Audience:  jwt.ClaimStrings{scope.Audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			Issuer:    "covey",
		},
		System: scope.System,
		Scopes: scope.Scopes,
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodEdDSA, claims).SignedString(p.priv)
	if err != nil {
		return identity.Token{}, err
	}
	return identity.Token{Value: signed, ExpiresAt: now.Add(ttl)}, nil
}

func (p *Provider) VerifyAgentToken(ctx context.Context, token, audience string) (uuid.UUID, error) {
	parsed, err := jwt.ParseWithClaims(token, &agentClaims{}, func(t *jwt.Token) (any, error) {
		if t.Method != jwt.SigningMethodEdDSA {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return p.pub, nil
	}, jwt.WithAudience(audience), jwt.WithIssuer("covey"), jwt.WithExpirationRequired())
	if err != nil {
		return uuid.Nil, err
	}
	claims := parsed.Claims.(*agentClaims)
	return uuid.Parse(claims.Subject)
}

// AuthenticateHuman checks the credentials against the ACCOUNT and then
// resolves the seat this login starts from.
//
// Two steps, because they answer two questions: "is this the right password?"
// belongs to the account, "which organisation am I working in?" to the
// membership. An account without a membership authenticates successfully and
// gets a principal without an organisation — that is not an error but the
// state a self-registration produces.
func (p *Provider) AuthenticateHuman(ctx context.Context, creds identity.Credentials) (identity.Principal, error) {
	var pr identity.Principal
	var hash string
	err := p.pool.QueryRow(ctx, `SELECT id, email, display_name, password_hash, platform_role
		FROM accounts WHERE email=$1`, strings.ToLower(strings.TrimSpace(creds.Email))).
		Scan(&pr.AccountID, &pr.Email, &pr.DisplayName, &hash, &pr.PlatformRole)
	if errors.Is(err, pgx.ErrNoRows) {
		// Dummy comparison against a timing oracle (user enumeration).
		_ = VerifyPassword(creds.Password, mustHash("covey-dummy"))
		return identity.Principal{}, ErrInvalidCredentials
	}
	if err != nil {
		return identity.Principal{}, err
	}
	if !VerifyPassword(creds.Password, hash) {
		return identity.Principal{}, ErrInvalidCredentials
	}

	// The seat to start from. With several it is the oldest — a deliberate,
	// reproducible choice rather than whatever the database happens to return
	// first; switching is a decision of its own (org switcher, P5).
	if err := p.pool.QueryRow(ctx, `SELECT id, org_id, role FROM humans
		WHERE account_id=$1 ORDER BY created_at LIMIT 1`, pr.AccountID).
		Scan(&pr.ID, &pr.OrgID, &pr.Role); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return identity.Principal{}, err
	}
	pr.Role = identity.NormalizeRole(pr.Role)

	// accounts.last_login_at exists since migration 0058 and was written by
	// nobody — a column that is always empty answers the one question the
	// account list is for ("is this login still in use?") with a shrug.
	// Deliberately best-effort: a failed bookkeeping write must not turn a
	// correct password into a failed login.
	if _, err := p.pool.Exec(ctx, `UPDATE accounts SET last_login_at=now() WHERE id=$1`,
		pr.AccountID); err != nil {
		return pr, nil //nolint:nilerr // see above: sign-in wins over bookkeeping
	}
	return pr, nil
}

// Memberships lists the seats of an account — the organisations this login
// works in.
func (p *Provider) Memberships(ctx context.Context, accountID uuid.UUID) ([]identity.Membership, error) {
	rows, err := p.pool.Query(ctx, `SELECT h.id, h.org_id, o.name, h.role
		FROM humans h JOIN organizations o ON o.id = h.org_id
		WHERE h.account_id=$1 ORDER BY h.created_at`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []identity.Membership
	for rows.Next() {
		var m identity.Membership
		if err := rows.Scan(&m.HumanID, &m.OrgID, &m.OrgName, &m.Role); err != nil {
			return nil, err
		}
		m.Role = identity.NormalizeRole(m.Role)
		out = append(out, m)
	}
	return out, rows.Err()
}

// --- Argon2id password hashing (PHC string format) ---

const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
)

func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func mustHash(pw string) string {
	h, err := HashPassword(pw)
	if err != nil {
		panic(err)
	}
	return h
}

func VerifyPassword(password, phc string) bool {
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, t, m, p, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}
