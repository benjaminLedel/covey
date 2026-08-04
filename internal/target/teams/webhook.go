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

	"covey/internal/reqlog"
)

// Activity is the relevant excerpt of a Bot Framework activity as the Azure
// Bot Service delivers it to the messaging endpoint (spec/15).
type Activity struct {
	Type         string              `json:"type"`
	ID           string              `json:"id"`
	Text         string              `json:"text"`
	ServiceURL   string              `json:"serviceUrl"`
	ChannelID    string              `json:"channelId"`
	From         ChannelAccount      `json:"from"`
	Recipient    ChannelAccount      `json:"recipient"`
	Conversation ConversationAccount `json:"conversation"`
	Attachments  []Attachment        `json:"attachments"`
	// Name/Value carry the invoke activities. For outgoing files that is
	// fileConsent/invoke: the recipient's answer to the consent card.
	Name  string      `json:"name"`
	Value InvokeValue `json:"value"`
}

// InvokeValue is the value block of a fileConsent/invoke activity: what the
// recipient decided, which file was meant (context.key, set by us) and — on
// consent — where the bytes go (uploadInfo).
type InvokeValue struct {
	Type    string `json:"type"`
	Action  string `json:"action"`
	Context struct {
		Key string `json:"key"`
	} `json:"context"`
	UploadInfo struct {
		UploadURL  string `json:"uploadUrl"`
		ContentURL string `json:"contentUrl"`
		Name       string `json:"name"`
		UniqueID   string `json:"uniqueId"`
		FileType   string `json:"fileType"`
	} `json:"uploadInfo"`
}

// Card content types of the Teams file exchange.
const (
	consentCardContentType = "application/vnd.microsoft.teams.card.file.consent"
	infoCardContentType    = "application/vnd.microsoft.teams.card.file.info"
	fileConsentInvokeName  = "fileConsent/invoke"
)

// IsFileConsent recognizes the answer to a consent card.
func (a Activity) IsFileConsent() bool {
	return strings.EqualFold(a.Type, "invoke") && a.Name == fileConsentInvokeName
}

// ConsentAccepted says whether the recipient consented — and whether we got an
// upload URL. Without a URL even an "accept" is worthless.
func (a Activity) ConsentAccepted() bool {
	return strings.EqualFold(a.Value.Action, "accept") && a.Value.UploadInfo.UploadURL != ""
}

// Attachment is a file attachment of an activity. Teams delivers files
// differently depending on the channel: shared files as contentType
// "application/vnd.microsoft.teams.file.download.info" with a pre-authorized
// content.downloadUrl; inline images as image/* with a contentUrl on the
// connector host (bearer token required). DownloadURL() unifies both.
type Attachment struct {
	ContentType string `json:"contentType"`
	ContentURL  string `json:"contentUrl"`
	Name        string `json:"name"`
	// Depending on the type, Content is either an object (file.download.info:
	// {downloadUrl,…}) or a string (text/html: the rich-text message) — hence raw.
	Content json.RawMessage `json:"content"`
}

// contentDownloadURL extracts content.downloadUrl if Content is an object. If
// Content is a string (text/html) or empty, the result is "".
func (at Attachment) contentDownloadURL() string {
	if len(at.Content) == 0 {
		return ""
	}
	var c struct {
		DownloadURL string `json:"downloadUrl"`
	}
	_ = json.Unmarshal(at.Content, &c) // error is fine: Content can be a string
	return c.DownloadURL
}

// DownloadURL is the effective URL the bytes live at: preferably the
// pre-authorized content.downloadUrl (shared files), otherwise the contentUrl
// (inline images).
func (at Attachment) DownloadURL() string {
	if u := at.contentDownloadURL(); u != "" {
		return u
	}
	return at.ContentURL
}

// Filename is the display name of the attachment; if it is missing, the
// basename is derived from the download URL.
func (at Attachment) Filename() string {
	if at.Name != "" {
		return at.Name
	}
	u := at.DownloadURL()
	if i := strings.LastIndexAny(u, "/?"); i >= 0 && u[i] == '/' {
		if base := u[i+1:]; base != "" {
			return base
		}
	}
	return "attachment"
}

// isDownloadable filters out the real file attachments: those with a download
// URL, but not the rich-text representation of the message (text/html) and not
// cards (adaptive cards).
func (at Attachment) isDownloadable() bool {
	if at.DownloadURL() == "" {
		return false
	}
	if at.ContentType == "text/html" || strings.HasPrefix(at.ContentType, "application/vnd.microsoft.card") {
		return false
	}
	return true
}

// Files returns the downloadable file attachments of the activity.
func (a Activity) Files() []Attachment {
	var out []Attachment
	for _, at := range a.Attachments {
		if at.isDownloadable() {
			out = append(out, at)
		}
	}
	return out
}

// ChannelAccount identifies the sender or recipient of an activity.
type ChannelAccount struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	AADObjectID string `json:"aadObjectId"`
}

// ConversationAccount identifies the conversation (1:1, group, channel).
type ConversationAccount struct {
	ID               string `json:"id"`
	ConversationType string `json:"conversationType"`
	TenantID         string `json:"tenantId"`
}

// ParseWebhook reads the raw activity. An activity without a type is not a
// valid Bot Framework payload and is rejected fail-closed.
func ParseWebhook(body []byte) (Activity, error) {
	var a Activity
	if err := json.Unmarshal(body, &a); err != nil {
		return a, fmt.Errorf("teams activity: %w", err)
	}
	if a.Type == "" {
		return a, fmt.Errorf("teams activity: type missing")
	}
	return a, nil
}

// CorrelationKey is the stable, natural correlation key for Teams: the
// conversation id that comes with every activity (spec/15).
func CorrelationKey(conversationID string) string {
	return "teams:conversation:" + conversationID
}

// DedupKey makes the webhook processing idempotent — the Bot Service repeats
// deliveries; the same activity may trigger only one wake. If the activity id
// is (rarely) missing, it falls back to conversation + text so the key does not
// collapse.
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

// CleanText removes the <at>…</at> mention tags Teams wraps around the bot
// name and trims the result — so the agent only sees the actual message, not
// the "@agent" salutation.
func (a Activity) CleanText() string {
	return strings.TrimSpace(mentionTag.ReplaceAllString(a.Text, ""))
}

// IsEcho recognizes the bot's own answer: sender = recipient (the bot
// identity). Such activities must not produce a wake cycle.
func (a Activity) IsEcho() bool {
	return a.From.ID != "" && a.From.ID == a.Recipient.ID
}

// InIntakeScope checks the configurable tenant filter
// (COVEY_TEAMS_INTAKE_TENANTS). Without an allowlist: all tenants.
func (a Activity) InIntakeScope() bool {
	tenants := intakeTenants()
	if len(tenants) == 0 {
		return true
	}
	return tenants[strings.ToLower(strings.TrimSpace(a.Conversation.TenantID))]
}

// ShouldWake is the complete intake decision: a genuine user message
// (type=message, with a sender, not an echo) from an admitted tenant that
// carries text or at least one file attachment. Only then does a task arise or
// a blocked task get woken (orchestrator.HandleWebhook gates on this flag).
//
// Second case: the answer to a consent card (fileConsent/invoke). It is not a
// message but the continuation of work the agent started — it is waiting for
// this to finish the upload. Consent as well as decline wake it; the decline so
// that it does not stay parked forever.
func (a Activity) ShouldWake() bool {
	if a.IsFileConsent() {
		return !a.IsEcho() && a.InIntakeScope()
	}
	return strings.EqualFold(a.Type, "message") &&
		a.From.ID != "" &&
		!a.IsEcho() &&
		(a.CleanText() != "" || len(a.Files()) > 0) &&
		a.InIntakeScope()
}

// --- JWT validation of the messaging endpoint (spec/15) ---
//
// The Azure Bot Service signs every delivery with a JWT in the Authorization
// header (issuer api.botframework.com, audience = bot app ID, RS256, keys from
// the Bot Framework JWKS). Covey validates the token before it trusts the
// event.

const (
	botFrameworkIssuer = "https://api.botframework.com"
	openIDConfigURL    = "https://login.botframework.com/v1/.well-known/openidconfiguration"
	jwksTTL            = time.Hour
)

// VerifyToken checks the JWT bearer from the Authorization header against the
// expected bot app ID (the audience). An empty appID = check disabled (dev
// mode / faketeams) — the same convention as the empty HMAC secret in Zammad.
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

// tokenVerifier validates Bot Framework JWTs and caches the public signature
// keys (JWKS) for up to jwksTTL. keyFunc is a test hook: if it is set, it
// replaces the network fetch of the JWKS.
type tokenVerifier struct {
	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	now       func() time.Time
	keyFunc   jwt.Keyfunc // only set in tests
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

// jwksKeyFunc resolves the kid from the token header against the (cached) Bot
// Framework keys; if the key is missing or the cache has expired, the JWKS is
// reloaded.
func (v *tokenVerifier) jwksKeyFunc(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, fmt.Errorf("token without kid")
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
	// Key rotation: load once more before we give up.
	if err := v.refreshLocked(); err != nil {
		return nil, err
	}
	key, ok := v.keys[kid]
	if !ok {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	return key, nil
}

// refreshLocked reloads the JWKS keys. The caller holds v.mu.
func (v *tokenVerifier) refreshLocked() error {
	// No request context available (the caller already holds v.mu), but without
	// a deadline the token check would hang on a hanging JWKS endpoint — and
	// with it every incoming Teams call.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	keys, err := fetchJWKS(ctx)
	if err != nil {
		return err
	}
	v.keys = keys
	v.fetchedAt = v.now()
	return nil
}

var jwksHTTP = reqlog.Client("teams", 10*time.Second)

// fetchJWKS fetches the OpenID metadata (jwks_uri) and from it the RSA
// signature keys of the Bot Framework.
func fetchJWKS(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	var meta struct {
		JWKSURI string `json:"jwks_uri"`
	}
	if err := getJSON(ctx, openIDConfigURL, &meta); err != nil {
		return nil, fmt.Errorf("openid-config: %w", err)
	}
	if meta.JWKSURI == "" {
		return nil, fmt.Errorf("openid-config without jwks_uri")
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
		return nil, fmt.Errorf("jwks: no RSA keys")
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

// jwkToRSA builds an RSA public key from the base64url-encoded fields n
// (modulus) and e (exponent).
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
