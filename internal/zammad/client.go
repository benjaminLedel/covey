// Package zammad bindet das MVP-Zielsystem an (spec/13): REST-Client für die
// Agent-Aktionen und Webhook-Verarbeitung (HMAC-verifiziert, idempotent).
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
)

// Client spricht die Zammad-REST-API mit einem (gebrokerten) API-Token.
// Das Token kommt pro Aufruf aus dem SecretStore — es wird nie persistiert.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: 15 * time.Second},
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

// Reply — POST /ticket_articles. internal=true ist eine interne Notiz,
// internal=false geht sichtbar an den Kunden raus.
func (c *Client) Reply(ctx context.Context, ticketID int, body string, internal bool) (Article, error) {
	var out Article
	err := c.do(ctx, http.MethodPost, "/ticket_articles", map[string]any{
		"ticket_id":    ticketID,
		"body":         body,
		"content_type": "text/plain",
		"type":         "note",
		"internal":     internal,
	}, &out)
	return out, err
}

// SetState — PUT /tickets/{id}. "pending reminder" bildet Coveys blocked ab.
func (c *Client) SetState(ctx context.Context, ticketID int, state string) error {
	body := map[string]any{"state": state}
	if strings.HasPrefix(state, "pending") {
		body["pending_time"] = time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tickets/%d", ticketID), body, nil)
}

// Escalate setzt eine interne Notiz und stellt das Ticket zurück in die Gruppe
// (owner_id 1 = System/unassigned), damit ein Mensch übernimmt.
func (c *Client) Escalate(ctx context.Context, ticketID int, note string) error {
	if _, err := c.Reply(ctx, ticketID, note, true); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/tickets/%d", ticketID),
		map[string]any{"owner_id": 1, "state": "open"}, nil)
}
