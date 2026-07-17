// Package gitlab bindet GitLab als Zielsystem-Plugin an (analog spec/13 für
// Zammad): REST-Client (API v4) für die Agent-Aktionen und Webhook-Verarbeitung
// (Token-verifiziert, idempotent). Einheit der Arbeit ist das Issue.
package gitlab

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

// Client spricht die GitLab-REST-API (v4) mit einem (gebrokerten) API-Token.
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
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/api/v4"+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.Token)
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
		return fmt.Errorf("gitlab %s %s: HTTP %d: %.300s", method, path, resp.StatusCode, data)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

type Issue struct {
	ID          int      `json:"id"`
	IID         int      `json:"iid"`
	ProjectID   int      `json:"project_id"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	State       string   `json:"state"`
	Labels      []string `json:"labels"`
	WebURL      string   `json:"web_url"`
}

type Note struct {
	ID       int    `json:"id"`
	Body     string `json:"body"`
	Internal bool   `json:"internal"`
	System   bool   `json:"system"`
	Author   struct {
		Username string `json:"username"`
	} `json:"author"`
	CreatedAt string `json:"created_at"`
}

// GetIssue — GET /projects/{id}/issues/{iid}
func (c *Client) GetIssue(ctx context.Context, projectID, issueIID int) (Issue, error) {
	var i Issue
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID), nil, &i)
	return i, err
}

// ListNotes — GET /projects/{id}/issues/{iid}/notes (chronologisch)
func (c *Client) ListNotes(ctx context.Context, projectID, issueIID int) ([]Note, error) {
	var out []Note
	err := c.do(ctx, http.MethodGet,
		fmt.Sprintf("/projects/%d/issues/%d/notes?sort=asc&order_by=created_at", projectID, issueIID), nil, &out)
	return out, err
}

// Comment — POST /projects/{id}/issues/{iid}/notes. internal=true ist eine
// interne Notiz (nur für Projektmitglieder ab Reporter sichtbar), internal=false
// ein öffentlicher Kommentar — auch für externe Reporter sichtbar.
func (c *Client) Comment(ctx context.Context, projectID, issueIID int, body string, internal bool) (Note, error) {
	var out Note
	err := c.do(ctx, http.MethodPost, fmt.Sprintf("/projects/%d/issues/%d/notes", projectID, issueIID),
		map[string]any{"body": body, "internal": internal}, &out)
	return out, err
}

// SetState — PUT /projects/{id}/issues/{iid} mit state_event ("close"|"reopen").
func (c *Client) SetState(ctx context.Context, projectID, issueIID int, stateEvent string) error {
	if stateEvent != "close" && stateEvent != "reopen" {
		return fmt.Errorf("ungültiger state %q (erlaubt: close, reopen)", stateEvent)
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"state_event": stateEvent}, nil)
}

// Escalate setzt eine interne Notiz und entfernt die Zuweisung
// (assignee_ids leer), damit ein Mensch das Issue übernimmt.
func (c *Client) Escalate(ctx context.Context, projectID, issueIID int, note string) error {
	if _, err := c.Comment(ctx, projectID, issueIID, note, true); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPut, fmt.Sprintf("/projects/%d/issues/%d", projectID, issueIID),
		map[string]any{"assignee_ids": []int{}}, nil)
}
