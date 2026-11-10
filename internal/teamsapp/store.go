package teamsapp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotLinked: no organisation has linked this Microsoft tenant.
	ErrNotLinked = errors.New("teamsapp: tenant not linked to an organisation")
	// ErrTenantTaken: the tenant is linked to another organisation.
	ErrTenantTaken = errors.New("teamsapp: this Microsoft tenant is linked to another organisation")
	// ErrUnknownConversation: the conversation is not one of this
	// organisation's, or the bot has never seen it.
	ErrUnknownConversation = errors.New("teamsapp: unknown conversation")
)

// Store keeps what the shared bot knows, per tenant. Every read an agent's
// action leads to goes through the organisation: a conversation or team is
// only found when its tenant is linked to the organisation asking.
type Store struct {
	Pool *pgxpool.Pool
}

// Tenant is a linked Microsoft tenant.
type Tenant struct {
	TenantID string `json:"tenant_id"`
	LinkedBy string `json:"linked_by"`
}

// Team is a team the bot is installed in.
type Team struct {
	TenantID   string `json:"tenant_id"`
	TeamID     string `json:"team_id"`
	Name       string `json:"name"`
	ServiceURL string `json:"-"`
}

// Conversation is a chat or channel thread the bot has seen or opened.
type Conversation struct {
	TenantID       string
	ConversationID string
	Kind           string // personal | groupChat | channel
	TeamID         string
	ChannelID      string
	AgentID        *uuid.UUID
	ServiceURL     string
}

// OrgOfTenant answers which organisation a tenant belongs to.
func (s Store) OrgOfTenant(ctx context.Context, tenantID string) (uuid.UUID, error) {
	var org uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT org_id FROM teamsapp_tenants WHERE tenant_id = $1`, tenantID).Scan(&org)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotLinked
	}
	return org, err
}

// Tenants lists the tenants an organisation has linked.
func (s Store) Tenants(ctx context.Context, orgID uuid.UUID) ([]Tenant, error) {
	rows, err := s.Pool.Query(ctx, `SELECT tenant_id, linked_by FROM teamsapp_tenants
		WHERE org_id = $1 ORDER BY linked_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tenant{}
	for rows.Next() {
		var t Tenant
		if err := rows.Scan(&t.TenantID, &t.LinkedBy); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Link ties a tenant to an organisation. Linking it again to the same one is
// a no-op; a tenant another organisation holds is refused, never moved.
func (s Store) Link(ctx context.Context, orgID uuid.UUID, tenantID, by string) error {
	var holder uuid.UUID
	err := s.Pool.QueryRow(ctx, `INSERT INTO teamsapp_tenants (tenant_id, org_id, linked_by)
		VALUES ($1, $2, $3)
		ON CONFLICT (tenant_id) DO UPDATE SET linked_by = teamsapp_tenants.linked_by
		RETURNING org_id`, tenantID, orgID, by).Scan(&holder)
	if err != nil {
		return err
	}
	if holder != orgID {
		return ErrTenantTaken
	}
	return nil
}

// Unlink removes an organisation's tenant and, through the foreign keys,
// everything the bot knew of it.
func (s Store) Unlink(ctx context.Context, orgID uuid.UUID, tenantID string) error {
	tag, err := s.Pool.Exec(ctx, `DELETE FROM teamsapp_tenants WHERE tenant_id = $1 AND org_id = $2`, tenantID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotLinked
	}
	return nil
}

// RememberTeam records a team the bot was seen in.
func (s Store) RememberTeam(ctx context.Context, t Team) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_teams (tenant_id, team_id, name, service_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant_id, team_id) DO UPDATE SET
		  name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE teamsapp_teams.name END,
		  service_url = EXCLUDED.service_url, updated_at = now()`,
		t.TenantID, t.TeamID, t.Name, t.ServiceURL)
	return err
}

// Teams lists the teams of an organisation's tenants.
func (s Store) Teams(ctx context.Context, orgID uuid.UUID) ([]Team, error) {
	rows, err := s.Pool.Query(ctx, `SELECT t.tenant_id, t.team_id, t.name, t.service_url
		FROM teamsapp_teams t JOIN teamsapp_tenants l ON l.tenant_id = t.tenant_id
		WHERE l.org_id = $1 ORDER BY t.name, t.team_id`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.TenantID, &t.TeamID, &t.Name, &t.ServiceURL); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Conversation finds a conversation of this organisation.
func (s Store) Conversation(ctx context.Context, orgID uuid.UUID, conversationID string) (Conversation, error) {
	var c Conversation
	err := s.Pool.QueryRow(ctx, `SELECT c.tenant_id, c.conversation_id, c.kind, c.team_id, c.channel_id,
		       c.agent_id, c.service_url
		FROM teamsapp_conversations c JOIN teamsapp_tenants l ON l.tenant_id = c.tenant_id
		WHERE l.org_id = $1 AND c.conversation_id = $2`, orgID, conversationID).
		Scan(&c.TenantID, &c.ConversationID, &c.Kind, &c.TeamID, &c.ChannelID, &c.AgentID, &c.ServiceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrUnknownConversation
	}
	return c, err
}

// conversationIn reads a conversation by tenant, for the router.
func (s Store) conversationIn(ctx context.Context, tenantID, conversationID string) (Conversation, error) {
	var c Conversation
	err := s.Pool.QueryRow(ctx, `SELECT tenant_id, conversation_id, kind, team_id, channel_id, agent_id, service_url
		FROM teamsapp_conversations WHERE tenant_id = $1 AND conversation_id = $2`, tenantID, conversationID).
		Scan(&c.TenantID, &c.ConversationID, &c.Kind, &c.TeamID, &c.ChannelID, &c.AgentID, &c.ServiceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrUnknownConversation
	}
	return c, err
}

// Bind records a conversation and the colleague it belongs to.
func (s Store) Bind(ctx context.Context, c Conversation) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_conversations
		  (tenant_id, conversation_id, kind, team_id, channel_id, agent_id, service_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (tenant_id, conversation_id) DO UPDATE SET
		  agent_id = EXCLUDED.agent_id, service_url = EXCLUDED.service_url, updated_at = now()`,
		c.TenantID, c.ConversationID, c.Kind, c.TeamID, c.ChannelID, c.AgentID, c.ServiceURL)
	return err
}

// ChannelAgent is the colleague a channel defaults to.
func (s Store) ChannelAgent(ctx context.Context, tenantID, channelID string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT agent_id FROM teamsapp_channel_agents
		WHERE tenant_id = $1 AND channel_id = $2`, tenantID, channelID).Scan(&id)
	return id, err == nil
}

// ChannelAgents lists an organisation's channel defaults: channel → agent.
func (s Store) ChannelAgents(ctx context.Context, orgID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT c.channel_id, c.agent_id FROM teamsapp_channel_agents c
		JOIN teamsapp_tenants l ON l.tenant_id = c.tenant_id WHERE l.org_id = $1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var ch string
		var id uuid.UUID
		if err := rows.Scan(&ch, &id); err != nil {
			return nil, err
		}
		out[ch] = id
	}
	return out, rows.Err()
}

// SetChannelAgent sets (agent non-nil) or clears a channel's default. The
// caller has checked that the agent belongs to the organisation; the tenant
// is checked here.
func (s Store) SetChannelAgent(ctx context.Context, orgID uuid.UUID, tenantID, channelID string, agentID *uuid.UUID) error {
	var ok bool
	if err := s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM teamsapp_tenants WHERE tenant_id = $1 AND org_id = $2)`,
		tenantID, orgID).Scan(&ok); err != nil {
		return err
	}
	if !ok {
		return ErrNotLinked
	}
	if agentID == nil {
		_, err := s.Pool.Exec(ctx, `DELETE FROM teamsapp_channel_agents WHERE tenant_id = $1 AND channel_id = $2`, tenantID, channelID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_channel_agents (tenant_id, channel_id, agent_id)
		VALUES ($1, $2, $3) ON CONFLICT (tenant_id, channel_id) DO UPDATE SET agent_id = EXCLUDED.agent_id`,
		tenantID, channelID, *agentID)
	return err
}
