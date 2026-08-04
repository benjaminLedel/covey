// Package zammad binds the MVP target system (spec/13): a REST client for the
// agent actions and webhook processing (HMAC-verified, idempotent).
package zammad

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// Client talks to the Zammad REST API with a (brokered) API token. The token
// comes from the SecretStore per call — it is never persisted.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    reqlog.Client("zammad", 15*time.Second),
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v1"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Token token="+c.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("zammad %s %s: HTTP %d: %.300s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type Ticket struct {
	ID         int    `json:"id"`
	Number     string `json:"number"`
	Title      string `json:"title"`
	State      string `json:"state"`
	StateID    int    `json:"state_id"`
	Group      string `json:"group"`
	Priority   string `json:"priority"`
	CustomerID int    `json:"customer_id"`
	OwnerID    int    `json:"owner_id"`
}

type Article struct {
	ID       int    `json:"id"`
	TicketID int    `json:"ticket_id"`
	From     string `json:"from"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
	Sender   string `json:"sender"`
	Type     string `json:"type"`
}

// GetTicket — GET /tickets/{id}
func (c *Client) GetTicket(ctx context.Context, id int) (Ticket, error) {
	var t Ticket
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/tickets/%d?expand=true", id), nil, &t)
	return t, err
}

// ListArticles — GET /ticket_articles/by_ticket/{id}
func (c *Client) ListArticles(ctx context.Context, ticketID int) ([]Article, error) {
	var out []Article
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/ticket_articles/by_ticket/%d", ticketID), nil, &out)
	return out, err
}

// Reply — POST /ticket_articles. internal=true is an internal note (type
// "note", visible only to agents). internal=false goes out customer-visible —
// as type "email" (default), so that the answer actually reaches the customer;
// overridable via COVEY_ZAMMAD_REPLY_TYPE for web/chat instances. (An external
// "note" would be visible in the ticket but would trigger no mail.)
func (c *Client) Reply(ctx context.Context, ticketID int, body string, internal bool) (Article, error) {
	articleType := "note"
	if !internal {
		articleType = externalReplyType()
	}
	var out Article
	err := c.do(ctx, http.MethodPost, "/ticket_articles", map[string]any{
		"ticket_id":    ticketID,
		"body":         body,
		"content_type": "text/plain",
		"type":         articleType,
		"internal":     internal,
	}, &out)
	return out, err
}

// SetState — PUT /tickets/{id}. "pending reminder" maps Covey's blocked.
func (c *Client) SetState(ctx context.Context, ticketID int, state string) error {
	body := map[string]any{"state": state}
	if strings.HasPrefix(state, "pending") {
		body["pending_time"] = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tickets/%d", ticketID), body, nil)
}

// Escalate adds an internal note and puts the ticket back into the group
// (owner_id 1 = system/unassigned) so that a human takes over.
func (c *Client) Escalate(ctx context.Context, ticketID int, note string) error {
	if _, err := c.Reply(ctx, ticketID, note, true); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tickets/%d", ticketID),
		map[string]any{"owner_id": 1, "state": "open"}, nil)
}
