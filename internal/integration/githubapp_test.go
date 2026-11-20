package integration

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"covey/internal/agents"
	"covey/internal/githubapp"
)

// fakeGitHub stands in for api.github.com and github.com in the App flows.
type fakeGitHub struct {
	t       *testing.T
	srv     *httptest.Server
	key     *rsa.PrivateKey
	pem     string
	mu      sync.Mutex
	minted  int
	userSee []int64 // installations the OAuth user can see
	calls   []string
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeGitHub{t: t, key: k,
		pem: string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(k)}))}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeGitHub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	// The plugin addresses a non-github.com API as GitHub Enterprise (…/api/v3).
	p := strings.TrimPrefix(r.URL.Path, "/api/v3")
	switch {
	case r.Method == http.MethodPost && strings.HasPrefix(p, "/app-manifests/"):
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"id": 4242, "slug": "acme-agents", "html_url": "https://github.com/apps/acme-agents",
			"pem": f.pem, "webhook_secret": "own-hook-secret", "owner": map[string]string{"login": "acme"}})
	case r.Method == http.MethodGet && strings.HasPrefix(p, "/app/installations/"):
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ey") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var id int64
		fmt.Sscanf(strings.TrimPrefix(p, "/app/installations/"), "%d", &id)
		if id == 404 {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"id": id, "account": map[string]string{"login": "acme", "type": "Organization"}})
	case r.Method == http.MethodPost && strings.HasSuffix(p, "/access_tokens"):
		f.mu.Lock()
		f.minted++
		n := f.minted
		f.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"token": fmt.Sprintf("ghs_minted%d", n), "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339)})
	case r.Method == http.MethodPost && p == "/login/oauth/access_token":
		json.NewEncoder(w).Encode(map[string]string{"access_token": "ghu_user"})
	case r.Method == http.MethodGet && p == "/user/installations":
		var list []map[string]any
		for _, id := range f.userSee {
			list = append(list, map[string]any{"id": id, "app_id": 77})
		}
		json.NewEncoder(w).Encode(map[string]any{"installations": list})
	case r.Method == http.MethodGet && p == "/installation/repositories":
		json.NewEncoder(w).Encode(map[string]any{"total_count": 3, "repositories": []map[string]string{{"full_name": "acme/a"}}})
	default:
		f.t.Errorf("fake GitHub: unexpected %s %s", r.Method, p)
		w.WriteHeader(http.StatusNotImplemented)
	}
}

// noRedirect makes a logged-in client stop at redirects, the way a test
// wants to read where covey sends the browser.
func noRedirect(c *apiClient) {
	c.http.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
}

func (c *apiClient) get(path string) *http.Response {
	c.t.Helper()
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		c.t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func stateOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	st := u.Query().Get("state")
	if st == "" {
		t.Fatalf("no state in %s", raw)
	}
	return st
}

func outcomeOf(t *testing.T, resp *http.Response) (string, string) {
	t.Helper()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got HTTP %d", resp.StatusCode)
	}
	u, _ := url.Parse(resp.Header.Get("Location"))
	return u.Query().Get("github"), u.Query().Get("detail")
}

func githubAgent(t *testing.T, s *stack, slug string) agents.Agent {
	t.Helper()
	a, err := s.registry.Create(context.Background(), s.orgID, slug, slug, "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(context.Background(), a.ID, map[string]string{
		"SOUL.md": "# " + slug, "ACCESS.md": "- system: github scope: read,write,comment",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	return a
}

// An organisation creates its own App through the manifest flow, installs it,
// and from then on the probe — like every run — gets a minted installation
// token instead of a stored github_token.
func TestGitHubOwnAppManifestFlow(t *testing.T) {
	s := newStack(t)
	gh := newFakeGitHub(t)
	s.githubApp.API = gh.srv.URL
	ctx := context.Background()
	if err := s.targets.SetEnabled(ctx, s.orgID, "github", true); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	noRedirect(admin)

	start := admin.expect(http.MethodPost, "/api/v1/integrations/github/manifest",
		map[string]string{"github_org": "acme", "name": "acme agents"}, http.StatusOK)
	action := start["action"].(string)
	if !strings.HasPrefix(action, "https://github.com/organizations/acme/settings/apps/new?state=") {
		t.Fatalf("form target %s", action)
	}
	var m githubapp.Manifest
	if err := json.Unmarshal([]byte(start["manifest"].(string)), &m); err != nil {
		t.Fatal(err)
	}
	if m.HookAttributes.URL != s.http.URL+"/api/webhooks/github-app" || m.RedirectURL != s.http.URL+"/api/integrations/github/manifest" {
		t.Fatalf("manifest urls %+v", m)
	}

	// GitHub sends the browser back with the code.
	resp := admin.get("/api/integrations/github/manifest?code=abc&state=" + url.QueryEscape(stateOf(t, action)))
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Location"), "https://github.com/apps/acme-agents/installations/new?state=") {
		t.Fatalf("after creating: HTTP %d → %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	install := resp.Header.Get("Location")

	// … and after installing, with the installation.
	resp = admin.get("/api/integrations/github/installed?installation_id=99&setup_action=install&state=" + url.QueryEscape(stateOf(t, install)))
	if outcome, detail := outcomeOf(t, resp); outcome != "connected" || detail != "acme" {
		t.Fatalf("outcome %s (%s)", outcome, detail)
	}

	st := admin.expect(http.MethodGet, "/api/v1/integrations/github", nil, http.StatusOK)
	if st["installed"] != true || st["bot_login"] != "acme-agents[bot]" {
		t.Fatalf("status %v", st)
	}
	setup := admin.expect(http.MethodGet, "/api/v1/targets/github/setup", nil, http.StatusOK)
	if setup["connection"] != "app" {
		t.Fatalf("setup connection %v", setup["connection"])
	}
	probe := admin.expect(http.MethodPost, "/api/v1/targets/github/probe", map[string]any{}, http.StatusOK)
	if probe["ok"] != true || probe["identity"] != "@acme-agents[bot] (GitHub App) · 3 repositories" {
		t.Fatalf("probe %v", probe)
	}

	// The webhook of the own App is verified with its own secret and routed
	// by label to the one agent addressed.
	githubAgent(t, s, "dev")
	githubAgent(t, s, "qa")
	body := []byte(`{"action":"opened","installation":{"id":99},"repository":{"full_name":"acme/a"},
		"issue":{"number":5,"title":"Bug","body":"broken","labels":[{"name":"agent:qa"}],"user":{"login":"anna","type":"User"}},
		"sender":{"login":"anna","type":"User"}}`)
	out := postGitHubApp(t, s, body, "issues", "own-hook-secret")
	if out["agent"] != "qa" || out["routed_by"] != "label" {
		t.Fatalf("routed %v", out)
	}
	if bad := postGitHubAppStatus(t, s, body, "issues", "wrong"); bad != http.StatusUnauthorized {
		t.Fatalf("a wrong signature got HTTP %d", bad)
	}
	// Two agents, no address: not routed.
	plain := []byte(strings.Replace(string(body), `[{"name":"agent:qa"}]`, `[]`, 1))
	if out := postGitHubApp(t, s, plain, "issues", "own-hook-secret"); out["outcome"] != "not routed" {
		t.Fatalf("expected not routed, got %v", out)
	}
	// A mention addresses the agent too.
	mention := []byte(`{"action":"created","installation":{"id":99},"repository":{"full_name":"acme/a"},
		"issue":{"number":5,"title":"Bug","body":"x","labels":[],"user":{"login":"anna","type":"User"}},
		"comment":{"id":1,"body":"/covey dev please look","user":{"login":"anna","type":"User"}},
		"sender":{"login":"anna","type":"User"}}`)
	if out := postGitHubApp(t, s, mention, "issue_comment", "own-hook-secret"); out["agent"] != "dev" || out["routed_by"] != "mention" {
		t.Fatalf("mention %v", out)
	}

	// Uninstalling on GitHub ends the App's tokens.
	postGitHubApp(t, s, []byte(`{"action":"deleted","installation":{"id":99}}`), "installation", "own-hook-secret")
	st = admin.expect(http.MethodGet, "/api/v1/integrations/github", nil, http.StatusOK)
	if st["installed"] != false {
		t.Fatalf("still installed: %v", st)
	}
}

// The shared App is public: an installation id from the browser is only
// accepted when the installing user can see it on GitHub.
func TestGitHubSharedAppNeedsTheInstallingUser(t *testing.T) {
	s := newStack(t)
	gh := newFakeGitHub(t)
	s.githubApp.API = gh.srv.URL
	s.githubApp.Shared = &githubapp.SharedApp{App: githubapp.App{ID: 77, Slug: "covey", PrivateKey: gh.key, API: gh.srv.URL},
		WebhookSecret: "shared-secret", ClientID: "cid", ClientSecret: "csecret", Web: gh.srv.URL}
	if err := s.targets.SetEnabled(context.Background(), s.orgID, "github", true); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")
	noRedirect(admin)

	connect := func() string {
		r := admin.expect(http.MethodPost, "/api/v1/integrations/github/connect", map[string]string{"source": "shared"}, http.StatusOK)
		u := r["url"].(string)
		if !strings.HasPrefix(u, "https://github.com/apps/covey/installations/new?state=") {
			t.Fatalf("install url %s", u)
		}
		return stateOf(t, u)
	}

	// Without the OAuth code: refused.
	resp := admin.get("/api/integrations/github/installed?installation_id=7&setup_action=install&state=" + url.QueryEscape(connect()))
	if outcome, _ := outcomeOf(t, resp); outcome != "error" {
		t.Fatalf("expected error without code, got %s", outcome)
	}
	// Somebody else's installation: refused.
	gh.userSee = []int64{8}
	resp = admin.get("/api/integrations/github/installed?installation_id=7&code=c&setup_action=install&state=" + url.QueryEscape(connect()))
	if outcome, detail := outcomeOf(t, resp); outcome != "error" || !strings.Contains(detail, "not one of yours") {
		t.Fatalf("expected refusal, got %s %s", outcome, detail)
	}
	// Another browser (no flow cookie): refused.
	other := login(t, s, "admin@test.local", "admin-passwort")
	noRedirect(other)
	gh.userSee = []int64{7}
	resp = other.get("/api/integrations/github/installed?installation_id=7&code=c&state=" + url.QueryEscape(connect()))
	if outcome, _ := outcomeOf(t, resp); outcome != "error" {
		t.Fatalf("expected refusal in another browser, got %s", outcome)
	}
	// The real thing.
	resp = admin.get("/api/integrations/github/installed?installation_id=7&code=c&setup_action=install&state=" + url.QueryEscape(connect()))
	if outcome, _ := outcomeOf(t, resp); outcome != "connected" {
		t.Fatalf("expected connected, got %s", outcome)
	}
	st := admin.expect(http.MethodGet, "/api/v1/integrations/github", nil, http.StatusOK)
	if st["installed"] != true || st["bot_login"] != "covey[bot]" || st["shared_available"] != true {
		t.Fatalf("status %v", st)
	}
	// One agent with access: everything goes to it, signed with the shared secret.
	githubAgent(t, s, "dev")
	body := []byte(`{"action":"opened","installation":{"id":7},"repository":{"full_name":"acme/a"},
		"issue":{"number":1,"title":"x","body":"y","labels":[],"user":{"login":"anna","type":"User"}},"sender":{"login":"anna","type":"User"}}`)
	if out := postGitHubApp(t, s, body, "issues", "shared-secret"); out["agent"] != "dev" || out["routed_by"] != "only agent" {
		t.Fatalf("routed %v", out)
	}
	admin.expect(http.MethodDelete, "/api/v1/integrations/github", nil, http.StatusNoContent)
	if out := postGitHubAppStatus(t, s, body, "issues", "shared-secret"); out != http.StatusAccepted {
		t.Fatalf("after disconnect: HTTP %d", out)
	}
}

func postGitHubAppStatus(t *testing.T, s *stack, body []byte, event, secret string) int {
	t.Helper()
	resp := postGitHubAppRaw(t, s, body, event, secret)
	resp.Body.Close()
	return resp.StatusCode
}

func postGitHubApp(t *testing.T, s *stack, body []byte, event, secret string) map[string]any {
	t.Helper()
	resp := postGitHubAppRaw(t, s, body, event, secret)
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 && resp.StatusCode != http.StatusAccepted {
		t.Fatalf("webhook: HTTP %d %s", resp.StatusCode, raw)
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func postGitHubAppRaw(t *testing.T, s *stack, body []byte, event, secret string) *http.Response {
	t.Helper()
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	req, _ := http.NewRequest(http.MethodPost, s.http.URL+"/api/webhooks/github-app", strings.NewReader(string(body)))
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", fmt.Sprintf("%d", time.Now().UnixNano()))
	req.Header.Set("X-Hub-Signature-256", "sha256="+hex.EncodeToString(mac.Sum(nil)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
