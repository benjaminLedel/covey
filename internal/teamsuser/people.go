package teamsuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/msauth"
)

// The agent works as the person who wrote (#713). Single sign-on in the chat
// with the installation's Teams app gives covey a token for exactly the
// person writing; it is kept per Microsoft user, and a task from that
// person's message uses it and nothing else.

// ErrNoPerson: the person a task works for has not signed in (or no longer
// refreshes).
var ErrNoPerson = errors.New("teamsuser: the person this task works for has not signed in to covey in Teams yet")

func personAAD(tenant, oid string) []byte { return []byte("teamsuser-person:" + tenant + ":" + oid) }

// HasPerson answers whether covey holds a token for this Microsoft user.
func (s *Service) HasPerson(ctx context.Context, tenant, oid string) bool {
	if s == nil || s.sso == nil || tenant == "" || oid == "" {
		return false
	}
	var ok bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teamsuser_people WHERE tenant_id = $1 AND oid = $2)`, tenant, oid).Scan(&ok)
	return ok
}

// SSOEnabled: the installation's Teams app is there to sign people in.
func (s *Service) SSOEnabled() bool { return s != nil && s.sso != nil }

// Exchange trades the token Teams handed the bot for the writer of a message
// (signin/tokenExchange) for a delegated Graph token, checks it is the same
// person (Graph's /me against the activity's aadObjectId), and stores it.
func (s *Service) Exchange(ctx context.Context, tenant, oid, ssoToken string) error {
	if !s.SSOEnabled() {
		return errNoApp
	}
	tok, err := s.sso.OnBehalfOf(ctx, tenant, ssoToken, Scope)
	if err != nil {
		return err
	}
	return s.keepPerson(ctx, tenant, oid, tok)
}

// FinishPersonSignIn completes the sign-in link the bot offers when single
// sign-on did not work: the subject names the tenant and the person who
// wrote, and the Microsoft account that signed in has to be that person.
func (s *Service) FinishPersonSignIn(ctx context.Context, state, cookieNonce, code, redirectURI string) (tenant, oid string, err error) {
	if !s.SSOEnabled() {
		return "", "", errNoApp
	}
	sub, tok, claims, err := s.sso.Finish(ctx, state, cookieNonce, code, redirectURI, Scope)
	if err != nil {
		return "", "", err
	}
	tenant, oid, ok := strings.Cut(sub, "|")
	if !ok {
		return "", "", msauth.ErrState
	}
	if !strings.EqualFold(claims.Tid, tenant) || !strings.EqualFold(claims.Oid, oid) {
		return "", "", fmt.Errorf("teamsuser: signed in as %s — sign in as the person who wrote to the bot", claims.Who())
	}
	return tenant, oid, s.keepPerson(ctx, tenant, oid, tok)
}

// StartPersonSignIn is the fallback link's first half.
func (s *Service) StartPersonSignIn(tenant, oid, redirectURI string) (string, string, error) {
	if !s.SSOEnabled() {
		return "", "", errNoApp
	}
	return s.sso.Start(tenant+"|"+oid, redirectURI, Scope)
}

func (s *Service) keepPerson(ctx context.Context, tenant, oid string, tok msauth.Tokens) error {
	if tok.RefreshToken == "" || tok.AccessToken == "" {
		return errors.New("teamsuser: Microsoft returned no refresh token (offline_access missing?)")
	}
	g := graphCall{s: s, token: tok.AccessToken}
	var me struct {
		ID                string `json:"id"`
		UserPrincipalName string `json:"userPrincipalName"`
	}
	if err := g.get(ctx, "/me?$select=id,userPrincipalName", &me); err != nil {
		return err
	}
	if !strings.EqualFold(me.ID, oid) {
		return errors.New("teamsuser: the token belongs to another person than the one who wrote")
	}
	nonce, ct, err := s.Box.Seal(personAAD(tenant, oid), tok.RefreshToken)
	if err != nil {
		return err
	}
	if _, err := s.Pool.Exec(ctx, `INSERT INTO teamsuser_people (tenant_id, oid, ms_user, nonce, ciphertext)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant_id, oid) DO UPDATE SET ms_user = EXCLUDED.ms_user, nonce = EXCLUDED.nonce,
		  ciphertext = EXCLUDED.ciphertext, updated_at = now()`, tenant, oid, me.UserPrincipalName, nonce, ct); err != nil {
		return err
	}
	s.mu.Lock()
	s.people[tenant+"|"+oid] = cached{token: tok.AccessToken, until: time.Now().Add(time.Duration(max(tok.ExpiresIn, 120)-60) * time.Second)}
	s.mu.Unlock()
	return nil
}

// SetTaskPerson records whose message a task is working on.
func (s *Service) SetTaskPerson(ctx context.Context, taskID uuid.UUID, tenant, oid string) error {
	if s == nil || taskID == uuid.Nil || oid == "" {
		return nil
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO teams_task_people (task_id, tenant_id, oid) VALUES ($1, $2, $3)
		ON CONFLICT (task_id) DO UPDATE SET tenant_id = EXCLUDED.tenant_id, oid = EXCLUDED.oid`, taskID, tenant, oid)
	return err
}

// taskPerson answers whose task it is; ok=false for a task no Teams message
// started (a heartbeat, a task from covey itself).
func (s *Service) taskPerson(ctx context.Context, org, taskID uuid.UUID) (tenant, oid string, ok bool, err error) {
	if taskID == uuid.Nil {
		return "", "", false, nil
	}
	var origin string
	var t, o *string
	err = s.Pool.QueryRow(ctx, `SELECT t.origin, p.tenant_id, p.oid FROM backlog_tasks t
		LEFT JOIN teams_task_people p ON p.task_id = t.id WHERE t.id = $1 AND t.org_id = $2`, taskID, org).Scan(&origin, &t, &o)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	if t != nil && o != nil {
		return *t, *o, true, nil
	}
	// A task a Teams message started belongs to a person even when the
	// record of who is missing; it must not fall back to the agent's own
	// delegation and act as somebody else.
	if strings.HasPrefix(origin, "webhook:teamsapp") {
		return "", "", true, ErrNoPerson
	}
	return "", "", false, nil
}

// personToken is the person's access token, refreshed when it ran out, the
// rotated refresh token sealed again. The tenant has to belong to the
// agent's organisation.
func (s *Service) personToken(ctx context.Context, org uuid.UUID, tenant, oid string) (string, error) {
	key := tenant + "|" + oid
	s.mu.Lock()
	c, ok := s.people[key]
	s.mu.Unlock()
	var nonce, ct []byte
	err := s.Pool.QueryRow(ctx, `SELECT p.nonce, p.ciphertext FROM teamsuser_people p
		JOIN teamsapp_tenants l ON l.tenant_id = p.tenant_id
		WHERE p.tenant_id = $1 AND p.oid = $2 AND l.org_id = $3`, tenant, oid, org).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoPerson
	}
	if err != nil {
		return "", err
	}
	if ok && time.Now().Before(c.until) {
		return c.token, nil
	}
	refresh, err := s.Box.Open("teamsuser person", personAAD(tenant, oid), nonce, ct)
	if err != nil {
		return "", err
	}
	tok, err := s.sso.Refresh(ctx, refresh, Scope)
	if err != nil {
		return "", fmt.Errorf("%w (%v)", ErrNoPerson, err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if n, c2, err := s.Box.Seal(personAAD(tenant, oid), tok.RefreshToken); err == nil {
			_, _ = s.Pool.Exec(ctx, `UPDATE teamsuser_people SET nonce = $3, ciphertext = $4, updated_at = now()
				WHERE tenant_id = $1 AND oid = $2`, tenant, oid, n, c2)
		}
	}
	s.mu.Lock()
	s.people[key] = cached{token: tok.AccessToken, until: time.Now().Add(time.Duration(max(tok.ExpiresIn, 120)-60) * time.Second)}
	s.mu.Unlock()
	return tok.AccessToken, nil
}

// tokenFor picks whose token an action runs with: the person a Teams
// message started the task for, if there is one — and then only theirs —
// otherwise the agent's own delegation (#712).
func (s *Service) tokenFor(ctx context.Context, org, agent, taskID uuid.UUID) (string, error) {
	tenant, oid, ok, err := s.taskPerson(ctx, org, taskID)
	if err != nil {
		return "", err
	}
	if ok {
		if s.sso == nil {
			return "", ErrNoPerson
		}
		return s.personToken(ctx, org, tenant, oid)
	}
	if s.cfg.AppID == "" {
		return "", ErrNoDelegation
	}
	return s.token(ctx, org, agent)
}

// Pending keeps a message while its writer signs in, and hands it back.
func (s *Service) KeepPending(ctx context.Context, tenant, oid string, activity json.RawMessage) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_pending (tenant_id, oid, activity) VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id, oid) DO UPDATE SET activity = EXCLUDED.activity, created_at = now()`, tenant, oid, activity)
	return err
}

// TakePending returns and removes the kept message, if one is younger than a
// day.
func (s *Service) TakePending(ctx context.Context, tenant, oid string) (json.RawMessage, bool) {
	var raw json.RawMessage
	err := s.Pool.QueryRow(ctx, `DELETE FROM teamsapp_pending WHERE tenant_id = $1 AND oid = $2
		AND created_at > now() - interval '1 day' RETURNING activity`, tenant, oid).Scan(&raw)
	return raw, err == nil
}
