package integration

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"covey/internal/secrets/sealbox"
	"covey/internal/sso"
)

// fakeIdP is an OIDC provider of the smallest kind: discovery, keys, an
// authorization endpoint that signs in whoever the test names, and a token
// endpoint that checks the PKCE verifier and issues an RS256 ID token.
type fakeIdP struct {
	srv *httptest.Server
	key *rsa.PrivateKey

	mu       sync.Mutex
	next     map[string]any // the claims the next sign-in carries
	refuse   string         // non-empty: the token endpoint refuses the client with this description
	codes    map[string]idpCode
	clientID string
}

type idpCode struct {
	claims    map[string]any
	nonce     string
	challenge string
}

func newFakeIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIdP{key: key, codes: map[string]idpCode{}, clientID: "covey-client"}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}})
	})
	mux.HandleFunc("GET /authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("client_id") != f.clientID || q.Get("code_challenge_method") != "S256" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		code := "code-" + q.Get("state")
		f.codes[code] = idpCode{claims: f.next, nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
		f.mu.Unlock()
		back, _ := url.Parse(q.Get("redirect_uri"))
		bq := back.Query()
		bq.Set("code", code)
		bq.Set("state", q.Get("state"))
		back.RawQuery = bq.Encode()
		http.Redirect(w, r, back.String(), http.StatusFound)
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		f.mu.Lock()
		if f.refuse != "" {
			desc := f.refuse
			f.mu.Unlock()
			w.WriteHeader(http.StatusUnauthorized)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client", "error_description": desc})
			return
		}
		c, ok := f.codes[r.PostForm.Get("code")]
		delete(f.codes, r.PostForm.Get("code"))
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != c.challenge {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant"})
			return
		}
		claims := map[string]any{"iss": f.srv.URL, "aud": f.clientID, "nonce": c.nonce,
			"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix()}
		for k, v := range c.claims {
			claims[k] = v
		}
		signer, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key},
			(&jose.SignerOptions{}).WithHeader("kid", "k1"))
		payload, _ := json.Marshal(claims)
		jws, _ := signer.Sign(payload)
		idt, _ := jws.CompactSerialize()
		json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer",
			"expires_in": 3600, "id_token": idt})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// signIn runs the browser's part: start, provider, callback. It returns the
// client and where the browser ended up.
func (f *fakeIdP) signIn(t *testing.T, s *stack, email string, claims map[string]any) (*apiClient, *url.URL) {
	t.Helper()
	f.mu.Lock()
	f.next = claims
	f.mu.Unlock()
	c := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: newJar()}}
	resp, err := c.http.Get(s.http.URL + "/api/sso/start?email=" + url.QueryEscape(email))
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return c, resp.Request.URL
}

func ssoError(u *url.URL) string { return u.Query().Get("sso_error") }

// Single sign-on per organisation (#723): configure, prove the domain, sign
// in existing and new people, refuse foreign addresses, and require it.
func TestOrgSingleSignOn(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	idp := newFakeIdP(t)
	key, _ := sealbox.GenerateMasterKey()
	box, _ := sealbox.New(key)
	txt := map[string][]string{}
	var txtMu sync.Mutex
	s.srv.SSO = &sso.Service{Pool: s.pool, Box: box, LookupTXT: func(_ context.Context, name string) ([]string, error) {
		txtMu.Lock()
		defer txtMu.Unlock()
		return txt[name], nil
	}}

	s.mitglied(t, "anna@test.local", "Anna", "agent_owner", "anna-passwort")
	s.mitglied(t, "ops@test.local", "Ops", "auditor", "ops-passwort")
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET platform_role='system_admin' WHERE email='ops@test.local'`); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	anna := login(t, s, "anna@test.local", "anna-passwort")

	// Only the org admin configures it.
	anna.expect(http.MethodGet, "/api/v1/org/sso", nil, http.StatusForbidden)
	admin.expect(http.MethodPut, "/api/v1/org/sso", map[string]string{
		"issuer": idp.srv.URL + "/nowhere", "client_id": idp.clientID, "secret": "s3cret"}, http.StatusBadRequest)
	admin.expect(http.MethodPut, "/api/v1/org/sso", map[string]string{
		"issuer": idp.srv.URL, "client_id": idp.clientID, "default_role": "boss", "secret": "s3cret"}, http.StatusBadRequest)
	cfg := admin.expect(http.MethodPut, "/api/v1/org/sso", map[string]string{
		"issuer": idp.srv.URL, "client_id": idp.clientID, "secret": "s3cret"}, http.StatusOK)
	if cfg["default_role"] != "auditor" || cfg["secret"] != nil {
		t.Fatalf("saved configuration: %v", cfg)
	}

	// An unverified domain routes nobody.
	d := admin.expect(http.MethodPost, "/api/v1/org/sso/domains", map[string]string{"domain": "@Test.Local"}, http.StatusCreated)
	if d["domain"] != "test.local" || d["record"] != "_covey-challenge.test.local" {
		t.Fatalf("claimed domain: %v", d)
	}
	if got := admin.expect(http.MethodGet, "/api/v1/public/sso/discover?email=anna@test.local", nil, http.StatusOK); got["sso"] != false {
		t.Fatalf("an unverified domain was discovered: %v", got)
	}
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/verify", nil, http.StatusUnprocessableEntity)
	txtMu.Lock()
	txt["_covey-challenge.test.local"] = []string{"something else", d["token"].(string)}
	txtMu.Unlock()
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/verify", nil, http.StatusOK)
	if got := admin.expect(http.MethodGet, "/api/v1/public/sso/discover?email=anna@test.local", nil, http.StatusOK); got["sso"] != true || got["required"] != false {
		t.Fatalf("discovery after verification: %v", got)
	}

	// Another organisation cannot claim the same domain.
	s.alsSystemadmin(t)
	second := admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]string{
		"name": "Other", "admin_email": "boss@other.example", "admin_name": "Boss", "admin_password": "boss-passwort",
	}, http.StatusCreated)
	_ = second
	boss := login(t, s, "boss@other.example", "boss-passwort")
	boss.expect(http.MethodPost, "/api/v1/org/sso/domains", map[string]string{"domain": "test.local"}, http.StatusConflict)
	if _, err := s.pool.Exec(ctx, `UPDATE accounts SET platform_role='user' WHERE email='admin@test.local'`); err != nil {
		t.Fatal(err)
	}

	// An existing account is bound on its first sign-in and keeps its seat.
	annaSSO, end := idp.signIn(t, s, "anna@test.local", map[string]any{"sub": "anna-sub", "email": "anna@test.local", "email_verified": true})
	if e := ssoError(end); e != "" {
		t.Fatalf("anna's sign-in failed: %s", e)
	}
	me := annaSSO.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != s.orgID.String() || me["Role"] != "agent_owner" {
		t.Fatalf("anna after single sign-on: %v", me)
	}

	// A new person gets an account and a seat with the configured role.
	newSSO, end := idp.signIn(t, s, "neu@test.local", map[string]any{"sub": "neu-sub", "preferred_username": "Neu@Test.Local", "name": "Neu"})
	if e := ssoError(end); e != "" {
		t.Fatalf("a new person's sign-in failed: %s", e)
	}
	me = newSSO.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != s.orgID.String() || me["Role"] != "auditor" || me["Email"] != "neu@test.local" {
		t.Fatalf("new person after single sign-on: %v", me)
	}

	// The provider speaks only for the verified domains: an address of
	// another organisation's domain does not sign in, not even as the boss.
	_, end = idp.signIn(t, s, "anna@test.local", map[string]any{"sub": "eve-sub", "email": "boss@other.example"})
	if e := ssoError(end); e != "foreign_address" {
		t.Fatalf("a foreign address: sso_error=%q, expected foreign_address", e)
	}
	// An address the provider says it has not verified is not believed.
	_, end = idp.signIn(t, s, "anna@test.local", map[string]any{"sub": "x-sub", "email": "anna@test.local", "email_verified": false})
	if e := ssoError(end); e != "unverified_email" {
		t.Fatalf("an unverified address: sso_error=%q, expected unverified_email", e)
	}
	// A callback without the flow cookie is refused.
	resp, err := (&http.Client{Jar: newJar()}).Get(s.http.URL + "/api/sso/callback?state=a&code=b")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if e := ssoError(resp.Request.URL); e != "expired" {
		t.Fatalf("callback without a flow: sso_error=%q", e)
	}

	// Requiring it needs the admin to have come through the provider.
	admin.expect(http.MethodPut, "/api/v1/org/sso/required", map[string]bool{"required": true}, http.StatusConflict)
	adminSSO, end := idp.signIn(t, s, "admin@test.local", map[string]any{"sub": "admin-sub", "email": "admin@test.local"})
	if e := ssoError(end); e != "" {
		t.Fatalf("admin's sign-in failed: %s", e)
	}
	if got := adminSSO.expect(http.MethodGet, "/api/v1/org/sso", nil, http.StatusOK); got["signed_in_via_sso"] != true {
		t.Fatalf("admin not seen as signed in through the provider: %v", got)
	}
	out := adminSSO.expect(http.MethodPut, "/api/v1/org/sso/required", map[string]bool{"required": true}, http.StatusOK)
	// admin and anna lose their password; neu never had one, ops is a
	// system admin.
	if out["rewritten"] != float64(2) {
		t.Fatalf("rewritten accounts: %v, expected 2", out["rewritten"])
	}

	// The rewritten accounts' password sessions are gone, the acting one
	// stays, and so do sessions that came through the provider.
	admin.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized)
	anna.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized)
	adminSSO.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)

	// Password moves are refused with a pointer to single sign-on.
	plain := &apiClient{t: t, base: s.http.URL, http: &http.Client{Jar: newJar()}}
	if got := plain.expect(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "anna@test.local", "password": "anna-passwort"}, http.StatusConflict); got["sso"] != true {
		t.Fatalf("password sign-in under required SSO: %v", got)
	}
	var hash string
	if err := s.pool.QueryRow(ctx, `SELECT password_hash FROM accounts WHERE email='anna@test.local'`).Scan(&hash); err != nil || hash != sso.NoPassword {
		t.Fatalf("anna's password was not removed: %q %v", hash, err)
	}
	users := adminSSO.expectList(http.MethodGet, "/api/v1/users", nil, http.StatusOK)
	var annaSeat string
	for _, u := range users {
		if u["email"] == "anna@test.local" {
			annaSeat = u["id"].(string)
		}
	}
	adminSSO.expect(http.MethodPatch, "/api/v1/users/"+annaSeat, map[string]string{"password": "new-passwort"}, http.StatusConflict)
	adminSSO.expect(http.MethodPatch, "/api/v1/auth/me", map[string]string{"password": "new-passwort", "current_password": "x"}, http.StatusConflict)
	// A new person in the domain is created without a password.
	adminSSO.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "carl@test.local", "display_name": "Carl", "role": "auditor"}, http.StatusCreated)

	// The instance's system admin keeps the password: the way back in.
	login(t, s, "ops@test.local", "ops-passwort")
	// A person signing in through the provider still gets in.
	if _, end := idp.signIn(t, s, "anna@test.local", map[string]any{"sub": "anna-sub", "email": "anna@test.local"}); ssoError(end) != "" {
		t.Fatalf("anna's sign-in under required SSO failed: %s", ssoError(end))
	}

	// A domain verified while SSO is required is rewritten at once.
	s.mitglied(t, "dora@zweig.test", "Dora", "auditor", "dora-passwort")
	dora := login(t, s, "dora@zweig.test", "dora-passwort")
	d = adminSSO.expect(http.MethodPost, "/api/v1/org/sso/domains", map[string]string{"domain": "zweig.test"}, http.StatusCreated)
	txtMu.Lock()
	txt["_covey-challenge.zweig.test"] = []string{d["token"].(string)}
	txtMu.Unlock()
	adminSSO.expect(http.MethodPost, "/api/v1/org/sso/domains/zweig.test/verify", nil, http.StatusOK)
	dora.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized)

	// Switching it off lets the password paths through again; nobody gets
	// a password back by it.
	adminSSO.expect(http.MethodPut, "/api/v1/org/sso/required", map[string]bool{"required": false}, http.StatusOK)
	plain.expect(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "anna@test.local", "password": "anna-passwort"}, http.StatusUnauthorized)
}

// An instance system admin confirms a domain by hand, without its DNS record
// (#729); an org admin cannot.
func TestOrgSSODomainConfirmedByHand(t *testing.T) {
	s := newStack(t)
	idp := newFakeIdP(t)
	key, _ := sealbox.GenerateMasterKey()
	box, _ := sealbox.New(key)
	s.srv.SSO = &sso.Service{Pool: s.pool, Box: box, LookupTXT: func(context.Context, string) ([]string, error) {
		return nil, nil
	}}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPut, "/api/v1/org/sso", map[string]string{
		"issuer": idp.srv.URL, "client_id": idp.clientID, "secret": "s3cret"}, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains", map[string]string{"domain": "test.local"}, http.StatusCreated)

	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/confirm", nil, http.StatusForbidden)
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/verify", nil, http.StatusUnprocessableEntity)

	s.alsSystemadmin(t)
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/unclaimed.example/confirm", nil, http.StatusNotFound)
	d := admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/confirm", nil, http.StatusOK)
	if d["verified_at"] == nil || d["verified_method"] != "manual" {
		t.Fatalf("confirmed by hand: %v", d)
	}
	var by string
	if err := s.pool.QueryRow(context.Background(), `SELECT a.email FROM org_sso_domains d JOIN accounts a ON a.id=d.verified_by
		WHERE d.domain='test.local'`).Scan(&by); err != nil || by != "admin@test.local" {
		t.Fatalf("confirmed by %q, %v", by, err)
	}
	if got := admin.expect(http.MethodGet, "/api/v1/public/sso/discover?email=anna@test.local", nil, http.StatusOK); got["sso"] != true {
		t.Fatalf("a domain confirmed by hand is not discovered: %v", got)
	}
	// The panel says how it was confirmed.
	if got := admin.expect(http.MethodGet, "/api/v1/org/sso", nil, http.StatusOK); got["domains"].([]any)[0].(map[string]any)["verified_method"] != "manual" {
		t.Fatalf("domains: %v", got["domains"])
	}
}

// A failed sign-in names its reason to the person and keeps the provider's
// message for the organisation's admin; a success clears it (#734).
func TestOrgSSOFailureIsVisible(t *testing.T) {
	s := newStack(t)
	idp := newFakeIdP(t)
	key, _ := sealbox.GenerateMasterKey()
	box, _ := sealbox.New(key)
	txt := map[string][]string{}
	s.srv.SSO = &sso.Service{Pool: s.pool, Box: box, LookupTXT: func(_ context.Context, name string) ([]string, error) {
		return txt[name], nil
	}}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPut, "/api/v1/org/sso", map[string]string{
		"issuer": idp.srv.URL, "client_id": idp.clientID, "secret": "s3cret"}, http.StatusOK)
	d := admin.expect(http.MethodPost, "/api/v1/org/sso/domains", map[string]string{"domain": "test.local"}, http.StatusCreated)
	txt["_covey-challenge.test.local"] = []string{d["token"].(string)}
	admin.expect(http.MethodPost, "/api/v1/org/sso/domains/test.local/verify", nil, http.StatusOK)

	idp.mu.Lock()
	idp.refuse = "AADSTS7000215: Invalid client secret provided."
	idp.mu.Unlock()
	_, end := idp.signIn(t, s, "admin@test.local", map[string]any{"sub": "a", "email": "admin@test.local"})
	if e := ssoError(end); e != "token" {
		t.Fatalf("a refused code: sso_error=%q, expected token", e)
	}
	cfg := admin.expect(http.MethodGet, "/api/v1/org/sso", nil, http.StatusOK)["config"].(map[string]any)
	if msg, _ := cfg["last_error"].(string); !strings.Contains(msg, "AADSTS7000215") || cfg["last_error_at"] == nil {
		t.Fatalf("the admin does not see the provider's message: %v", cfg)
	}

	_, end = idp.signIn(t, s, "admin@test.local", map[string]any{"sub": "a"})
	if e := ssoError(end); e != "token" {
		t.Fatalf("still refused: %q", e)
	}
	idp.mu.Lock()
	idp.refuse = ""
	idp.mu.Unlock()
	_, end = idp.signIn(t, s, "admin@test.local", map[string]any{"sub": "a"})
	if e := ssoError(end); e != "no_email" {
		t.Fatalf("a token without an address: sso_error=%q, expected no_email", e)
	}
	_, end = idp.signIn(t, s, "admin@test.local", map[string]any{"sub": "a", "email": "admin@test.local"})
	if e := ssoError(end); e != "" {
		t.Fatalf("the sign-in failed: %q", e)
	}
	cfg = admin.expect(http.MethodGet, "/api/v1/org/sso", nil, http.StatusOK)["config"].(map[string]any)
	if cfg["last_error"] != nil || cfg["last_error_at"] != nil {
		t.Fatalf("a success did not clear the last failure: %v", cfg)
	}
}
