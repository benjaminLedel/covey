package teamsapp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/google/uuid"

	"covey/internal/agents"
)

// ChannelRef is one channel as an agent sees it.
type ChannelRef struct {
	Team      string `json:"team"`
	Channel   string `json:"channel"`
	ChannelID string `json:"channel_id"`
	TenantID  string `json:"tenant_id"`
	teamID    string
	service   string
}

// Execute runs one of an agent's teamsapp actions. ACCESS, scope and guard
// rails have been checked by the caller; what is checked here is the one
// thing only this package can: that the conversation or channel is the
// agent's organisation's. The connector host always comes from the records,
// never from params — an agent that passes a service_url is ignored.
func (s *Service) Execute(ctx context.Context, agent agents.Agent, _ uuid.UUID, action string, params json.RawMessage) (any, error) {
	if s == nil {
		return nil, errNoApp
	}
	var in struct {
		ConversationID string `json:"conversation_id"`
		ReplyTo        string `json:"reply_to_activity_id"`
		Channel        string `json:"channel"`
		Text           string `json:"text"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fmt.Errorf("teamsapp %s: %w", action, err)
		}
	}
	switch action {
	case "list_channels":
		return s.channels(ctx, agent)
	case "reply":
		return s.reply(ctx, agent, in.ConversationID, in.ReplyTo, in.Text)
	case "post":
		return s.post(ctx, agent, in.Channel, in.Text)
	}
	return nil, fmt.Errorf("teamsapp: unknown action %q (reply, list_channels, post)", action)
}

func sender(a agents.Agent) string {
	if a.DisplayName != "" {
		return a.DisplayName
	}
	return a.Slug
}

// reply answers into a conversation of the organisation. A personal or
// group chat is the colleague's it is bound to; a channel thread is open to
// any colleague of the organisation, as the channel is.
func (s *Service) reply(ctx context.Context, agent agents.Agent, convID, replyTo, text string) (any, error) {
	if strings.TrimSpace(convID) == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("teamsapp reply: conversation_id and text are required")
	}
	conv, err := s.Store.Conversation(ctx, agent.OrgID, convID)
	if err != nil {
		return nil, fmt.Errorf("teamsapp reply: %w — reply only in a conversation you were addressed in", err)
	}
	if conv.Kind != "channel" && (conv.AgentID == nil || *conv.AgentID != agent.ID) {
		return nil, fmt.Errorf("teamsapp reply: this chat belongs to another colleague")
	}
	c, err := s.connector()
	if err != nil {
		return nil, err
	}
	res, err := c.SendActivity(ctx, conv.ServiceURL, conv.ConversationID, replyTo, teams.SenderActivity(sender(agent), text))
	if err != nil {
		return nil, err
	}
	return map[string]string{"activity_id": res.ID, "conversation_id": conv.ConversationID}, nil
}

func (s *Service) channels(ctx context.Context, agent agents.Agent) ([]ChannelRef, error) {
	return s.Channels(ctx, agent.OrgID)
}

// Channels lists the channels of every team the bot is installed in for the
// organisation's tenants, fresh from Teams.
func (s *Service) Channels(ctx context.Context, orgID uuid.UUID) ([]ChannelRef, error) {
	if s == nil {
		return nil, errNoApp
	}
	teamsList, err := s.Store.Teams(ctx, orgID)
	if err != nil {
		return nil, err
	}
	c, err := s.connector()
	if err != nil {
		return nil, err
	}
	out := []ChannelRef{}
	for _, t := range teamsList {
		chans, err := c.ListChannels(ctx, t.ServiceURL, t.TeamID)
		if err != nil {
			s.Log.Warn("teamsapp: channels not listed", "team", t.TeamID, "err", err)
			continue
		}
		for _, ch := range chans {
			out = append(out, ChannelRef{Team: t.Name, Channel: ch.Name, ChannelID: ch.ID,
				TenantID: t.TenantID, teamID: t.TeamID, service: t.ServiceURL})
		}
	}
	return out, nil
}

// post starts a thread in a channel of the organisation and binds it to the
// agent, so a reply in the thread comes back to it.
func (s *Service) post(ctx context.Context, agent agents.Agent, channel, text string) (any, error) {
	channel = strings.TrimSpace(channel)
	if channel == "" || strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("teamsapp post: channel and text are required")
	}
	list, err := s.channels(ctx, agent)
	if err != nil {
		return nil, err
	}
	ch, err := pickChannel(list, channel)
	if err != nil {
		return nil, err
	}
	c, err := s.connector()
	if err != nil {
		return nil, err
	}
	res, err := c.CreateChannelThread(ctx, ch.service, ch.ChannelID, teams.SenderActivity(sender(agent), text))
	if err != nil {
		return nil, err
	}
	id := agent.ID
	if err := s.Store.Bind(ctx, Conversation{TenantID: ch.TenantID, ConversationID: res.ID, Kind: "channel",
		TeamID: ch.teamID, ChannelID: ch.ChannelID, AgentID: &id, ServiceURL: ch.service}); err != nil {
		s.Log.Warn("teamsapp: posted thread not recorded", "conversation", res.ID, "err", err)
	}
	return map[string]string{"conversation_id": res.ID, "activity_id": res.ActivityID,
		"channel": ch.Team + "/" + ch.Channel}, nil
}

// pickChannel resolves what the agent named: a channel ID, "Team/Channel", or
// a channel name that is unique across the organisation's teams.
func pickChannel(list []ChannelRef, want string) (ChannelRef, error) {
	for _, ch := range list {
		if ch.ChannelID == want {
			return ch, nil
		}
	}
	team, name, qualified := strings.Cut(want, "/")
	var hits []ChannelRef
	for _, ch := range list {
		if qualified {
			if strings.EqualFold(ch.Team, strings.TrimSpace(team)) && strings.EqualFold(ch.Channel, strings.TrimSpace(name)) {
				hits = append(hits, ch)
			}
		} else if strings.EqualFold(ch.Channel, want) {
			hits = append(hits, ch)
		}
	}
	switch len(hits) {
	case 1:
		return hits[0], nil
	case 0:
		return ChannelRef{}, fmt.Errorf("teamsapp post: no channel %q in this organisation's teams — list_channels shows them", want)
	}
	return ChannelRef{}, fmt.Errorf("teamsapp post: %q names channels in several teams — write Team/Channel or pass the channel_id", want)
}
