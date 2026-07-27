package teams

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Activity ist der relevante Ausschnitt einer Bot-Framework-Aktivität, wie der
// Azure Bot Service sie an den Messaging-Endpoint zustellt (spec/15).
type Activity struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Text         string              `json:"text"`
	ServiceURL   string              `json:"serviceUrl"`
	ChannelID    string              `json:"channelId"`
	From         ChannelAccount      `json:"from"`
	Recipient    ChannelAccount      `json:"recipient"`
	Conversation ConversationAccount `json:"conversation"`
}

// ChannelAccount identifiziert Absender bzw. Empfänger einer Activity.
type ChannelAccount struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AADObjectID string `json:"aadObjectId"`
}

// ConversationAccount identifiziert die Konversation (1:1, Gruppe, Kanal).
type ConversationAccount struct {
	ID               string `json:"id"`
	ConversationType string `json:"conversationType"`
	TenantID         string `json:"tenantId"`
}

// ParseWebhook liest die rohe Activity. Eine Activity ohne Typ ist kein
// gültiger Bot-Framework-Payload und wird fail-closed abgelehnt.
func ParseWebhook(body []byte) (Activity, error) {
	var a Activity
	if err := json.Unmarshal(body, &a); err != nil {
		return a, fmt.Errorf("teams activity: %w", err)
	}
	if a.Type == "" {
		return a, fmt.Errorf("teams activity: type fehlt")
	}
	return a, nil
}

// CorrelationKey ist der stabile, natürliche Korrelations-Key für Teams: die
// Konversations-id, die in jeder Activity mitkommt (spec/15).
func CorrelationKey(conversationID string) string {
	return "teams:conversation:" + conversationID
}

// DedupKey macht die Webhook-Verarbeitung idempotent — der Bot Service
// wiederholt Zustellungen; dieselbe Activity darf nur einen Wake auslösen.
// Fällt die Activity-id (selten) aus, wird auf Konversation + Text
// zurückgegriffen, damit der Key nicht kollabiert.
func (a Activity) DedupKey() string {
	if a.ID != "" {
		return "teams:activity:" + a.ID
	}
	return fmt.Sprintf("teams:conv:%s:%x", a.Conversation.ID, hash(a.Text))
}

func hash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

var mentionTag = regexp.MustCompile(`(?s)<at\b[^>]*>.*?</at>`)

// CleanText entfernt die <at>…</at>-Mention-Tags, die Teams um den Bot-Namen
// legt, und trimmt das Ergebnis — sodass der Agent nur die eigentliche
// Nachricht sieht, nicht die Anrede "@Agent".
func (a Activity) CleanText() string {
	return strings.TrimSpace(mentionTag.ReplaceAllString(a.Text, ""))
}

// IsEcho erkennt die eigene Antwort des Bots: Absender = Empfänger (die
// Bot-Identität). Solche Activities dürfen keinen Wake-Zyklus erzeugen.
func (a Activity) IsEcho() bool {
	return a.From.ID != "" && a.From.ID == a.Recipient.ID
}

// InIntakeScope prüft den konfigurierbaren Tenant-Filter
// (COVEY_TEAMS_INTAKE_TENANTS). Ohne Allowlist: alle Tenants.
func (a Activity) InIntakeScope() bool {
	tenants := intakeTenants()
	if len(tenants) == 0 {
		return true
	}
	return tenants[strings.ToLower(strings.TrimSpace(a.Conversation.TenantID))]
}

// ShouldWake ist die vollständige Aufnahme-Entscheidung: eine echte
// Nutzer-Nachricht (type=message, mit Absender und Text, kein Echo) aus einem
// zugelassenen Tenant. Nur dann entsteht eine Aufgabe bzw. wird eine geblockte
// Aufgabe geweckt (orchestrator.HandleWebhook gated auf diesem Flag).
func (a Activity) ShouldWake() bool {
	return strings.EqualFold(a.Type, "message") &&
		a.From.ID != "" &&
		!a.IsEcho() &&
		a.CleanText() != "" &&
		a.InIntakeScope()
}

// --- JWT-Validierung des Messaging-Endpoints (spec/15) ---
//
// Der Azure Bot Service signiert jede Zustellung mit einem JWT im
// Authorization-Header (Issuer api.botframework.com, Audience = Bot-App-ID,
// RS256, Schlüssel aus der Bot-Framework-JWKS). Covey validiert das Token,
// bevor es dem Event traut.

const (
	botFrameworkIssuer = "https://api.botframework.com"
	openIDConfigURL    = "https://login.botframework.com/v1/.well-known/openidconfiguration"
	jwksTTL            = time.Hour
)

// VerifyToken prüft den JWT-Bearer aus dem Authorization-Header gegen die
// erwartete Bot-App-ID (die Audience). Leere appID = Prüfung deaktiviert
// (Dev-Modus / faketeams) — dieselbe Konvention wie das leere HMAC-Secret bei
// Zammad.
func VerifyToken(appID, authHeader string) bool {
	if appID == "" {
		return true
	}
	tok, ok := strings.CutPrefix(strings.TrimSpace(authHeader), "Bearer ")
	if !ok {
		return false
	}
	return defaultVerifier.verify(appID, strings.TrimSpace(tok)) == nil
}

var defaultVerifier = &tokenVerifier{now: time.Now}

// tokenVerifier validiert Bot-Framework-JWTs und cacht die öffentlichen
// Signatur-Schlüssel (JWKS) bis jwksTTL. keyFunc ist ein Test-Haken: ist er
// gesetzt, ersetzt er den Netz-Abruf der JWKS.
type tokenVerifier struct {
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	now       func() time.Time
	keyFunc   jwt.Keyfunc // nur in Tests gesetzt
}

func (v *tokenVerifier) verify(appID, tokenStr string) error {
	kf := v.keyFunc
	if kf == nil {
		kf = v.jwksKeyFunc
	}
	_, err := jwt.Parse(tokenStr, kf,
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithIssuer(botFrameworkIssuer),
		jwt.WithAudience(appID),
		jwt.WithExpirationRequired(),
	)
	return err
}

// jwksKeyFunc löst die kid aus dem Token-Header gegen die (gecachten)
// Bot-Framework-Schlüssel auf; fehlt der Schlüssel oder ist der Cache abgelaufen,
// wird die JWKS neu geladen.
func (v *tokenVerifier) jwksKeyFunc(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, fmt.Errorf("token ohne kid")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.keys == nil || v.now().Sub(v.fetchedAt) > jwksTTL {
		if err := v.refreshLocked(); err != nil {
			return nil, err
		}
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	// Schlüsselrotation: einmal frisch laden, bevor wir aufgeben.
	if err := v.refreshLocked(); err != nil {
		return nil, err
	}
	key, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unbekannte kid %q", kid)
	}
	return key, nil
}

// refreshLocked lädt die JWKS-Schlüssel neu. Aufrufer hält v.mu.
func (v *tokenVerifier) refreshLocked() error {
	keys, err := fetchJWKS(context.Background())
	if err != nil {
		return err
	}
	v.keys = keys
	v.fetchedAt = v.now()
	return nil
}

var jwksHTTP = &http.Client{Timeout: 10 * time.Second}

// fetchJWKS holt die OpenID-Metadaten (jwks_uri) und daraus die
// RSA-Signatur-Schlüssel des Bot Frameworks.
func fetchJWKS(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := getJSON(ctx, openIDConfigURL, &meta); err != nil {
		return nil, fmt.Errorf("openid-config: %w", err)
	}
	if meta.JWKSURI == "" {
		return nil, fmt.Errorf("openid-config ohne jwks_uri")
	}
	var set struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := getJSON(ctx, meta.JWKSURI, &set); err != nil {
		return nil, fmt.Errorf("jwks: %w", err)
	}
	out := map[string]*rsa.PublicKey{}
	for _, k := range set.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := jwkToRSA(k.N, k.E)
		if err != nil {
			continue
		}
		out[k.Kid] = pub
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("jwks: keine RSA-Schlüssel")
	}
	return out, nil
}

func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := jwksHTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

// jwkToRSA baut aus den base64url-kodierten Feldern n (Modulus) und e (Exponent)
// einen RSA-Public-Key.
func jwkToRSA(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(nStr, "="))
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(eStr, "="))
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eBytes {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("exponent 0")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: e}, nil
}
