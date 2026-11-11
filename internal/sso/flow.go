package sso

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"golang.org/x/oauth2"
)

// FlowTTL is how long a started sign-in may take.
const FlowTTL = 10 * time.Minute

// flow is what the browser carries between the start and the callback,
// sealed with the master key: which organisation, the state the provider
// echoes, the nonce the ID token must carry and the PKCE verifier. Sealed, not
// only signed — the verifier is a secret until the code is redeemed.
type flow struct {
	Org      uuid.UUID `json:"o"`
	State    string    `json:"s"`
	Nonce    string    `json:"n"`
	Verifier string    `json:"v"`
	Expires  int64     `json:"e"`
}

var flowAAD = []byte("org-sso:flow")

func random() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func (s *Service) oauth(ctx context.Context, cfg Config, redirectURI string) (*oidc.Provider, oauth2.Config, error) {
	p, err := oidc.NewProvider(s.ctx(ctx), cfg.Issuer)
	if err != nil {
		return nil, oauth2.Config{}, fmt.Errorf("sso: the provider cannot be reached: %w", err)
	}
	return p, oauth2.Config{
		ClientID: cfg.ClientID, ClientSecret: cfg.secret, RedirectURL: redirectURI,
		Endpoint: p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
	}, nil
}

// Begin starts a sign-in: the provider's authorization URL to send the browser
// to, and the sealed flow for the cookie that binds the callback to this
// browser. loginHint pre-fills the provider's sign-in form.
func (s *Service) Begin(ctx context.Context, org uuid.UUID, redirectURI, loginHint string) (authURL, cookie string, err error) {
	cfg, err := s.Get(ctx, org)
	if err != nil {
		return "", "", err
	}
	_, oc, err := s.oauth(ctx, cfg, redirectURI)
	if err != nil {
		return "", "", err
	}
	f := flow{Org: org, Verifier: oauth2.GenerateVerifier(), Expires: time.Now().Add(FlowTTL).Unix()}
	if f.State, err = random(); err != nil {
		return "", "", err
	}
	if f.Nonce, err = random(); err != nil {
		return "", "", err
	}
	raw, _ := json.Marshal(f)
	n, ct, err := s.Box.Seal(flowAAD, string(raw))
	if err != nil {
		return "", "", err
	}
	opts := []oauth2.AuthCodeOption{oidc.Nonce(f.Nonce), oauth2.S256ChallengeOption(f.Verifier)}
	if loginHint != "" {
		opts = append(opts, oauth2.SetAuthURLParam("login_hint", loginHint))
	}
	return oc.AuthCodeURL(f.State, opts...),
		base64.RawURLEncoding.EncodeToString(n) + "." + base64.RawURLEncoding.EncodeToString(ct), nil
}

func (s *Service) openFlow(cookie string) (flow, error) {
	var f flow
	a, b, ok := strings.Cut(cookie, ".")
	if !ok {
		return f, ErrFlow
	}
	n, err1 := base64.RawURLEncoding.DecodeString(a)
	ct, err2 := base64.RawURLEncoding.DecodeString(b)
	if err1 != nil || err2 != nil {
		return f, ErrFlow
	}
	raw, err := s.Box.Open("sso flow", flowAAD, n, ct)
	if err != nil {
		return f, ErrFlow
	}
	if json.Unmarshal([]byte(raw), &f) != nil || time.Now().Unix() > f.Expires {
		return f, ErrFlow
	}
	return f, nil
}

// Result is a finished sign-in: the account, and its seat in the
// organisation the session starts on.
type Result struct {
	AccountID uuid.UUID
	SeatID    uuid.UUID
	OrgID     uuid.UUID
	Email     string
	// Linked: an existing account was bound to the provider identity now.
	Linked bool
	// Created: the account did not exist before.
	Created bool
}

type claims struct {
	Email             string `json:"email"`
	EmailVerified     *bool  `json:"email_verified"`
	PreferredUsername string `json:"preferred_username"`
	UPN               string `json:"upn"`
	Name              string `json:"name"`
}

func (c claims) address() string {
	for _, v := range []string{c.Email, c.PreferredUsername, c.UPN} {
		if v = strings.ToLower(strings.TrimSpace(v)); strings.Contains(v, "@") {
			return v
		}
	}
	return ""
}

// Finish completes a sign-in at the callback: checks the flow against the
// cookie, redeems the code, verifies the ID token and resolves account and
// seat.
func (s *Service) Finish(ctx context.Context, cookie, state, code, redirectURI string) (Result, error) {
	f, err := s.openFlow(cookie)
	if err != nil {
		return Result{}, err
	}
	if state == "" || state != f.State {
		return Result{}, ErrFlow
	}
	res, err := s.finish(ctx, f, code, redirectURI)
	// The organisation's admin cannot read the server log, so its last
	// failure is kept with its configuration (#734); success clears it.
	msg := ""
	if err != nil {
		msg = err.Error()
	}
	if _, rerr := s.Pool.Exec(ctx, `UPDATE org_sso SET last_error=$2,
			last_error_at=CASE WHEN $2='' THEN NULL ELSE now() END WHERE org_id=$1`, f.Org, msg); rerr != nil && err == nil {
		err = rerr
	}
	return res, err
}

// Fail records a failure the provider reported on its way back (an error
// instead of a code) against the organisation the flow belongs to, so its
// admin sees it (#734). A flow that cannot be opened records nothing.
func (s *Service) Fail(ctx context.Context, cookie, msg string) {
	f, err := s.openFlow(cookie)
	if err != nil {
		return
	}
	_, _ = s.Pool.Exec(ctx, `UPDATE org_sso SET last_error=$2, last_error_at=now() WHERE org_id=$1`, f.Org, msg)
}

func (s *Service) finish(ctx context.Context, f flow, code, redirectURI string) (Result, error) {
	cfg, err := s.Get(ctx, f.Org)
	if err != nil {
		return Result{}, err
	}
	p, oc, err := s.oauth(ctx, cfg, redirectURI)
	if err != nil {
		return Result{}, err
	}
	tok, err := oc.Exchange(s.ctx(ctx), code, oauth2.VerifierOption(f.Verifier))
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrToken, err)
	}
	rawID, _ := tok.Extra("id_token").(string)
	if rawID == "" {
		return Result{}, fmt.Errorf("%w: the provider's answer carries no ID token", ErrIDToken)
	}
	idt, err := p.Verifier(&oidc.Config{ClientID: cfg.ClientID}).Verify(s.ctx(ctx), rawID)
	if err != nil {
		return Result{}, fmt.Errorf("%w: %w", ErrIDToken, err)
	}
	if idt.Nonce != f.Nonce {
		return Result{}, ErrFlow
	}
	var c claims
	if err := idt.Claims(&c); err != nil {
		return Result{}, err
	}
	email := c.address()
	if email == "" {
		return Result{}, ErrNoEmail
	}
	// A provider that says outright that it has not checked the address is
	// not believed: binding to an existing account by address would otherwise
	// let anybody who can type a colleague's address into their profile sign
	// in as that colleague.
	if c.EmailVerified != nil && !*c.EmailVerified {
		return Result{}, fmt.Errorf("%w: %s", ErrUnverified, email)
	}
	return s.resolve(ctx, f.Org, cfg, idt.Issuer, idt.Subject, email, strings.TrimSpace(c.Name))
}

// resolve binds the provider identity to an account and the account to a
// seat.
func (s *Service) resolve(ctx context.Context, org uuid.UUID, cfg Config, issuer, subject, email, name string) (Result, error) {
	res := Result{OrgID: org, Email: email}
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Result{}, err
	}
	defer tx.Rollback(ctx)

	// The organisation's provider speaks only for its verified domains.
	var ours bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_sso_domains
		WHERE org_id=$1 AND domain=$2 AND verified_at IS NOT NULL)`, org, DomainOf(email)).Scan(&ours); err != nil {
		return Result{}, err
	}

	err = tx.QueryRow(ctx, `SELECT account_id FROM account_identities WHERE issuer=$1 AND subject=$2`,
		issuer, subject).Scan(&res.AccountID)
	switch {
	case err == nil:
		// Bound before. The address may have moved on at the provider; the
		// binding holds, but only for an identity this organisation owns.
		var owner uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT org_id FROM account_identities WHERE issuer=$1 AND subject=$2`,
			issuer, subject).Scan(&owner); err != nil {
			return Result{}, err
		}
		if owner != org || !ours {
			return Result{}, ErrForeignAddress
		}
	case errors.Is(err, pgx.ErrNoRows):
		if !ours {
			return Result{}, ErrForeignAddress
		}
		err = tx.QueryRow(ctx, `SELECT id FROM accounts WHERE email=$1`, email).Scan(&res.AccountID)
		if errors.Is(err, pgx.ErrNoRows) {
			res.AccountID, res.Created = uuid.New(), true
			if name == "" {
				name = email
			}
			if _, err := tx.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
				VALUES ($1,$2,$3,$4,now())`, res.AccountID, email, NoPassword, name); err != nil {
				return Result{}, err
			}
		} else if err != nil {
			return Result{}, err
		} else {
			res.Linked = true
		}
		if _, err := tx.Exec(ctx, `INSERT INTO account_identities (issuer, subject, account_id, org_id)
			VALUES ($1,$2,$3,$4)`, issuer, subject, res.AccountID, org); err != nil {
			return Result{}, err
		}
	default:
		return Result{}, err
	}

	err = tx.QueryRow(ctx, `SELECT id FROM humans WHERE org_id=$1 AND account_id=$2`, org, res.AccountID).Scan(&res.SeatID)
	if errors.Is(err, pgx.ErrNoRows) {
		res.SeatID = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
			SELECT $1, $2, a.id, a.email, COALESCE(NULLIF(a.display_name, ''), a.email), a.password_hash, $4
			FROM accounts a WHERE a.id=$3`, res.SeatID, org, res.AccountID, cfg.DefaultRole); err != nil {
			return Result{}, err
		}
	} else if err != nil {
		return Result{}, err
	}

	// Under "required" an account that signs in through the provider has no
	// business keeping a password — the same rewrite the switch does, for an
	// account that came after it.
	if cfg.Required {
		if _, err := tx.Exec(ctx, `UPDATE accounts SET password_hash=$2 WHERE id=$1 AND platform_role='user'`,
			res.AccountID, NoPassword); err != nil {
			return Result{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE account_identities SET last_login_at=now() WHERE issuer=$1 AND subject=$2`,
		issuer, subject); err != nil {
		return Result{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE accounts SET last_login_at=now() WHERE id=$1`, res.AccountID); err != nil {
		return Result{}, err
	}
	return res, tx.Commit(ctx)
}

// FlowCookie is the cookie the sealed flow travels in.
const FlowCookie = "covey_sso_flow"
