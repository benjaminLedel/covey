// Package teams bindet Microsoft Teams als Zielsystem an (spec/15): Webhook-
// Verarbeitung des Bot-Framework-Messaging-Endpoints (JWT-verifiziert,
// idempotent) und ein REST-Client für den Bot Connector (OAuth2
// client_credentials).
package teams

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"covey/internal/reqlog"
	"covey/internal/target"
)

// Client spricht den Bot-Connector-REST-Dienst mit einem kurzlebigen, per
// OAuth2 client_credentials getauschten Access-Token. App-ID und App-Passwort
// kommen pro Aufruf gebrokert aus dem SecretStore — sie werden nie persistiert.
type Client struct {
	tokenEndpoint string
	appID         string
	appPassword   string
	HTTP          *http.Client
	now           func() time.Time

	mu       sync.Mutex
	token    string
	tokenExp time.Time
}

// NewClient baut den Connector-Client aus dem gebrokerten Credential:
// cred.Token = "{appId}:{appPassword}", cred.BaseURL = optionaler
// Token-Endpoint (Default: Multi-Tenant-Bot-Framework).
func NewClient(cred target.Credential) (*Client, error) {
	appID, appPassword, err := parseCredential(cred.Token)
	if err != nil {
		return nil, err
	}
	endpoint := strings.TrimSpace(cred.BaseURL)
	if endpoint == "" {
		endpoint = defaultTokenEndpoint()
	}
	return &Client{
		tokenEndpoint: endpoint,
		appID:         appID,
		appPassword:   appPassword,
		HTTP:          reqlog.Client("teams", 15*time.Second),
		now:           time.Now,
	}, nil
}

// parseCredential zerlegt "{appId}:{appPassword}". Die App-ID (GUID) steht vor
// dem ersten ':'; der Rest ist das Passwort (darf selbst ':' enthalten).
func parseCredential(token string) (appID, appPassword string, err error) {
	appID, appPassword, ok := strings.Cut(strings.TrimSpace(token), ":")
	if !ok || appID == "" || appPassword == "" {
		return "", "", fmt.Errorf("teams_token muss das Format \"appId:appPassword\" haben")
	}
	return appID, appPassword, nil
}

// accessToken liefert ein gültiges Connector-Token; es wird pro Prozess
// gecacht und ~1 Minute vor Ablauf erneuert.
func (c *Client) accessToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && c.now().Before(c.tokenExp) {
		return c.token, nil
	}
	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {c.appID},
		"client_secret": {c.appPassword},
		"scope":         {connectorScope},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("teams token: HTTP %d: %.300s", resp.StatusCode, data)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("teams token: %w", err)
	}
	if out.AccessToken == "" {
		return "", fmt.Errorf("teams token: leere Antwort")
	}
	ttl := out.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	c.token = out.AccessToken
	c.tokenExp = c.now().Add(time.Duration(ttl-60) * time.Second)
	return c.token, nil
}

// post schickt einen JSON-Body an eine absolute Connector-URL mit
// Bearer-Auth und dekodiert die Antwort optional nach out.
func (c *Client) post(ctx context.Context, absURL string, body, out any) error {
	token, err := c.accessToken(ctx)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, absURL, bytes.NewReader(raw))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
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
		return fmt.Errorf("teams POST %s: HTTP %d: %.300s", absURL, resp.StatusCode, data)
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// ResourceResponse ist die Antwort des Connectors auf einen Activity-Post:
// die id der erzeugten Nachricht bzw. Konversation.
type ResourceResponse struct {
	ID string `json:"id"`
}

func messageActivity(text string) map[string]any {
	return map[string]any{"type": "message", "text": text}
}

// SendMessage postet eine neue Nachricht in eine bestehende Konversation.
func (c *Client) SendMessage(ctx context.Context, serviceURL, conversationID, text string) (ResourceResponse, error) {
	var out ResourceResponse
	u := connectorURL(serviceURL, "/v3/conversations/"+url.PathEscape(conversationID)+"/activities")
	err := c.post(ctx, u, messageActivity(text), &out)
	return out, err
}

// Reply antwortet auf eine konkrete Nachricht. Ohne activityID (leer) fällt es
// auf SendMessage zurück.
func (c *Client) Reply(ctx context.Context, serviceURL, conversationID, activityID, text string) (ResourceResponse, error) {
	if strings.TrimSpace(activityID) == "" {
		return c.SendMessage(ctx, serviceURL, conversationID, text)
	}
	var out ResourceResponse
	u := connectorURL(serviceURL, "/v3/conversations/"+url.PathEscape(conversationID)+
		"/activities/"+url.PathEscape(activityID))
	err := c.post(ctx, u, messageActivity(text), &out)
	return out, err
}

// CreateConversation eröffnet einen proaktiven 1:1-Chat mit einem Nutzer und
// sendet die erste Nachricht.
func (c *Client) CreateConversation(ctx context.Context, serviceURL, tenantID, userID, text string) (ResourceResponse, error) {
	var conv ResourceResponse
	body := map[string]any{
		"isGroup": false,
		"bot":     map[string]any{"id": "28:" + c.appID},
		"members": []map[string]any{{"id": userID}},
	}
	if tenantID != "" {
		body["channelData"] = map[string]any{"tenant": map[string]any{"id": tenantID}}
	}
	if err := c.post(ctx, connectorURL(serviceURL, "/v3/conversations"), body, &conv); err != nil {
		return conv, err
	}
	if conv.ID == "" {
		return conv, fmt.Errorf("teams create_conversation: leere conversation id")
	}
	if strings.TrimSpace(text) == "" {
		return conv, nil
	}
	if _, err := c.SendMessage(ctx, serviceURL, conv.ID, text); err != nil {
		return conv, err
	}
	return conv, nil
}

func connectorURL(serviceURL, path string) string {
	return strings.TrimRight(serviceURL, "/") + path
}

// DownloadAttachment holt die Bytes eines Anhangs. Teams liefert zwei Sorten
// URLs: vor-autorisierte content.downloadUrl (SharePoint/OneDrive, ohne Token)
// und Connector-contentUrl (Bearer-Token nötig). Deshalb wird zunächst ohne
// Auth versucht; bei 401/403 einmal mit Connector-Token erneut. Der Body ist
// auf limit+1 Bytes begrenzt, damit der Aufrufer eine Überschreitung erkennt.
func (c *Client) DownloadAttachment(ctx context.Context, downloadURL string, limit int64) (contentType string, body []byte, err error) {
	contentType, body, status, err := c.getBytes(ctx, downloadURL, "", limit)
	if err != nil {
		return "", nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		token, terr := c.accessToken(ctx)
		if terr != nil {
			return "", nil, fmt.Errorf("teams attachment: %d und Token-Abruf scheitert: %w", status, terr)
		}
		contentType, body, status, err = c.getBytes(ctx, downloadURL, token, limit)
		if err != nil {
			return "", nil, err
		}
	}
	if status < 200 || status >= 300 {
		return "", nil, fmt.Errorf("teams attachment: HTTP %d", status)
	}
	return contentType, body, nil
}

func (c *Client) getBytes(ctx context.Context, url, bearer string, limit int64) (contentType string, body []byte, status int, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, 0, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "", nil, resp.StatusCode, nil // Retry-Signal, Body verwerfen
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", nil, resp.StatusCode, err
	}
	return resp.Header.Get("Content-Type"), data, resp.StatusCode, nil
}
