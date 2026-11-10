package teamsuser

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
)

// Execute runs an agent's teamsuser action with the token of the person who
// delegated to this agent — and only this agent's. ACCESS, scope and guard
// rails were checked by the caller. Every ID the agent passes is a Graph
// path segment under the fixed Graph host; what it reaches is decided by the
// person's own permissions in Teams.
func (s *Service) Execute(ctx context.Context, agent agents.Agent, taskID uuid.UUID, action string, params json.RawMessage) (any, error) {
	if s == nil {
		return nil, errNoApp
	}
	var in struct {
		ChatID    string `json:"chat_id"`
		TeamID    string `json:"team_id"`
		ChannelID string `json:"channel_id"`
		ReplyTo   string `json:"reply_to"`
		Text      string `json:"text"`
		Limit     int    `json:"limit"`
	}
	if len(params) > 0 {
		if err := json.Unmarshal(params, &in); err != nil {
			return nil, fmt.Errorf("teamsuser %s: %w", action, err)
		}
	}
	if in.Limit <= 0 || in.Limit > 50 {
		in.Limit = 20
	}
	tok, err := s.tokenFor(ctx, agent.OrgID, agent.ID, taskID)
	if err != nil {
		return nil, err
	}
	g := graphCall{s: s, token: tok}
	esc := url.PathEscape
	switch action {
	case "list_chats":
		var out struct {
			Value []struct {
				ID       string `json:"id"`
				Topic    string `json:"topic"`
				ChatType string `json:"chatType"`
				Members  []struct {
					DisplayName string `json:"displayName"`
				} `json:"members"`
			} `json:"value"`
		}
		if err := g.get(ctx, "/me/chats?$expand=members&$top=50", &out); err != nil {
			return nil, err
		}
		list := []map[string]any{}
		for _, c := range out.Value {
			names := []string{}
			for _, m := range c.Members {
				names = append(names, m.DisplayName)
			}
			list = append(list, map[string]any{"chat_id": c.ID, "topic": c.Topic, "type": c.ChatType, "members": names})
		}
		return list, nil
	case "read_chat":
		if in.ChatID == "" {
			return nil, fmt.Errorf("teamsuser read_chat: chat_id is required")
		}
		return g.messages(ctx, fmt.Sprintf("/chats/%s/messages?$top=%d", esc(in.ChatID), in.Limit))
	case "send_chat":
		if in.ChatID == "" || strings.TrimSpace(in.Text) == "" {
			return nil, fmt.Errorf("teamsuser send_chat: chat_id and text are required")
		}
		return g.send(ctx, "/chats/"+esc(in.ChatID)+"/messages", body(agent, in.Text))
	case "list_channels":
		var teamsOut struct {
			Value []struct {
				ID          string `json:"id"`
				DisplayName string `json:"displayName"`
			} `json:"value"`
		}
		if err := g.get(ctx, "/me/joinedTeams", &teamsOut); err != nil {
			return nil, err
		}
		list := []map[string]string{}
		for _, t := range teamsOut.Value {
			var ch struct {
				Value []struct {
					ID          string `json:"id"`
					DisplayName string `json:"displayName"`
				} `json:"value"`
			}
			if err := g.get(ctx, "/teams/"+esc(t.ID)+"/channels", &ch); err != nil {
				s.Log.Warn("teamsuser: channels not listed", "team", t.ID, "err", err)
				continue
			}
			for _, c := range ch.Value {
				list = append(list, map[string]string{"team": t.DisplayName, "team_id": t.ID, "channel": c.DisplayName, "channel_id": c.ID})
			}
		}
		return list, nil
	case "read_channel":
		if in.TeamID == "" || in.ChannelID == "" {
			return nil, fmt.Errorf("teamsuser read_channel: team_id and channel_id are required")
		}
		return g.messages(ctx, fmt.Sprintf("/teams/%s/channels/%s/messages?$top=%d", esc(in.TeamID), esc(in.ChannelID), in.Limit))
	case "post_channel":
		if in.TeamID == "" || in.ChannelID == "" || strings.TrimSpace(in.Text) == "" {
			return nil, fmt.Errorf("teamsuser post_channel: team_id, channel_id and text are required")
		}
		path := "/teams/" + esc(in.TeamID) + "/channels/" + esc(in.ChannelID) + "/messages"
		if in.ReplyTo != "" {
			path += "/" + esc(in.ReplyTo) + "/replies"
		}
		return g.send(ctx, path, body(agent, in.Text))
	}
	return nil, fmt.Errorf("teamsuser: unknown action %q", action)
}

// body is the message in the person's name, with the line that says who
// wrote it. Without that line nobody in Teams could tell the person from
// their assistant.
func body(agent agents.Agent, text string) map[string]any {
	name := agent.DisplayName
	if name == "" {
		name = agent.Slug
	}
	content := strings.ReplaceAll(html.EscapeString(strings.TrimSpace(text)), "\n", "<br>") +
		"<br><br><em>— sent by " + html.EscapeString(name) + " (covey)</em>"
	return map[string]any{"body": map[string]any{"contentType": "html", "content": content}}
}

type graphCall struct {
	s     *Service
	token string
}

func (g graphCall) do(ctx context.Context, method, path string, in, out any) error {
	var rd io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, g.s.graph()+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+g.token)
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		return fmt.Errorf("teams (as the person): HTTP %d: %.300s", resp.StatusCode, e.Error.Message)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (g graphCall) get(ctx context.Context, path string, out any) error {
	return g.do(ctx, http.MethodGet, path, nil, out)
}

func (g graphCall) send(ctx context.Context, path string, msg map[string]any) (any, error) {
	var out struct {
		ID string `json:"id"`
	}
	if err := g.do(ctx, http.MethodPost, path, msg, &out); err != nil {
		return nil, err
	}
	return map[string]string{"message_id": out.ID}, nil
}

var tags = regexp.MustCompile(`(?s)<[^>]*>`)

// messages reads a message list as an agent needs it: who, when, the text
// without markup. System messages (someone joined) are left out.
func (g graphCall) messages(ctx context.Context, path string) (any, error) {
	var out struct {
		Value []struct {
			ID              string `json:"id"`
			MessageType     string `json:"messageType"`
			CreatedDateTime string `json:"createdDateTime"`
			From            *struct {
				User *struct {
					DisplayName string `json:"displayName"`
				} `json:"user"`
			} `json:"from"`
			Body struct {
				Content string `json:"content"`
			} `json:"body"`
		} `json:"value"`
	}
	if err := g.get(ctx, path, &out); err != nil {
		return nil, err
	}
	list := []map[string]string{}
	for _, m := range out.Value {
		if m.MessageType != "" && m.MessageType != "message" {
			continue
		}
		from := ""
		if m.From != nil && m.From.User != nil {
			from = m.From.User.DisplayName
		}
		text := strings.TrimSpace(html.UnescapeString(tags.ReplaceAllString(m.Body.Content, " ")))
		list = append(list, map[string]string{"id": m.ID, "from": from, "at": m.CreatedDateTime, "text": text})
	}
	return list, nil
}
