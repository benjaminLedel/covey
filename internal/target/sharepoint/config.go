package sharepoint

import (
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
)

// Configuration of the SharePoint plugin out of the brokered secret pair. The
// broker knows exactly two secrets per system (sharepoint_url +
// sharepoint_token), so sharepoint_url encodes the share link plus optional
// endpoint overrides (separated by spaces):
//
//	sharepoint_url   = https://contoso.sharepoint.com/:f:/s/TeamX/AbCdEf…
//	                   [graph=https://graph.microsoft.com]
//	                   [login=https://login.microsoftonline.com]
//	sharepoint_token = tenant-id:client-id:client-secret
//
// The share link is any SharePoint/Teams link onto a folder or a document
// library ("Copy link" in SharePoint resp. in the files tab of a Teams
// channel) — the Graph API resolves it to the document library through the
// /shares endpoint. graph=/login= override the Microsoft endpoints (national
// clouds, tests); the default is the public cloud. sharepoint_token carries
// the client-credentials triple of an Entra ID app registration; a value
// WITHOUT the two colons is used as a ready-made bearer token (tests/demos).

// Config is the parsed connection configuration.
type Config struct {
	ShareLink string // share link onto a folder/library
	GraphBase string // Graph endpoint, without /v1.0
	LoginBase string // base of the Entra ID token endpoint

	// The client-credentials triple — empty when StaticToken is set.
	Tenant       string
	ClientID     string
	ClientSecret string

	// StaticToken is a ready-made bearer token (tests/demos) — then the OAuth
	// flow falls away entirely.
	StaticToken string
}

// ParseConfig breaks the brokered credential down into the Graph configuration.
func ParseConfig(baseURL, token string) (Config, error) {
	cfg := Config{
		GraphBase: "https://graph.microsoft.com",
		LoginBase: "https://login.microsoftonline.com",
	}
	for _, part := range strings.Fields(baseURL) {
		switch {
		case strings.HasPrefix(part, "graph="):
			cfg.GraphBase = strings.TrimRight(strings.TrimPrefix(part, "graph="), "/")
		case strings.HasPrefix(part, "login="):
			cfg.LoginBase = strings.TrimRight(strings.TrimPrefix(part, "login="), "/")
		case cfg.ShareLink == "":
			cfg.ShareLink = part
		default:
			return Config{}, fmt.Errorf("sharepoint_url: unexpected component %q (expected: share link [graph=…] [login=…])", part)
		}
	}
	if cfg.ShareLink == "" {
		return Config{}, fmt.Errorf("sharepoint_url: share link missing — store the SharePoint/Teams link of the folder")
	}
	if u, err := url.Parse(cfg.ShareLink); err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return Config{}, fmt.Errorf("sharepoint_url: %q is not a valid link", cfg.ShareLink)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Config{}, fmt.Errorf("sharepoint_token missing")
	}
	if parts := strings.SplitN(token, ":", 3); len(parts) == 3 {
		if parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return Config{}, fmt.Errorf("sharepoint_token must be %q", "tenant-id:client-id:client-secret")
		}
		cfg.Tenant, cfg.ClientID, cfg.ClientSecret = parts[0], parts[1], parts[2]
	} else {
		cfg.StaticToken = token
	}
	return cfg, nil
}

// Token cache for the client-credentials flow: Entra ID tokens live ~1 h —
// fetching them afresh per action would be a needless roundtrip and would run
// into throttling at Microsoft. The cache lives only in the daemon's RAM (like
// the brokered credentials themselves) and expires with a safety margin.
var (
	tokenMu    sync.Mutex
	tokenCache = map[string]cachedToken{}
)

type cachedToken struct {
	token   string
	expires time.Time
}

// AccessToken returns a valid bearer token: the static one, the cached one or
// one freshly fetched through the client-credentials flow.
func (cfg Config) AccessToken(ctx context.Context) (string, error) {
	if cfg.StaticToken != "" {
		return cfg.StaticToken, nil
	}
	key := cfg.LoginBase + "|" + cfg.Tenant + "|" + cfg.ClientID

	tokenMu.Lock()
	cached, ok := tokenCache[key]
	tokenMu.Unlock()
	if ok && time.Now().Before(cached.expires) {
		return cached.token, nil
	}

	form := url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"scope":         {cfg.GraphBase + "/.default"},
	}
	endpoint := cfg.LoginBase + "/" + url.PathEscape(cfg.Tenant) + "/oauth2/v2.0/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := reqlog.Client("sharepoint", 15*time.Second).Do(req)
	if err != nil {
		return "", fmt.Errorf("entra-id token: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("entra-id token: HTTP %d: %.300s", resp.StatusCode, data)
	}
	var out struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.AccessToken == "" {
		return "", fmt.Errorf("entra-id token: unexpected response")
	}
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl > 2*time.Minute {
		ttl -= 2 * time.Minute // safety margin ahead of the real expiry
	}
	tokenMu.Lock()
	tokenCache[key] = cachedToken{token: out.AccessToken, expires: time.Now().Add(ttl)}
	tokenMu.Unlock()
	return out.AccessToken, nil
}
