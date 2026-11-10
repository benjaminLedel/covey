package teamsapp

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Linking a Microsoft tenant to an organisation.
//
// The tenant ID is what routes every message, so it has to be proven, not
// typed: an organisation that could claim any tenant would receive that
// tenant's messages. The proof is a sign-in with Microsoft through the
// installation's app — the tenant comes out of the ID token the token
// endpoint hands back over TLS (never out of a redirect parameter, which the
// person clicking could edit) — by someone holding a Teams or Global
// Administrator role in that tenant.
//
// Two further bindings keep the flow from being turned around. The state
// parameter is signed and names the organisation and an expiry; and its
// nonce must match a cookie set in the browser of the covey admin who
// started the link. A link started by somebody else and sent to a tenant's
// administrator therefore ends without the cookie and is refused, instead of
// handing that tenant to the sender's organisation.

// LinkCookie carries the nonce of a link in progress.
const LinkCookie = "covey_teamsapp_link"

const linkTTL = 15 * time.Minute

// Directory role template IDs (Microsoft Entra built-in roles) that may link
// a tenant.
var adminRoles = map[string]string{
	"62e90394-69f5-4237-9190-012177145e10": "Global Administrator",
	"69091246-20e8-4a56-aa4d-066075b2a7a8": "Teams Administrator",
}

var (
	// ErrLinkState: the state is forged, expired, or not from this browser.
	ErrLinkState = errors.New("teamsapp: the sign-in does not belong to a link started in this browser, or it expired — start again from covey")
	// ErrNotAdmin: the signed-in account holds no admin role in its tenant.
	ErrNotAdmin = errors.New("teamsapp: only a Teams or Global Administrator of the Microsoft tenant can connect it")
)

func (s *Service) loginBase() string {
	if b := strings.TrimRight(strings.TrimSpace(s.cfg.LoginBase), "/"); b != "" {
		return b
	}
	return "https://login.microsoftonline.com"
}

func (s *Service) stateKey() []byte {
	k := sha256.Sum256([]byte("covey teamsapp link state\x00" + s.cfg.AppPassword))
	return k[:]
}

// StartLink returns the Microsoft sign-in URL and the nonce to put into the
// LinkCookie.
func (s *Service) StartLink(orgID uuid.UUID, redirectURI string) (signIn, nonce string, err error) {
	if s == nil {
		return "", "", errNoApp
	}
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	nonce = base64.RawURLEncoding.EncodeToString(b)
	exp := time.Now().Add(linkTTL).Unix()
	payload := orgID.String() + "|" + strconv.FormatInt(exp, 10) + "|" + nonce
	mac := hmac.New(sha256.New, s.stateKey())
	mac.Write([]byte(payload))
	state := base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + hex.EncodeToString(mac.Sum(nil))

	q := url.Values{
		"client_id":     {s.cfg.AppID},
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"response_mode": {"query"},
		"scope":         {"openid profile"},
		"state":         {state},
		"nonce":         {nonce},
		"prompt":        {"select_account"},
	}
	return s.loginBase() + "/organizations/oauth2/v2.0/authorize?" + q.Encode(), nonce, nil
}

// checkState verifies the state against the cookie and returns the
// organisation it was started for.
func (s *Service) checkState(state, cookieNonce string) (uuid.UUID, string, error) {
	enc, sig, ok := strings.Cut(state, ".")
	if !ok {
		return uuid.Nil, "", ErrLinkState
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return uuid.Nil, "", ErrLinkState
	}
	mac := hmac.New(sha256.New, s.stateKey())
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(sig), []byte(want)) {
		return uuid.Nil, "", ErrLinkState
	}
	parts := strings.Split(string(raw), "|")
	if len(parts) != 3 {
		return uuid.Nil, "", ErrLinkState
	}
	org, err := uuid.Parse(parts[0])
	exp, err2 := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || err2 != nil || time.Now().Unix() > exp {
		return uuid.Nil, "", ErrLinkState
	}
	if cookieNonce == "" || !hmac.Equal([]byte(cookieNonce), []byte(parts[2])) {
		return uuid.Nil, "", ErrLinkState
	}
	return org, parts[2], nil
}

// FinishLink completes a link: checks the state and the cookie, exchanges the
// code, reads tenant, account and roles from the ID token, and links the
// tenant to the organisation the state names.
func (s *Service) FinishLink(ctx context.Context, state, cookieNonce, code, redirectURI string) (orgID uuid.UUID, tenantID string, err error) {
	if s == nil {
		return uuid.Nil, "", errNoApp
	}
	org, nonce, err := s.checkState(state, cookieNonce)
	if err != nil {
		return uuid.Nil, "", err
	}
	claims, err := s.exchange(ctx, code, redirectURI)
	if err != nil {
		return org, "", err
	}
	if claims.Aud != s.cfg.AppID || claims.Nonce != nonce || claims.Tid == "" {
		return org, "", fmt.Errorf("teamsapp: the ID token does not belong to this sign-in")
	}
	if !holdsAdminRole(claims.Wids) {
		if len(claims.Wids) == 0 {
			return org, "", fmt.Errorf("%w (the sign-in carried no directory roles: the app registration has to emit them — groupMembershipClaims \"DirectoryRole\")", ErrNotAdmin)
		}
		return org, "", ErrNotAdmin
	}
	by := claims.PreferredUsername
	if by == "" {
		by = claims.Name
	}
	if err := s.Store.Link(ctx, org, claims.Tid, by); err != nil {
		return org, claims.Tid, err
	}
	return org, claims.Tid, nil
}

func holdsAdminRole(wids []string) bool {
	for _, w := range wids {
		if _, ok := adminRoles[strings.ToLower(w)]; ok {
			return true
		}
	}
	return false
}

type idClaims struct {
	Aud               string   `json:"aud"`
	Tid               string   `json:"tid"`
	Nonce             string   `json:"nonce"`
	Wids              []string `json:"wids"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
}

// exchange trades the code for tokens at the token endpoint. The ID token is
// read without checking its signature: it comes straight from Microsoft's
// token endpoint over TLS, in answer to our own client authentication, which
// OpenID Connect Core (3.1.3.7) accepts in place of the signature check.
func (s *Service) exchange(ctx context.Context, code, redirectURI string) (idClaims, error) {
	form := url.Values{
		"client_id":     {s.cfg.AppID},
		"client_secret": {s.cfg.AppPassword},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
		"scope":         {"openid profile"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.loginBase()+"/organizations/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return idClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return idClaims{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &e)
		return idClaims{}, fmt.Errorf("teamsapp: Microsoft refused the sign-in: %.300s", e.Description)
	}
	var out struct {
		IDToken string `json:"id_token"`
	}
	if err := json.Unmarshal(data, &out); err != nil || out.IDToken == "" {
		return idClaims{}, fmt.Errorf("teamsapp: the token endpoint returned no ID token")
	}
	parts := strings.Split(out.IDToken, ".")
	if len(parts) != 3 {
		return idClaims{}, fmt.Errorf("teamsapp: malformed ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return idClaims{}, fmt.Errorf("teamsapp: malformed ID token")
	}
	var c idClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return idClaims{}, fmt.Errorf("teamsapp: malformed ID token")
	}
	return c, nil
}
