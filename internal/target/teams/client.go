// Package teams binds Microsoft Teams as a target system (spec/15): webhook
// processing of the Bot Framework messaging endpoint (JWT-verified,
// idempotent) and a REST client for the Bot Connector (OAuth2
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

// Client talks to the Bot Connector REST service with a short-lived access
// token exchanged via OAuth2 client_credentials. App ID and app password are
// brokered from the SecretStore per call — they are never persisted.
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

// NewClient builds the connector client from the brokered credential:
// cred.Token = "{appId}:{appPassword}", cred.BaseURL = optional token endpoint
// (default: the multi-tenant Bot Framework one).
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

// parseCredential splits "{appId}:{appPassword}". The app ID (a GUID) comes
// before the first ':'; the rest is the password (which may contain ':' itself).
func parseCredential(token string) (appID, appPassword string, err error) {
	appID, appPassword, ok := strings.Cut(strings.TrimSpace(token), ":")
	if !ok || appID == "" || appPassword == "" {
		return "", "", fmt.Errorf("teams_token must have the format \"appId:appPassword\"")
	}
	return appID, appPassword, nil
}

// accessToken returns a valid connector token; it is cached per process and
// renewed ~1 minute before it expires.
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
		return "", fmt.Errorf("teams token: empty response")
	}
	ttl := out.ExpiresIn
	if ttl <= 0 {
		ttl = 3600
	}
	c.token = out.AccessToken
	c.tokenExp = c.now().Add(time.Duration(ttl-60) * time.Second)
	return c.token, nil
}

// post sends a JSON body to an absolute connector URL with bearer auth and
// optionally decodes the response into out.
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

// ResourceResponse is the connector's answer to an activity post: the id of
// the created message or conversation.
type ResourceResponse struct {
	ID string `json:"id"`
}

func messageActivity(text string) map[string]any {
	return map[string]any{"type": "message", "text": text}
}

// SendMessage posts a new message into an existing conversation.
func (c *Client) SendMessage(ctx context.Context, serviceURL, conversationID, text string) (ResourceResponse, error) {
	var out ResourceResponse
	u := connectorURL(serviceURL, "/v3/conversations/"+url.PathEscape(conversationID)+"/activities")
	err := c.post(ctx, u, messageActivity(text), &out)
	return out, err
}

// Reply answers a specific message. Without an activityID (empty) it falls
// back to SendMessage.
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

// SendFileConsent posts the consent card for an outgoing file (spec/15,
// "sending files"). Teams renders it as a card with "accept" / "decline"; only
// the recipient's click produces the upload URL, which arrives back in an
// invoke activity. consentKey travels unchanged through both contexts and maps
// the answer back to the file it was meant for.
func (c *Client) SendFileConsent(ctx context.Context, serviceURL, conversationID, filename, description string, sizeBytes int64, consentKey string) (ResourceResponse, error) {
	var out ResourceResponse
	activity := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": consentCardContentType,
			"name":        filename,
			"content": map[string]any{
				"description":    description,
				"sizeInBytes":    sizeBytes,
				"acceptContext":  map[string]any{"key": consentKey},
				"declineContext": map[string]any{"key": consentKey},
			},
		}},
	}
	u := connectorURL(serviceURL, "/v3/conversations/"+url.PathEscape(conversationID)+"/activities")
	err := c.post(ctx, u, activity, &out)
	return out, err
}

// UploadFile pushes the bytes to the upload URL that Teams delivered after the
// consent. That is a SharePoint/OneDrive upload session: PUT with
// Content-Range, without a connector token — the URL carries its authorization
// itself. Hence deliberately no bearer header here (it would be rejected).
func (c *Client) UploadFile(ctx context.Context, uploadURL string, data []byte) error {
	if strings.TrimSpace(uploadURL) == "" {
		return fmt.Errorf("teams upload: upload_url missing")
	}
	if len(data) == 0 {
		return fmt.Errorf("teams upload: file is empty")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Length", fmt.Sprintf("%d", len(data)))
	req.Header.Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(data)-1, len(data)))
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("teams upload: HTTP %d: %.300s", resp.StatusCode, body)
	}
	return nil
}

// SendFileInfo posts the completion card that shows the recipient the
// finished upload in the chat. Without it, all that remains after the upload
// is the ticked-off consent card.
func (c *Client) SendFileInfo(ctx context.Context, serviceURL, conversationID, filename, contentURL, uniqueID, fileType string) (ResourceResponse, error) {
	var out ResourceResponse
	activity := map[string]any{
		"type": "message",
		"attachments": []map[string]any{{
			"contentType": infoCardContentType,
			"contentUrl":  contentURL,
			"name":        filename,
			"content": map[string]any{
				"uniqueId": uniqueID,
				"fileType": fileType,
			},
		}},
	}
	u := connectorURL(serviceURL, "/v3/conversations/"+url.PathEscape(conversationID)+"/activities")
	err := c.post(ctx, u, activity, &out)
	return out, err
}

// CreateConversation opens a proactive 1:1 chat with a user and sends the
// first message.
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
		return conv, fmt.Errorf("teams create_conversation: empty conversation id")
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

// DownloadAttachment fetches the bytes of an attachment. Teams delivers two
// kinds of URLs: pre-authorized content.downloadUrl (SharePoint/OneDrive, no
// token) and connector contentUrl (bearer token required). Therefore it first
// tries without auth; on 401/403 once more with the connector token. The body
// is capped at limit+1 bytes so the caller can detect an overrun.
func (c *Client) DownloadAttachment(ctx context.Context, downloadURL string, limit int64) (contentType string, body []byte, err error) {
	contentType, body, status, err := c.getBytes(ctx, downloadURL, "", limit)
	if err != nil {
		return "", nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		token, terr := c.accessToken(ctx)
		if terr != nil {
			return "", nil, fmt.Errorf("teams attachment: %d and fetching the token fails: %w", status, terr)
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
		return "", nil, resp.StatusCode, nil // retry signal, discard the body
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return "", nil, resp.StatusCode, err
	}
	return resp.Header.Get("Content-Type"), data, resp.StatusCode, nil
}
