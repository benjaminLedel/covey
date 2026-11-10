package teamsapp

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrUnknownConversation: the conversation is not one of this bot's, or the
// bot has never seen it.
var ErrUnknownConversation = errors.New("teamsapp: unknown conversation")

// Store keeps what a bot knows. Everything is keyed by the bot, and the bot
// belongs to exactly one organisation: what one organisation's bot has seen
// is never found through another's.
type Store struct {
	Pool *pgxpool.Pool
}

// Team is a team the bot is installed in.
type Team struct {
	BotID      uuid.UUID `json:"-"`
	TeamID     string    `json:"team_id"`
	Name       string    `json:"name"`
	ServiceURL string    `json:"-"`
}

// Conversation is a chat or channel thread the bot has seen or opened.
type Conversation struct {
	BotID          uuid.UUID
	ConversationID string
	Kind           string // personal | groupChat | channel
	TeamID         string
	ChannelID      string
	AgentID        *uuid.UUID
	ServiceURL     string
}

// RememberTeam records a team the bot was seen in.
func (s Store) RememberTeam(ctx context.Context, t Team) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_teams (bot_id, team_id, name, service_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (bot_id, team_id) DO UPDATE SET
		  name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE teamsapp_teams.name END,
		  service_url = EXCLUDED.service_url, updated_at = now()`,
		t.BotID, t.TeamID, t.Name, t.ServiceURL)
	return err
}

// Teams lists the teams a bot is installed in.
func (s Store) Teams(ctx context.Context, bot uuid.UUID) ([]Team, error) {
	rows, err := s.Pool.Query(ctx, `SELECT bot_id, team_id, name, service_url FROM teamsapp_teams
		WHERE bot_id = $1 ORDER BY name, team_id`, bot)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Team{}
	for rows.Next() {
		var t Team
		if err := rows.Scan(&t.BotID, &t.TeamID, &t.Name, &t.ServiceURL); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Conversation finds one of a bot's conversations.
func (s Store) Conversation(ctx context.Context, bot uuid.UUID, conversationID string) (Conversation, error) {
	var c Conversation
	err := s.Pool.QueryRow(ctx, `SELECT bot_id, conversation_id, kind, team_id, channel_id, agent_id, service_url
		FROM teamsapp_conversations WHERE bot_id = $1 AND conversation_id = $2`, bot, conversationID).
		Scan(&c.BotID, &c.ConversationID, &c.Kind, &c.TeamID, &c.ChannelID, &c.AgentID, &c.ServiceURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrUnknownConversation
	}
	return c, err
}

// Bind records a conversation and the colleague it belongs to.
func (s Store) Bind(ctx context.Context, c Conversation) error {
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_conversations
		  (bot_id, conversation_id, kind, team_id, channel_id, agent_id, service_url)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (bot_id, conversation_id) DO UPDATE SET
		  agent_id = EXCLUDED.agent_id, service_url = EXCLUDED.service_url, updated_at = now()`,
		c.BotID, c.ConversationID, c.Kind, c.TeamID, c.ChannelID, c.AgentID, c.ServiceURL)
	return err
}

// ChannelAgent is the colleague a channel defaults to.
func (s Store) ChannelAgent(ctx context.Context, bot uuid.UUID, channelID string) (uuid.UUID, bool) {
	var id uuid.UUID
	err := s.Pool.QueryRow(ctx, `SELECT agent_id FROM teamsapp_channel_agents
		WHERE bot_id = $1 AND channel_id = $2`, bot, channelID).Scan(&id)
	return id, err == nil
}

// ChannelAgents lists a bot's channel defaults: channel → agent.
func (s Store) ChannelAgents(ctx context.Context, bot uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := s.Pool.Query(ctx, `SELECT channel_id, agent_id FROM teamsapp_channel_agents WHERE bot_id = $1`, bot)
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
// caller has checked that the agent belongs to the bot's organisation.
func (s Store) SetChannelAgent(ctx context.Context, bot uuid.UUID, channelID string, agentID *uuid.UUID) error {
	if agentID == nil {
		_, err := s.Pool.Exec(ctx, `DELETE FROM teamsapp_channel_agents WHERE bot_id = $1 AND channel_id = $2`, bot, channelID)
		return err
	}
	_, err := s.Pool.Exec(ctx, `INSERT INTO teamsapp_channel_agents (bot_id, channel_id, agent_id)
		VALUES ($1, $2, $3) ON CONFLICT (bot_id, channel_id) DO UPDATE SET agent_id = EXCLUDED.agent_id`,
		bot, channelID, *agentID)
	return err
}
