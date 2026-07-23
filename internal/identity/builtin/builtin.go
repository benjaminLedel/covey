// Package builtin ist der DB-gestützte IdentityProvider: signierte JWTs
// (Ed25519, kurz-TTL, gescopt) für Agenten, Argon2id für Menschen-Logins.
// Krypto-Primitive aus bewährten Libs, kein eigener OAuth/OIDC-Stack (spec/10).
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

var ErrInvalidCredentials = errors.New("ungültige Zugangsdaten")

type Provider struct {
	pool *pgxpool.Pool
	priv ed25519.PrivateKey
	pub  ed25519.PublicKey
}

// New erzeugt den Provider mit einem prozess-lokalen Signing-Key. Die von ihm
// gemachten Tokens sind bewusst kurzlebig — ein Neustart invalidiert sie, was
// für Daemon-Tokens (Minuten-TTL) in Ordnung ist.
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
			return nil, fmt.Errorf("unerwartete Signaturmethode %v", t.Header["alg"])
		}
		return p.pub, nil
	}, jwt.WithAudience(audience), jwt.WithIssuer("covey"), jwt.WithExpirationRequired())
	if err != nil {
		return uuid.Nil, err
	}
	claims := parsed.Claims.(*agentClaims)
	return uuid.Parse(claims.Subject)
}

func (p *Provider) AuthenticateHuman(ctx context.Context, creds identity.Credentials) (identity.Principal, error) {
	var pr identity.Principal
	var hash string
	err := p.pool.QueryRow(ctx, `SELECT id, org_id, email, display_name, role, password_hash
		FROM humans WHERE email=$1`, strings.ToLower(creds.Email)).
		Scan(&pr.ID, &pr.OrgID, &pr.Email, &pr.DisplayName, &pr.Role, &hash)
	if errors.Is(err, pgx.ErrNoRows) {
		// Dummy-Vergleich gegen Timing-Orakel (User-Enumeration).
		_ = VerifyPassword(creds.Password, mustHash("covey-dummy"))
		return pr, ErrInvalidCredentials
	}
	if err != nil {
		return pr, err
	}
	if !VerifyPassword(creds.Password, hash) {
		return pr, ErrInvalidCredentials
	}
	return pr, nil
}

// --- Argon2id-Passwort-Hashing (PHC-String-Format) ---

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
