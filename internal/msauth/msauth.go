// Package msauth is covey as an OAuth client of the Microsoft identity
// platform: the authorization code flow a person starts in covey's interface
// and finishes at Microsoft, and the refresh of a delegated token.
//
// It is a client, not a protocol of our own — Microsoft is the server. What
// it adds is the binding that keeps the flow from being turned around: the
// state is signed and names what the sign-in is for (an organisation, an
// agent), and its nonce must match a cookie in the browser that started it.
// A sign-in somebody else started and sent to a victim therefore ends without
// that cookie and is refused, instead of binding the victim's Microsoft
// account to the sender's purpose.
package msauth

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
	"strings"
	"time"
)

// ErrState: the state is forged, expired, or not from this browser.
var ErrState = errors.New("the sign-in does not belong to a request started in this browser, or it expired — start again from covey")

// TTL is how long a started sign-in may take.
const TTL = 15 * time.Minute

// App is an app registration covey signs in through.
type App struct {
	ClientID string
	Secret   string
	// LoginBase is the identity platform; empty: https://login.microsoftonline.com.
	LoginBase string
	// Purpose separates the state keys of different flows, so a state
	// minted for one can never be redeemed at another.
	Purpose string
}

func (a App) base() string {
	if b := strings.TrimRight(strings.TrimSpace(a.LoginBase), "/"); b != "" {
		return b
	}
	return "https://login.microsoftonline.com"
}

// mac signs a state with the app's client secret as the HMAC key; the
// purpose goes into the message, so a state minted for one flow never
// verifies at another.
func (a App) mac(raw []byte) string {
	m := hmac.New(sha256.New, []byte(a.Secret))
	m.Write([]byte("covey msauth state\x00" + a.Purpose + "\x00"))
	m.Write(raw)
	return hex.EncodeToString(m.Sum(nil))
}

type statePayload struct {
	Subject string `json:"s"`
	Expires int64  `json:"e"`
	Nonce   string `json:"n"`
}

// Start returns the Microsoft sign-in URL for subject (whatever the caller
// needs back at the end: "org", "org|agent|person") and the nonce to set as
// a cookie in this browser.
func (a App) Start(subject, redirectURI, scope string) (signIn, nonce string, err error) {
	b := make([]byte, 18)
	if _, err := rand.Read(b); err != nil {
		return "", "", err
	}
	nonce = base64.RawURLEncoding.EncodeToString(b)
	raw, _ := json.Marshal(statePayload{Subject: subject, Expires: time.Now().Add(TTL).Unix(), Nonce: nonce})
	state := base64.RawURLEncoding.EncodeToString(raw) + "." + a.mac(raw)
	q := url.Values{
		"client_id":     {a.ClientID},
		"response_type": {"code"},
		"redirect_uri":  {redirectURI},
		"response_mode": {"query"},
		"scope":         {scope},
		"state":         {state},
		"nonce":         {nonce},
		"prompt":        {"select_account"},
	}
	return a.base() + "/organizations/oauth2/v2.0/authorize?" + q.Encode(), nonce, nil
}

// checkState verifies the state against the cookie and returns its subject
// and nonce.
func (a App) checkState(state, cookieNonce string) (statePayload, error) {
	enc, sig, ok := strings.Cut(state, ".")
	if !ok {
		return statePayload{}, ErrState
	}
	raw, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return statePayload{}, ErrState
	}
	if !hmac.Equal([]byte(sig), []byte(a.mac(raw))) {
		return statePayload{}, ErrState
	}
	var p statePayload
	if err := json.Unmarshal(raw, &p); err != nil || time.Now().Unix() > p.Expires {
		return statePayload{}, ErrState
	}
	if cookieNonce == "" || !hmac.Equal([]byte(cookieNonce), []byte(p.Nonce)) {
		return statePayload{}, ErrState
	}
	return p, nil
}

// Tokens is the token endpoint's answer.
type Tokens struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
}

// Claims are the ID token claims covey reads.
type Claims struct {
	Aud               string   `json:"aud"`
	Tid               string   `json:"tid"`
	Oid               string   `json:"oid"`
	Nonce             string   `json:"nonce"`
	Wids              []string `json:"wids"`
	PreferredUsername string   `json:"preferred_username"`
	Name              string   `json:"name"`
}

// Who is the account as a person would recognise it.
func (c Claims) Who() string {
	if c.PreferredUsername != "" {
		return c.PreferredUsername
	}
	return c.Name
}

// Finish checks state and cookie, exchanges the code and reads the ID token.
// The ID token is read without checking its signature: it comes straight from
// Microsoft's token endpoint over TLS, in answer to our own client
// authentication, which OpenID Connect Core (3.1.3.7) accepts in place of the
// signature check. Its audience and nonce are checked.
func (a App) Finish(ctx context.Context, state, cookieNonce, code, redirectURI, scope string) (string, Tokens, Claims, error) {
	p, err := a.checkState(state, cookieNonce)
	if err != nil {
		return "", Tokens{}, Claims{}, err
	}
	tok, err := a.token(ctx, url.Values{
		"grant_type":   {"authorization_code"},
		"code":         {code},
		"redirect_uri": {redirectURI},
		"scope":        {scope},
	})
	if err != nil {
		return p.Subject, Tokens{}, Claims{}, err
	}
	c, err := readIDToken(tok.IDToken)
	if err != nil {
		return p.Subject, Tokens{}, Claims{}, err
	}
	if c.Aud != a.ClientID || c.Nonce != p.Nonce || c.Tid == "" {
		return p.Subject, Tokens{}, Claims{}, errors.New("the ID token does not belong to this sign-in")
	}
	return p.Subject, tok, c, nil
}

// OnBehalfOf trades a token issued to this app for the signed-in person (the
// one Teams hands a bot through single sign-on) for a delegated token with
// scope. The tenant is the person's: the on-behalf-of grant is not served by
// the multi-tenant endpoint.
func (a App) OnBehalfOf(ctx context.Context, tenant, assertion, scope string) (Tokens, error) {
	return a.tokenAt(ctx, tenant, url.Values{
		"grant_type":          {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"requested_token_use": {"on_behalf_of"},
		"assertion":           {assertion},
		"scope":               {scope},
	})
}

// Refresh trades a refresh token for a fresh access token (and, usually, a
// rotated refresh token the caller has to store).
func (a App) Refresh(ctx context.Context, refreshToken, scope string) (Tokens, error) {
	return a.token(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {scope},
	})
}

func (a App) token(ctx context.Context, form url.Values) (Tokens, error) {
	return a.tokenAt(ctx, "organizations", form)
}

func (a App) tokenAt(ctx context.Context, tenant string, form url.Values) (Tokens, error) {
	if strings.TrimSpace(tenant) == "" || strings.ContainsAny(tenant, "/?#") {
		return Tokens{}, errors.New("no usable tenant for the token request")
	}
	form.Set("client_id", a.ClientID)
	form.Set("client_secret", a.Secret)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		a.base()+"/"+url.PathEscape(tenant)+"/oauth2/v2.0/token", strings.NewReader(form.Encode()))
	if err != nil {
		return Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Tokens{}, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Description string `json:"error_description"`
		}
		_ = json.Unmarshal(data, &e)
		return Tokens{}, fmt.Errorf("Microsoft refused the token request: %.300s", e.Description)
	}
	var t Tokens
	if err := json.Unmarshal(data, &t); err != nil {
		return Tokens{}, fmt.Errorf("token endpoint: %w", err)
	}
	return t, nil
}

func readIDToken(idt string) (Claims, error) {
	parts := strings.Split(idt, ".")
	if len(parts) != 3 {
		return Claims{}, errors.New("the token endpoint returned no usable ID token")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return Claims{}, errors.New("malformed ID token")
	}
	var c Claims
	if err := json.Unmarshal(payload, &c); err != nil {
		return Claims{}, errors.New("malformed ID token")
	}
	return c, nil
}
