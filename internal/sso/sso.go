// Package sso is single sign-on per organisation (#723): covey as the OIDC
// relying party of the provider an organisation brings along.
//
// It is a client, not a protocol of our own — the provider is the server, and
// go-oidc checks its ID tokens. What this package adds is the part that is
// covey's to decide:
//
//   - which organisation an e-mail address signs in through: the one that
//     proved by DNS that it holds the address's domain;
//   - which addresses an organisation's provider may assert: only those in its
//     verified domains, so no organisation can sign in as somebody else's
//     account;
//   - which account a provider identity becomes: bound by issuer and subject,
//     an existing account of the same address on its first sign-in;
//   - what "SSO required" does to the accounts already there: their password
//     goes, their sessions end.
package sso

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/secrets/sealbox"
)

// NoPassword is the password hash of an account that signs in through single
// sign-on only. It is no PHC string, so no password ever verifies against it.
const NoPassword = "!sso"

// ChallengePrefix is the DNS label the domain's TXT record sits under.
const ChallengePrefix = "_covey-challenge."

var (
	// ErrNotConfigured: the organisation has no single sign-on.
	ErrNotConfigured = errors.New("sso: this organisation has no single sign-on configured")
	// ErrDomainTaken: another organisation claims the domain.
	ErrDomainTaken = errors.New("sso: this domain is claimed by another organisation")
	// ErrNoDomain: the organisation does not claim the domain.
	ErrNoDomain = errors.New("sso: this organisation does not claim that domain")
	// ErrNotVerified: the TXT record was not found or does not carry the token.
	ErrNotVerified = errors.New("sso: the TXT record was not found or does not carry the token")
	// ErrNotLinked: requiring SSO needs the admin to have signed in through it.
	ErrNotLinked = errors.New("sso: sign in through this organisation's single sign-on once before requiring it — otherwise nobody knows it works")
	// ErrForeignAddress: the provider asserted an address outside the
	// organisation's verified domains.
	ErrForeignAddress = errors.New("sso: the provider signed in an address outside this organisation's verified domains")
	// ErrNoEmail: the ID token names no e-mail address.
	ErrNoEmail = errors.New("sso: the provider's ID token carries no e-mail address")
	// ErrToken: the provider did not redeem the code — mostly a wrong client
	// secret, or a redirect URI registered for another kind of client.
	ErrToken = errors.New("sso: the provider did not redeem the code")
	// ErrIDToken: the ID token is missing or does not verify.
	ErrIDToken = errors.New("sso: the ID token does not verify")
	// ErrUnverified: the provider says it has not verified the address.
	ErrUnverified = errors.New("sso: the provider has not verified the address")
	// ErrFlow: the flow cookie is missing, forged, expired or from another
	// browser.
	ErrFlow = errors.New("sso: the sign-in does not belong to a request started in this browser, or it expired")
)

// Config is an organisation's single sign-on as the interface shows it —
// never the secret.
type Config struct {
	OrgID       uuid.UUID `json:"org_id"`
	Issuer      string    `json:"issuer"`
	ClientID    string    `json:"client_id"`
	DefaultRole string    `json:"default_role"`
	Required    bool      `json:"required"`
	UpdatedAt   time.Time `json:"updated_at"`
	// LastError is the last failed sign-in through the provider (#734).
	LastError   string     `json:"last_error,omitempty"`
	LastErrorAt *time.Time `json:"last_error_at,omitempty"`

	secret string
}

// Input is what an org admin enters. An empty secret on an update keeps the
// stored one, as long as the client ID stays the same.
type Input struct {
	Issuer      string `json:"issuer"`
	ClientID    string `json:"client_id"`
	Secret      string `json:"secret"`
	DefaultRole string `json:"default_role"`
}

// Domain is a claimed e-mail domain.
type Domain struct {
	Domain     string     `json:"domain"`
	Token      string     `json:"token"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
	// Method is how it was confirmed: "dns", or "manual" by an instance
	// system admin (#729).
	Method string `json:"verified_method,omitempty"`
	// Record is the TXT record's name, for the interface to show.
	Record string `json:"record"`
}

// Route is where an e-mail address signs in.
type Route struct {
	OrgID    uuid.UUID
	OrgName  string
	Required bool
}

// Service holds the organisations' single sign-on.
type Service struct {
	Pool *pgxpool.Pool
	Box  *sealbox.Box
	// LookupTXT resolves a DNS TXT record; nil: net.DefaultResolver.
	LookupTXT func(ctx context.Context, name string) ([]string, error)
	// HTTP talks to the providers; nil: a client with a timeout.
	HTTP *http.Client
	// ValidRole says whether a role may be handed to a new seat.
	ValidRole func(string) bool
}

func (s *Service) client() *http.Client {
	if s.HTTP != nil {
		return s.HTTP
	}
	return &http.Client{Timeout: 15 * time.Second}
}

func (s *Service) ctx(ctx context.Context) context.Context {
	return oidc.ClientContext(ctx, s.client())
}

func secretAAD(org uuid.UUID) []byte { return []byte("org-sso:secret:" + org.String()) }

// Get is the organisation's configuration.
func (s *Service) Get(ctx context.Context, org uuid.UUID) (Config, error) {
	var c Config
	var n, ct []byte
	err := s.Pool.QueryRow(ctx, `SELECT org_id, issuer, client_id, default_role, required, updated_at,
		last_error, last_error_at, secret_nonce, secret_ct FROM org_sso WHERE org_id=$1`, org).
		Scan(&c.OrgID, &c.Issuer, &c.ClientID, &c.DefaultRole, &c.Required, &c.UpdatedAt,
			&c.LastError, &c.LastErrorAt, &n, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return Config{}, ErrNotConfigured
	}
	if err != nil {
		return Config{}, err
	}
	if c.secret, err = s.Box.Open("sso client secret", secretAAD(org), n, ct); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Save creates or updates the organisation's configuration. The issuer's
// discovery document is fetched first: a wrong issuer fails here, with the
// provider's reason, and not at the first sign-in.
func (s *Service) Save(ctx context.Context, org uuid.UUID, in Input) (Config, error) {
	in.Issuer = strings.TrimRight(strings.TrimSpace(in.Issuer), "/")
	in.ClientID, in.DefaultRole = strings.TrimSpace(in.ClientID), strings.TrimSpace(in.DefaultRole)
	if in.Issuer == "" || in.ClientID == "" {
		return Config{}, fmt.Errorf("sso: issuer and client ID are required")
	}
	if u, err := url.Parse(in.Issuer); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return Config{}, fmt.Errorf("sso: the issuer is a URL such as https://login.example.org/realms/acme")
	}
	if s.ValidRole != nil && !s.ValidRole(in.DefaultRole) {
		return Config{}, fmt.Errorf("sso: unknown role %q", in.DefaultRole)
	}
	existing, err := s.Get(ctx, org)
	if err != nil && !errors.Is(err, ErrNotConfigured) {
		return Config{}, err
	}
	secret := in.Secret
	if secret == "" {
		if errors.Is(err, ErrNotConfigured) || existing.ClientID != in.ClientID {
			return Config{}, fmt.Errorf("sso: the client secret is required")
		}
		secret = existing.secret
	}
	if _, err := oidc.NewProvider(s.ctx(ctx), in.Issuer); err != nil {
		return Config{}, fmt.Errorf("sso: the issuer's discovery document could not be read: %w", err)
	}
	n, ct, err := s.Box.Seal(secretAAD(org), secret)
	if err != nil {
		return Config{}, err
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO org_sso (org_id, issuer, client_id, secret_nonce, secret_ct, default_role)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id) DO UPDATE SET issuer=EXCLUDED.issuer, client_id=EXCLUDED.client_id,
			secret_nonce=EXCLUDED.secret_nonce, secret_ct=EXCLUDED.secret_ct,
			default_role=EXCLUDED.default_role, updated_at=now()`,
		org, in.Issuer, in.ClientID, n, ct, in.DefaultRole); err != nil {
		return Config{}, err
	}
	return s.Get(ctx, org)
}

// Delete removes the organisation's configuration. The domains stay claimed;
// without a configuration they route nobody, and "required" goes with the row.
func (s *Service) Delete(ctx context.Context, org uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM org_sso WHERE org_id=$1`, org)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotConfigured
	}
	return nil
}

// NormalizeDomain lowercases a domain and strips an "@" or an address in
// front of it.
func NormalizeDomain(d string) string {
	d = strings.ToLower(strings.TrimSpace(d))
	if i := strings.LastIndex(d, "@"); i >= 0 {
		d = d[i+1:]
	}
	return strings.TrimSuffix(d, ".")
}

// DomainOf is the domain of an e-mail address, or "".
func DomainOf(email string) string {
	email = strings.ToLower(strings.TrimSpace(email))
	i := strings.LastIndex(email, "@")
	if i < 0 {
		return ""
	}
	return email[i+1:]
}

func validDomain(d string) bool {
	if len(d) < 3 || len(d) > 253 || !strings.Contains(d, ".") {
		return false
	}
	for _, label := range strings.Split(d, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
				return false
			}
		}
	}
	return true
}

// Domains lists the organisation's claimed domains.
func (s *Service) Domains(ctx context.Context, org uuid.UUID) ([]Domain, error) {
	rows, err := s.Pool.Query(ctx, `SELECT domain, token, verified_at, COALESCE(verified_method, '')
		FROM org_sso_domains WHERE org_id=$1 ORDER BY domain`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Domain{}
	for rows.Next() {
		var d Domain
		if err := rows.Scan(&d.Domain, &d.Token, &d.VerifiedAt, &d.Method); err != nil {
			return nil, err
		}
		d.Record = ChallengePrefix + d.Domain
		out = append(out, d)
	}
	return out, rows.Err()
}

// AddDomain claims a domain and hands out the token its TXT record has to
// carry. Claiming the same domain again returns the existing claim.
func (s *Service) AddDomain(ctx context.Context, org uuid.UUID, domain string) (Domain, error) {
	domain = NormalizeDomain(domain)
	if !validDomain(domain) {
		return Domain{}, fmt.Errorf("sso: %q is not a domain", domain)
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return Domain{}, err
	}
	token := "covey-" + hex.EncodeToString(buf)
	_, err := s.Pool.Exec(ctx, `INSERT INTO org_sso_domains (domain, org_id, token) VALUES ($1,$2,$3)`,
		domain, org, token)
	var pe *pgconn.PgError
	if errors.As(err, &pe) && pe.Code == "23505" {
		var owner uuid.UUID
		if err := s.Pool.QueryRow(ctx, `SELECT org_id FROM org_sso_domains WHERE domain=$1`, domain).Scan(&owner); err != nil {
			return Domain{}, err
		}
		if owner != org {
			return Domain{}, ErrDomainTaken
		}
	} else if err != nil {
		return Domain{}, err
	}
	return s.domain(ctx, org, domain)
}

func (s *Service) domain(ctx context.Context, org uuid.UUID, domain string) (Domain, error) {
	d := Domain{Domain: domain, Record: ChallengePrefix + domain}
	err := s.Pool.QueryRow(ctx, `SELECT token, verified_at, COALESCE(verified_method, '')
		FROM org_sso_domains WHERE domain=$1 AND org_id=$2`,
		domain, org).Scan(&d.Token, &d.VerifiedAt, &d.Method)
	if errors.Is(err, pgx.ErrNoRows) {
		return Domain{}, ErrNoDomain
	}
	return d, err
}

// VerifyDomain looks the TXT record up. A verified domain under a required
// single sign-on rewrites its accounts at once; keepSession is the acting
// session's token hash, which survives.
func (s *Service) VerifyDomain(ctx context.Context, org uuid.UUID, domain, keepSession string) (Domain, error) {
	domain = NormalizeDomain(domain)
	d, err := s.domain(ctx, org, domain)
	if err != nil {
		return Domain{}, err
	}
	lookup := s.LookupTXT
	if lookup == nil {
		lookup = net.DefaultResolver.LookupTXT
	}
	records, err := lookup(ctx, ChallengePrefix+domain)
	found := false
	for _, r := range records {
		if strings.TrimSpace(r) == d.Token {
			found = true
		}
	}
	if !found {
		if err != nil {
			return d, fmt.Errorf("%w (%v)", ErrNotVerified, err)
		}
		return d, ErrNotVerified
	}
	return s.markVerified(ctx, org, domain, "dns", uuid.Nil, keepSession)
}

// ConfirmDomain confirms a claimed domain by hand, without its DNS record
// (#729). Only an instance system admin may — the caller checks that; the
// account is kept beside the claim. What follows is the same as for a DNS
// proof.
func (s *Service) ConfirmDomain(ctx context.Context, org uuid.UUID, domain string, by uuid.UUID, keepSession string) (Domain, error) {
	domain = NormalizeDomain(domain)
	if _, err := s.domain(ctx, org, domain); err != nil {
		return Domain{}, err
	}
	return s.markVerified(ctx, org, domain, "manual", by, keepSession)
}

func (s *Service) markVerified(ctx context.Context, org uuid.UUID, domain, method string, by uuid.UUID, keepSession string) (Domain, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Domain{}, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE org_sso_domains SET verified_at=COALESCE(verified_at, now()),
			verified_method=COALESCE(verified_method, $3), verified_by=COALESCE(verified_by, $4)
		WHERE domain=$1 AND org_id=$2`, domain, org, method, uuid.NullUUID{UUID: by, Valid: by != uuid.Nil}); err != nil {
		return Domain{}, err
	}
	var required bool
	if err := tx.QueryRow(ctx, `SELECT COALESCE((SELECT required FROM org_sso WHERE org_id=$1), false)`, org).
		Scan(&required); err != nil {
		return Domain{}, err
	}
	if required {
		if _, err := rewrite(ctx, tx, org, keepSession); err != nil {
			return Domain{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Domain{}, err
	}
	return s.domain(ctx, org, domain)
}

// RemoveDomain gives a claim up.
func (s *Service) RemoveDomain(ctx context.Context, org uuid.UUID, domain string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM org_sso_domains WHERE domain=$1 AND org_id=$2`, NormalizeDomain(domain), org)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNoDomain
	}
	return nil
}

// Discover says where an e-mail address signs in: the organisation holding
// its verified domain, if that organisation has single sign-on configured.
func (s *Service) Discover(ctx context.Context, email string) (Route, bool, error) {
	if s == nil {
		return Route{}, false, nil
	}
	domain := DomainOf(email)
	if domain == "" {
		return Route{}, false, nil
	}
	var r Route
	err := s.Pool.QueryRow(ctx, `SELECT o.id, o.name, c.required
		FROM org_sso_domains d JOIN org_sso c ON c.org_id=d.org_id JOIN organizations o ON o.id=d.org_id
		WHERE d.domain=$1 AND d.verified_at IS NOT NULL`, domain).Scan(&r.OrgID, &r.OrgName, &r.Required)
	if errors.Is(err, pgx.ErrNoRows) {
		return Route{}, false, nil
	}
	if err != nil {
		return Route{}, false, err
	}
	return r, true, nil
}

// PasswordRefused says whether an address may not use a password: its domain
// is verified by an organisation that requires single sign-on, and the
// account behind it is no instance system admin — they keep their password,
// so an installation cannot lock itself out.
func (s *Service) PasswordRefused(ctx context.Context, email string) (bool, error) {
	r, ok, err := s.Discover(ctx, email)
	if err != nil || !ok || !r.Required {
		return false, err
	}
	var admin bool
	err = s.Pool.QueryRow(ctx, `SELECT platform_role='system_admin' FROM accounts WHERE email=$1`,
		strings.ToLower(strings.TrimSpace(email))).Scan(&admin)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return !admin, nil
}

// SetRequired switches "SSO required". Turning it on is refused unless the
// acting account has signed in through this organisation's provider — so the
// switch cannot lock out the person flipping it, and nobody requires a sign-in
// that was never seen to work. Turning it on rewrites the accounts of the
// verified domains (see rewrite); keepSession is the acting session's token
// hash, which survives. It returns how many accounts were rewritten.
func (s *Service) SetRequired(ctx context.Context, org, actor uuid.UUID, on bool, keepSession string) (int, error) {
	cfg, err := s.Get(ctx, org)
	if err != nil {
		return 0, err
	}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if on {
		var linked bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_identities
			WHERE account_id=$1 AND org_id=$2 AND issuer=$3)`, actor, org, cfg.Issuer).Scan(&linked); err != nil {
			return 0, err
		}
		if !linked {
			return 0, ErrNotLinked
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE org_sso SET required=$2, updated_at=now() WHERE org_id=$1`, org, on); err != nil {
		return 0, err
	}
	n := 0
	if on {
		if n, err = rewrite(ctx, tx, org, keepSession); err != nil {
			return 0, err
		}
	}
	return n, tx.Commit(ctx)
}

// rewrite turns the accounts of an organisation's verified domains into
// single sign-on accounts: the password goes and the sessions end, except
// the acting one. Instance system admins are left alone. Turning "required"
// off later gives nobody a password back — whoever wants one asks for a reset.
func rewrite(ctx context.Context, tx pgx.Tx, org uuid.UUID, keepSession string) (int, error) {
	rows, err := tx.Query(ctx, `UPDATE accounts SET password_hash=$2
		WHERE platform_role='user' AND password_hash<>$2
		  AND split_part(email, '@', 2) IN
		      (SELECT domain FROM org_sso_domains WHERE org_id=$1 AND verified_at IS NOT NULL)
		RETURNING id`, org, NoPassword)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[uuid.UUID])
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE humans SET password_hash=$2 WHERE account_id = ANY($1)`, ids, NoPassword); err != nil {
		return 0, err
	}
	// A reset link mailed before the switch would hand the password back.
	if _, err := tx.Exec(ctx, `DELETE FROM account_tokens WHERE account_id = ANY($1) AND purpose='reset'`, ids); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM http_sessions WHERE account_id = ANY($1) AND token_hash<>$2`,
		ids, keepSession); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// Linked says whether an account has signed in through the organisation's
// current provider — the condition for requiring it.
func (s *Service) Linked(ctx context.Context, org, account uuid.UUID) (bool, error) {
	var linked bool
	err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM account_identities i JOIN org_sso c
		ON c.org_id=i.org_id AND c.issuer=i.issuer WHERE i.org_id=$1 AND i.account_id=$2)`, org, account).Scan(&linked)
	return linked, err
}
