// Package githubapp speaks to GitHub as a GitHub App: it signs the App's JWT,
// exchanges it for installation tokens (and keeps them until shortly before
// they expire), converts a manifest code into a freshly created App, and reads
// what an installation is.
//
// Two kinds of App use it. The platform's shared App is configured through the
// environment (COVEY_GITHUB_APP_*) and serves every organisation that installs
// it. An organisation's own App is created through the manifest flow; its
// credentials live in the organisation's secrets. Both end in the same place:
// an installation id, from which the control plane mints the token it brokers
// to the github plugin. The private key never leaves the control plane.
package githubapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DefaultAPI is github.com's REST API.
const DefaultAPI = "https://api.github.com"

// App is one GitHub App's credentials.
type App struct {
	ID         int64
	Slug       string // e.g. "covey" — the bot is "covey[bot]"
	PrivateKey *rsa.PrivateKey
	API        string // empty = DefaultAPI
}

// ParseKey reads a PEM private key as GitHub hands it out (PKCS#1). Escaped
// newlines ("\n" as two characters) are accepted, because an environment
// variable often carries the key on one line.
func ParseKey(pem string) (*rsa.PrivateKey, error) {
	pem = strings.TrimSpace(strings.ReplaceAll(pem, `\n`, "\n"))
	if pem == "" {
		return nil, errors.New("private key empty")
	}
	k, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(pem))
	if err != nil {
		return nil, fmt.Errorf("private key unreadable: %w", err)
	}
	return k, nil
}

// BotLogin is the login GitHub gives the App's bot user.
func (a App) BotLogin() string {
	if a.Slug == "" {
		return ""
	}
	return a.Slug + "[bot]"
}

func (a App) api() string {
	if a.API != "" {
		return strings.TrimRight(a.API, "/")
	}
	return DefaultAPI
}

// JWT signs the App's own token: valid for nine minutes (GitHub allows ten),
// issued a minute in the past to absorb clock drift.
func (a App) JWT(now time.Time) (string, error) {
	if a.ID == 0 || a.PrivateKey == nil {
		return "", errors.New("GitHub App not configured (id or key missing)")
	}
	t := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.RegisteredClaims{
		Issuer:    strconv.FormatInt(a.ID, 10),
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(9 * time.Minute)),
	})
	return t.SignedString(a.PrivateKey)
}

// Token is an installation access token.
type Token struct {
	Value     string    `json:"token"`
	ExpiresAt time.Time `json:"expires_at"`
}

// Installation is what GitHub says about one installation of an App.
type Installation struct {
	ID      int64 `json:"id"`
	Account struct {
		Login string `json:"login"`
		Type  string `json:"type"` // "Organization" | "User"
	} `json:"account"`
	RepositorySelection string     `json:"repository_selection"` // "all" | "selected"
	AppSlug             string     `json:"app_slug"`
	SuspendedAt         *time.Time `json:"suspended_at"`
}

// Client talks to GitHub on an App's behalf and caches installation tokens.
// Safe for concurrent use; one Client per process is enough.
type Client struct {
	HTTP *http.Client
	Now  func() time.Time

	mu     sync.Mutex
	tokens map[string]Token // "<api>|<app id>|<installation id>"
}

// NewClient returns a client with sane timeouts.
func NewClient() *Client {
	return &Client{HTTP: &http.Client{Timeout: 15 * time.Second}, Now: time.Now, tokens: map[string]Token{}}
}

// refreshBefore: a cached token is replaced this long before it expires, so an
// action that starts with it does not run into the expiry halfway.
const refreshBefore = 10 * time.Minute

// InstallationToken returns a valid token for the installation, minting a new
// one only when the cached one is close to expiry.
func (c *Client) InstallationToken(ctx context.Context, app App, installationID int64) (Token, error) {
	if installationID == 0 {
		return Token{}, errors.New("no installation id")
	}
	key := fmt.Sprintf("%s|%d|%d", app.api(), app.ID, installationID)
	now := c.Now()
	c.mu.Lock()
	if t, ok := c.tokens[key]; ok && t.ExpiresAt.Sub(now) > refreshBefore {
		c.mu.Unlock()
		return t, nil
	}
	c.mu.Unlock()

	var t Token
	if err := c.appCall(ctx, app, http.MethodPost, fmt.Sprintf("/app/installations/%d/access_tokens", installationID), nil, &t); err != nil {
		return Token{}, err
	}
	if t.Value == "" {
		return Token{}, errors.New("GitHub returned no installation token")
	}
	c.mu.Lock()
	c.tokens[key] = t
	c.mu.Unlock()
	return t, nil
}

// Forget drops a cached token — after an uninstall, or when GitHub refused it.
func (c *Client) Forget(app App, installationID int64) {
	c.mu.Lock()
	delete(c.tokens, fmt.Sprintf("%s|%d|%d", app.api(), app.ID, installationID))
	c.mu.Unlock()
}

// GetInstallation reads one installation of the App. It is how the control
// plane checks that an installation id handed back by a browser redirect
// really belongs to this App before it binds it to an organisation.
func (c *Client) GetInstallation(ctx context.Context, app App, installationID int64) (Installation, error) {
	var in Installation
	err := c.appCall(ctx, app, http.MethodGet, fmt.Sprintf("/app/installations/%d", installationID), nil, &in)
	return in, err
}

// Created is what the manifest conversion returns: a new App with everything
// needed to act as it.
type Created struct {
	ID            int64  `json:"id"`
	Slug          string `json:"slug"`
	Name          string `json:"name"`
	HTMLURL       string `json:"html_url"`
	PEM           string `json:"pem"`
	WebhookSecret string `json:"webhook_secret"`
	ClientID      string `json:"client_id"`
	ClientSecret  string `json:"client_secret"`
	Owner         struct {
		Login string `json:"login"`
	} `json:"owner"`
}

// ConvertManifest exchanges the one-time code GitHub hands back after "Create
// GitHub App" for the App's credentials (POST /app-manifests/{code}/conversions,
// no authentication — the code is the proof, valid for one hour, once).
func (c *Client) ConvertManifest(ctx context.Context, api, code string) (Created, error) {
	if api == "" {
		api = DefaultAPI
	}
	var out Created
	if code == "" {
		return Created{}, errors.New("GitHub sent no code")
	}
	err := c.call(ctx, http.MethodPost, strings.TrimRight(api, "/")+"/app-manifests/"+url.PathEscape(code)+"/conversions", "", nil, &out)
	if err == nil && (out.ID == 0 || out.PEM == "") {
		err = errors.New("GitHub returned an incomplete App")
	}
	return out, err
}

func (c *Client) appCall(ctx context.Context, app App, method, path string, body, out any) error {
	tok, err := app.JWT(c.Now())
	if err != nil {
		return err
	}
	return c.call(ctx, method, app.api()+path, "Bearer "+tok, body, out)
}

// APIError carries GitHub's status and message.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("GitHub %d: %s", e.Status, e.Message)
}

func decodeJSON(res *http.Response, out any) error {
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		return &APIError{Status: res.StatusCode, Message: http.StatusText(res.StatusCode)}
	}
	return json.Unmarshal(raw, out)
}

func (c *Client) call(ctx context.Context, method, url, auth string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(raw, &m)
		if m.Message == "" {
			m.Message = http.StatusText(res.StatusCode)
		}
		return &APIError{Status: res.StatusCode, Message: m.Message}
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}
