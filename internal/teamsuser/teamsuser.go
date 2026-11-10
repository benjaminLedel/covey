// Package teamsuser lets an agent act in Teams with a person's own access
// (#712, #713): delegated Microsoft Graph access through the organisation's
// bot registration (#715) — or the app registration it configured for
// delegation. Tokens are sealed with the master key, used only in the
// control plane, and only for the work they were granted for.
//
// Two ways a token comes to be used:
//
//   - The person who wrote: a task a Teams message started runs with the
//     writer's token (people.go), obtained by single sign-on in the chat.
//   - A per-agent delegation: a person connects their Teams to one agent
//     from covey's interface, for runs no Teams message started.
package teamsuser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/msauth"
	"covey/internal/secrets/sealbox"
	"covey/internal/teamsapp"
)

// Scope is what a person grants: their chats, the teams and channels they
// belong to, and sending in both. offline_access gives the refresh token.
const Scope = "openid profile offline_access User.Read Chat.ReadWrite ChatMessage.Send " +
	"Team.ReadBasic.All Channel.ReadBasic.All ChannelMessage.Read.All ChannelMessage.Send"

// Cookie carries the nonce of a delegation in progress.
const Cookie = "covey_teamsuser_grant"

// ErrNoDelegation: nobody has connected their Teams to this agent.
var ErrNoDelegation = errors.New("teamsuser: nobody has connected their Teams to this agent")

// Service holds the tokens.
type Service struct {
	Pool *pgxpool.Pool
	Box  *sealbox.Box
	// Bots resolves an organisation's bot and its delegation app.
	Bots *teamsapp.Service
	Log  *slog.Logger
	HTTP *http.Client
	// GraphBase is Microsoft Graph; empty: https://graph.microsoft.com/v1.0.
	GraphBase string

	mu sync.Mutex
	// access caches a per-agent delegation's access token by organisation
	// and agent; people a writer's by "bot|oid".
	access map[[2]uuid.UUID]cached
	people map[string]cached
}

type cached struct {
	token string
	until time.Time
}

// New builds the service; nil without the bots it delegates through.
func New(pool *pgxpool.Pool, box *sealbox.Box, bots *teamsapp.Service, log *slog.Logger) *Service {
	if box == nil || bots == nil {
		return nil
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Pool: pool, Box: box, Bots: bots, Log: log,
		HTTP:   &http.Client{Timeout: 20 * time.Second},
		access: map[[2]uuid.UUID]cached{}, people: map[string]cached{}}
}

func (s *Service) graph() string {
	if g := strings.TrimRight(strings.TrimSpace(s.GraphBase), "/"); g != "" {
		return g
	}
	return "https://graph.microsoft.com/v1.0"
}

func until(tok msauth.Tokens) time.Time {
	ttl := tok.ExpiresIn
	if ttl < 120 {
		ttl = 3600
	}
	return time.Now().Add(time.Duration(ttl-60) * time.Second)
}

// --- Per-agent delegation (#712) ---

// Delegation is one person's grant to one agent.
type Delegation struct {
	AgentID uuid.UUID  `json:"agent_id"`
	HumanID *uuid.UUID `json:"human_id,omitempty"`
	MSUser  string     `json:"ms_user"`
	Since   time.Time  `json:"since"`
}

func subject(org, agent, human uuid.UUID) string {
	return org.String() + "|" + agent.String() + "|" + human.String()
}

func grantApp(ref teamsapp.DelegationRef) msauth.App {
	app := ref.App
	app.Purpose = "teamsuser-grant:" + ref.BotID.String()
	return app
}

// Start begins a delegation of the signed-in person's Teams to an agent,
// through the organisation's delegation app. The caller has checked that the
// agent belongs to the organisation and the person may manage it.
func (s *Service) Start(ctx context.Context, org, agent, human uuid.UUID, redirectURI string) (signIn, nonce string, err error) {
	ref, err := s.Bots.DelegationFor(ctx, org)
	if err != nil {
		return "", "", err
	}
	return grantApp(ref).Start(subject(org, agent, human), redirectURI, Scope)
}

// Finish completes it: the refresh token is sealed under the agent's id and
// stored. A second delegation to the same agent replaces the first.
func (s *Service) Finish(ctx context.Context, state, cookieNonce, code, redirectURI string) (uuid.UUID, string, error) {
	// The organisation's app is the state's key, and the organisation is
	// named inside it: read it unchecked, then let that app verify.
	peek, err := msauth.PeekSubject(state)
	if err != nil {
		return uuid.Nil, "", err
	}
	orgPart, _, _ := strings.Cut(peek, "|")
	org, err := uuid.Parse(orgPart)
	if err != nil {
		return uuid.Nil, "", msauth.ErrState
	}
	ref, err := s.Bots.DelegationFor(ctx, org)
	if err != nil {
		return uuid.Nil, "", msauth.ErrState
	}
	sub, tok, claims, err := grantApp(ref).Finish(ctx, state, cookieNonce, code, redirectURI, Scope)
	if err != nil {
		return uuid.Nil, "", err
	}
	parts := strings.Split(sub, "|")
	if len(parts) != 3 {
		return uuid.Nil, "", msauth.ErrState
	}
	sOrg, e1 := uuid.Parse(parts[0])
	agent, e2 := uuid.Parse(parts[1])
	human, e3 := uuid.Parse(parts[2])
	if e1 != nil || e2 != nil || e3 != nil || sOrg != org {
		return uuid.Nil, "", msauth.ErrState
	}
	if tok.RefreshToken == "" {
		return agent, "", errors.New("teamsuser: Microsoft returned no refresh token (offline_access missing?)")
	}
	nonce, ct, err := s.Box.Seal(aad(agent), tok.RefreshToken)
	if err != nil {
		return agent, "", err
	}
	tag, err := s.Pool.Exec(ctx, `INSERT INTO teamsuser_delegations
		  (agent_id, org_id, human_id, ms_user, tenant_id, nonce, ciphertext)
		SELECT $1, $2, $3, $4, $5, $6, $7 WHERE EXISTS (SELECT 1 FROM agents WHERE id = $1 AND org_id = $2)
		ON CONFLICT (agent_id) DO UPDATE SET human_id = EXCLUDED.human_id, ms_user = EXCLUDED.ms_user,
		  tenant_id = EXCLUDED.tenant_id, nonce = EXCLUDED.nonce, ciphertext = EXCLUDED.ciphertext,
		  created_at = now(), updated_at = now()`,
		agent, org, human, claims.Who(), claims.Tid, nonce, ct)
	if err != nil {
		return agent, "", err
	}
	if tag.RowsAffected() == 0 {
		return agent, "", errors.New("teamsuser: the agent no longer belongs to this organisation")
	}
	s.forget(agent)
	if tok.AccessToken != "" {
		s.mu.Lock()
		s.access[[2]uuid.UUID{org, agent}] = cached{token: tok.AccessToken, until: until(tok)}
		s.mu.Unlock()
	}
	return agent, claims.Who(), nil
}

func aad(agent uuid.UUID) []byte { return []byte("teamsuser:" + agent.String()) }

// List returns an organisation's delegations.
func (s *Service) List(ctx context.Context, org uuid.UUID) ([]Delegation, error) {
	rows, err := s.Pool.Query(ctx, `SELECT agent_id, human_id, ms_user, created_at
		FROM teamsuser_delegations WHERE org_id = $1 ORDER BY created_at`, org)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Delegation{}
	for rows.Next() {
		var d Delegation
		if err := rows.Scan(&d.AgentID, &d.HumanID, &d.MSUser, &d.Since); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Revoke deletes an agent's delegation within the organisation.
func (s *Service) Revoke(ctx context.Context, org, agent uuid.UUID) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM teamsuser_delegations WHERE agent_id = $1 AND org_id = $2`, agent, org)
	if err != nil {
		return err
	}
	s.forget(agent)
	if tag.RowsAffected() == 0 {
		return ErrNoDelegation
	}
	return nil
}

// Delegator answers who an agent's delegation belongs to.
func (s *Service) Delegator(ctx context.Context, org, agent uuid.UUID) (*uuid.UUID, error) {
	var h *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT human_id FROM teamsuser_delegations WHERE agent_id = $1 AND org_id = $2`, agent, org).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoDelegation
	}
	return h, err
}

func (s *Service) forget(agent uuid.UUID) {
	s.mu.Lock()
	for k := range s.access {
		if k[1] == agent {
			delete(s.access, k)
		}
	}
	s.mu.Unlock()
}

// token is a per-agent delegation's access token, refreshed through the
// organisation's delegation app when it ran out; a rotated refresh token is
// sealed and stored again.
func (s *Service) token(ctx context.Context, org, agent uuid.UUID) (string, error) {
	s.mu.Lock()
	c, ok := s.access[[2]uuid.UUID{org, agent}]
	s.mu.Unlock()
	if ok && time.Now().Before(c.until) {
		return c.token, nil
	}
	var nonce, ct []byte
	err := s.Pool.QueryRow(ctx, `SELECT nonce, ciphertext FROM teamsuser_delegations
		WHERE agent_id = $1 AND org_id = $2`, agent, org).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNoDelegation
	}
	if err != nil {
		return "", err
	}
	ref, err := s.Bots.DelegationFor(ctx, org)
	if err != nil {
		return "", err
	}
	refresh, err := s.Box.Open("teamsuser delegation", aad(agent), nonce, ct)
	if err != nil {
		return "", err
	}
	tok, err := ref.App.Refresh(ctx, refresh, Scope)
	if err != nil {
		return "", fmt.Errorf("teamsuser: the delegation no longer works — the person has to connect their Teams again (%w)", err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if n, c2, err := s.Box.Seal(aad(agent), tok.RefreshToken); err == nil {
			_, _ = s.Pool.Exec(ctx, `UPDATE teamsuser_delegations SET nonce = $2, ciphertext = $3, updated_at = now()
				WHERE agent_id = $1`, agent, n, c2)
		}
	}
	s.mu.Lock()
	s.access[[2]uuid.UUID{org, agent}] = cached{token: tok.AccessToken, until: until(tok)}
	s.mu.Unlock()
	return tok.AccessToken, nil
}
