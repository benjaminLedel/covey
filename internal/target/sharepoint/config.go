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

// Konfiguration des SharePoint-Plugins aus dem gebrokerten Secret-Paar. Der
// Broker kennt pro System genau zwei Secrets (sharepoint_url +
// sharepoint_token), deshalb kodiert sharepoint_url den Freigabelink plus
// optionale Endpunkt-Overrides (durch Leerzeichen getrennt):
//
//	sharepoint_url   = https://contoso.sharepoint.com/:f:/s/TeamX/AbCdEf…
//	                   [graph=https://graph.microsoft.com]
//	                   [login=https://login.microsoftonline.com]
//	sharepoint_token = tenant-id:client-id:client-secret
//
// Der Freigabelink ist ein beliebiger SharePoint-/Teams-Link auf einen
// Ordner oder eine Dokumentbibliothek („Link kopieren" in SharePoint bzw.
// im Dateien-Tab eines Teams-Kanals) — die Graph-API löst ihn über den
// /shares-Endpoint zur Dokumentbibliothek auf. graph=/login= überschreiben
// die Microsoft-Endpunkte (nationale Clouds, Tests); Default ist die
// öffentliche Cloud. sharepoint_token trägt das Client-Credentials-Tripel
// einer Entra-ID-App-Registrierung; ein Wert OHNE die zwei Doppelpunkte
// wird als fertiger Bearer-Token verwendet (Tests/Demos).

// Config ist die geparste Verbindungs-Konfiguration.
type Config struct {
	ShareLink string // Freigabelink auf Ordner/Bibliothek
	GraphBase string // Graph-Endpoint, ohne /v1.0
	LoginBase string // Entra-ID-Token-Endpoint-Basis

	// Client-Credentials-Tripel — leer, wenn StaticToken gesetzt ist.
	Tenant       string
	ClientID     string
	ClientSecret string

	// StaticToken ist ein fertiger Bearer-Token (Tests/Demos) — dann
	// entfällt der OAuth-Flow komplett.
	StaticToken string
}

// ParseConfig zerlegt das gebrokerte Credential in die Graph-Konfiguration.
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
			return Config{}, fmt.Errorf("sharepoint_url: unerwarteter Bestandteil %q (erwartet: Freigabelink [graph=…] [login=…])", part)
		}
	}
	if cfg.ShareLink == "" {
		return Config{}, fmt.Errorf("sharepoint_url: Freigabelink fehlt — den SharePoint-/Teams-Link des Ordners hinterlegen")
	}
	if u, err := url.Parse(cfg.ShareLink); err != nil || u.Scheme != "https" && u.Scheme != "http" || u.Host == "" {
		return Config{}, fmt.Errorf("sharepoint_url: %q ist kein gültiger Link", cfg.ShareLink)
	}

	token = strings.TrimSpace(token)
	if token == "" {
		return Config{}, fmt.Errorf("sharepoint_token fehlt")
	}
	if parts := strings.SplitN(token, ":", 3); len(parts) == 3 {
		if parts[0] == "" || parts[1] == "" || parts[2] == "" {
			return Config{}, fmt.Errorf("sharepoint_token muss %q sein", "tenant-id:client-id:client-secret")
		}
		cfg.Tenant, cfg.ClientID, cfg.ClientSecret = parts[0], parts[1], parts[2]
	} else {
		cfg.StaticToken = token
	}
	return cfg, nil
}

// Token-Cache für den Client-Credentials-Flow: Entra-ID-Tokens leben ~1 h —
// sie pro Aktion neu zu holen wäre ein unnötiger Roundtrip und fiele bei
// Microsoft unter Throttling. Der Cache lebt nur im Daemon-RAM (wie die
// gebrokerten Credentials selbst) und verfällt mit Sicherheitsmarge.
var (
	tokenMu    sync.Mutex
	tokenCache = map[string]cachedToken{}
)

type cachedToken struct {
	token   string
	expires time.Time
}

// AccessToken liefert einen gültigen Bearer-Token: den statischen, den
// gecachten oder einen frisch über den Client-Credentials-Flow geholten.
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
		return "", fmt.Errorf("entra-id token: unerwartete Antwort")
	}
	ttl := time.Duration(out.ExpiresIn) * time.Second
	if ttl > 2*time.Minute {
		ttl -= 2 * time.Minute // Sicherheitsmarge vor dem echten Verfall
	}
	tokenMu.Lock()
	tokenCache[key] = cachedToken{token: out.AccessToken, expires: time.Now().Add(ttl)}
	tokenMu.Unlock()
	return out.AccessToken, nil
}
