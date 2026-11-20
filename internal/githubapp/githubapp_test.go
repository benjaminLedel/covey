package githubapp

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func testKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)})
	return k, string(p)
}

func TestParseKeyAcceptsEscapedNewlines(t *testing.T) {
	_, p := testKey(t)
	if _, err := ParseKey(strings.ReplaceAll(p, "\n", `\n`)); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseKey("nope"); err == nil {
		t.Fatal("garbage must not parse")
	}
}

func TestJWTClaims(t *testing.T) {
	k, _ := testKey(t)
	now := time.Unix(1_800_000_000, 0)
	s, err := App{ID: 123, PrivateKey: k}.JWT(now)
	if err != nil {
		t.Fatal(err)
	}
	var c jwt.RegisteredClaims
	_, err = jwt.ParseWithClaims(s, &c, func(*jwt.Token) (any, error) { return &k.PublicKey, nil },
		jwt.WithTimeFunc(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	if c.Issuer != "123" || c.ExpiresAt.Sub(now) > 10*time.Minute {
		t.Fatalf("claims %+v", c)
	}
}

func TestInstallationTokenIsCached(t *testing.T) {
	k, _ := testKey(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/7/access_tokens" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		calls.Add(1)
		w.Write([]byte(`{"token":"ghs_` + string(rune('a'+calls.Load())) + `","expires_at":"` + time.Now().Add(time.Hour).UTC().Format(time.RFC3339) + `"}`))
	}))
	defer srv.Close()
	c := NewClient()
	app := App{ID: 1, PrivateKey: k, API: srv.URL}
	t1, err := c.InstallationToken(context.Background(), app, 7)
	if err != nil {
		t.Fatal(err)
	}
	t2, _ := c.InstallationToken(context.Background(), app, 7)
	if t1.Value != t2.Value || calls.Load() != 1 {
		t.Fatalf("cache miss: %s %s, %d calls", t1.Value, t2.Value, calls.Load())
	}
	// Close to expiry → a new token.
	c.Now = func() time.Time { return time.Now().Add(55 * time.Minute) }
	t3, _ := c.InstallationToken(context.Background(), app, 7)
	if t3.Value == t1.Value || calls.Load() != 2 {
		t.Fatalf("expected refresh, got %s after %d calls", t3.Value, calls.Load())
	}
}

func TestConvertManifest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/app-manifests/abc/conversions" || r.Header.Get("Authorization") != "" {
			t.Errorf("unexpected %s auth=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":9,"slug":"acme-agents","pem":"-----BEGIN","webhook_secret":"s","owner":{"login":"acme"}}`))
	}))
	defer srv.Close()
	got, err := NewClient().ConvertManifest(context.Background(), srv.URL, "abc")
	if err != nil || got.ID != 9 || got.Slug != "acme-agents" || got.Owner.Login != "acme" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestErrorsCarryGitHubMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()
	k, _ := testKey(t)
	_, err := NewClient().GetInstallation(context.Background(), App{ID: 1, PrivateKey: k, API: srv.URL}, 5)
	var ae *APIError
	if err == nil || !strings.Contains(err.Error(), "404") || !errorsAs(err, &ae) {
		t.Fatalf("got %v", err)
	}
}

func TestManifestURLs(t *testing.T) {
	m := NewManifest("acme agents", "https://covey.example/")
	if m.HookAttributes.URL != "https://covey.example/api/webhooks/github-app" || m.Public || m.DefaultPermissions["contents"] != "write" {
		t.Fatalf("%+v", m)
	}
	if u := ManifestFormURL("acme", "s t"); u != "https://github.com/organizations/acme/settings/apps/new?state=s+t" {
		t.Fatal(u)
	}
	if u := ManifestFormURL("", "x"); !strings.HasPrefix(u, "https://github.com/settings/apps/new") {
		t.Fatal(u)
	}
	if u := InstallURL("covey", "x"); u != "https://github.com/apps/covey/installations/new?state=x" {
		t.Fatal(u)
	}
}

func errorsAs(err error, target **APIError) bool {
	e, ok := err.(*APIError)
	if ok {
		*target = e
	}
	return ok
}
