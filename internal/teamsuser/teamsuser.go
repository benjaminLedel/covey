// Package teamsuser lets a person hand an agent their own Teams (#712, proof
// of concept): delegated Microsoft Graph access, granted by the person
// signing in, stored sealed per agent, used only in the control plane and
// only for the agent it was granted to.
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
)

// Scope is what a person grants: their chats, the teams and channels they
// belong to, and sending in both. offline_access gives the refresh token.
const Scope = "openid profile offline_access User.Read Chat.ReadWrite ChatMessage.Send " +
	"Team.ReadBasic.All Channel.ReadBasic.All ChannelMessage.Read.All ChannelMessage.Send"

// Cookie carries the nonce of a delegation in progress.
const Cookie = "covey_teamsuser_grant"

var (
	// ErrNoDelegation: nobody has connected their Teams to this agent.
	ErrNoDelegation = errors.New("teamsuser: nobody has connected their Teams to this agent")
	errNoApp        = errors.New("teamsuser: no app configured on this installation")
)

// Config is the app registration for delegated access — a registration of
// its own, beside the installation's Teams bot.
type Config struct {
	AppID       string
	AppPassword string
	LoginBase   string
	// GraphBase is Microsoft Graph; empty: https://graph.microsoft.com/v1.0.
	GraphBase string
	// SSO is the installation's Teams app, through which the person writing
	// to the bot signs in (#713). nil: only per-agent delegations.
	SSO *msauth.App
}

// Service holds the delegations.
type Service struct {
	Pool *pgxpool.Pool
	Box  *sealbox.Box
	Log  *slog.Logger
	HTTP *http.Client

	cfg Config
	mu  sync.Mutex
	// access caches a person's short-lived access token per organisation and
	// agent — the organisation in the key as well, so a cache hit never
	// answers a question the database would have refused.
	access map[[2]uuid.UUID]cached
	// people caches a person's access token by "tenant|oid".
	people map[string]cached
	sso    *msauth.App
}

type cached struct {
	token string
	until time.Time
}

// New builds the service, or nil when the installation has no app for it or
// no master key to seal tokens with.
func New(pool *pgxpool.Pool, box *sealbox.Box, cfg Config, log *slog.Logger) *Service {
	own := strings.TrimSpace(cfg.AppID) != "" && strings.TrimSpace(cfg.AppPassword) != ""
	if (!own && cfg.SSO == nil) || box == nil {
		return nil
	}
	if !own {
		cfg.AppID, cfg.AppPassword = "", ""
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Pool: pool, Box: box, Log: log, cfg: cfg,
		HTTP: &http.Client{Timeout: 20 * time.Second}, access: map[[2]uuid.UUID]cached{},
		people: map[string]cached{}, sso: cfg.SSO}
}

// OwnApp: the per-agent delegation (#712) has an app registration.
func (s *Service) OwnApp() bool { return s != nil && s.cfg.AppID != "" }

func (s *Service) auth() msauth.App {
	return msauth.App{ClientID: s.cfg.AppID, Secret: s.cfg.AppPassword, LoginBase: s.cfg.LoginBase, Purpose: "teamsuser-grant"}
}

func (s *Service) graph() string {
	if g := strings.TrimRight(strings.TrimSpace(s.cfg.GraphBase), "/"); g != "" {
		return g
	}
	return "https://graph.microsoft.com/v1.0"
}

// Delegation is one person's grant to one agent.
type Delegation struct {
	AgentID uuid.UUID  `json:"agent_id"`
	HumanID *uuid.UUID `json:"human_id,omitempty"`
	MSUser  string     `json:"ms_user"`
	Since   time.Time  `json:"since"`
}

// subject is what the signed state carries: which organisation, agent and
// person the sign-in is for.
func subject(org, agent, human uuid.UUID) string {
	return org.String() + "|" + agent.String() + "|" + human.String()
}

// Start begins a delegation of the signed-in person's Teams to an agent.
// The caller has checked that the agent belongs to the organisation and that
// the person may manage it.
func (s *Service) Start(org, agent, human uuid.UUID, redirectURI string) (signIn, nonce string, err error) {
	if s == nil || s.cfg.AppID == "" {
		return "", "", errNoApp
	}
	return s.auth().Start(subject(org, agent, human), redirectURI, Scope)
}

// Finish completes it: the code becomes a refresh token, sealed under the
// agent's id and stored. A second delegation to the same agent replaces the
// first — one person per agent in the proof of concept.
func (s *Service) Finish(ctx context.Context, state, cookieNonce, code, redirectURI string) (uuid.UUID, string, error) {
	if s == nil || s.cfg.AppID == "" {
		return uuid.Nil, "", errNoApp
	}
	sub, tok, claims, err := s.auth().Finish(ctx, state, cookieNonce, code, redirectURI, Scope)
	if err != nil {
		return uuid.Nil, "", err
	}
	parts := strings.Split(sub, "|")
	if len(parts) != 3 {
		return uuid.Nil, "", msauth.ErrState
	}
	org, e1 := uuid.Parse(parts[0])
	agent, e2 := uuid.Parse(parts[1])
	human, e3 := uuid.Parse(parts[2])
	if e1 != nil || e2 != nil || e3 != nil {
		return uuid.Nil, "", msauth.ErrState
	}
	if tok.RefreshToken == "" {
		return agent, "", errors.New("teamsuser: Microsoft returned no refresh token (offline_access missing?)")
	}
	nonce, ct, err := s.Box.Seal(aad(agent), tok.RefreshToken)
	if err != nil {
		return agent, "", err
	}
	// The agent must still be the organisation's: the state was signed when
	// the sign-in started, the agent could have moved or gone since.
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
		s.remember(org, agent, tok)
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

// Delegator answers who an agent's delegation belongs to (for the revoke
// check: the person themself or an org admin).
func (s *Service) Delegator(ctx context.Context, org, agent uuid.UUID) (*uuid.UUID, error) {
	var h *uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT human_id FROM teamsuser_delegations WHERE agent_id = $1 AND org_id = $2`, agent, org).Scan(&h)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNoDelegation
	}
	return h, err
}

func (s *Service) remember(org, agent uuid.UUID, tok msauth.Tokens) {
	ttl := tok.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	s.mu.Lock()
	s.access[[2]uuid.UUID{org, agent}] = cached{token: tok.AccessToken, until: time.Now().Add(time.Duration(ttl-60) * time.Second)}
	s.mu.Unlock()
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

// token is the person's access token for this agent, refreshed when it ran
// out. Microsoft rotates the refresh token on use; the new one is sealed and
// stored, or the delegation would die at the next refresh.
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
	refresh, err := s.Box.Open("teamsuser delegation", aad(agent), nonce, ct)
	if err != nil {
		return "", err
	}
	tok, err := s.auth().Refresh(ctx, refresh, Scope)
	if err != nil {
		return "", fmt.Errorf("teamsuser: the delegation no longer works — the person has to connect their Teams again (%w)", err)
	}
	if tok.RefreshToken != "" && tok.RefreshToken != refresh {
		if n, c2, err := s.Box.Seal(aad(agent), tok.RefreshToken); err == nil {
			_, _ = s.Pool.Exec(ctx, `UPDATE teamsuser_delegations SET nonce = $2, ciphertext = $3, updated_at = now()
				WHERE agent_id = $1`, agent, n, c2)
		}
	}
	s.remember(org, agent, tok)
	return tok.AccessToken, nil
}
