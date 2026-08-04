package egress

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/uuid"
)

func TestAllowlistMatching(t *testing.T) {
	a := NewAllowlist([]string{"api.anthropic.com", "*.example.com", "helpdesk.local:443"})
	cases := []struct {
		host string
		want bool
	}{
		{"api.anthropic.com", true},
		{"api.anthropic.com:443", true},
		{"API.Anthropic.com", true},
		{"sub.example.com", true},
		{"example.com", true},
		{"deep.sub.example.com", true},
		{"helpdesk.local", true},
		{"evil.com", false},
		{"notexample.com", false},
		{"anthropic.com", false},
		{"", false},
	}
	for _, c := range cases {
		if got := a.Allows(c.host); got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestAllowlistEmptyFailsClosed(t *testing.T) {
	if NewAllowlist(nil).Allows("api.anthropic.com") {
		t.Error("an empty allowlist must fail closed and reject everything")
	}
}

// stubResolver accepts exactly one (agentID, token) pair and then returns a
// fixed allowlist; everything else → 407.
type stubResolver struct {
	agent uuid.UUID
	token string
	allow *Allowlist
	logs  []string
}

func (s *stubResolver) Resolve(_ context.Context, agentID, token string) (*Allowlist, uuid.UUID, bool) {
	if agentID != s.agent.String() || token != s.token {
		return nil, uuid.Nil, false
	}
	return s.allow, s.agent, true
}
func (s *stubResolver) Log(_ uuid.UUID, host, _ string, allowed bool) {
	verb := "deny"
	if allowed {
		verb = "allow"
	}
	s.logs = append(s.logs, verb+" "+host)
}

func TestNormalizePattern(t *testing.T) {
	cases := []struct {
		in, want string
		ok       bool
	}{
		{"Example.COM", "example.com", true},
		{" example.com ", "example.com", true},
		{"*.Example.com", "*.example.com", true},
		{"example.com:8080", "example.com", true},
		{"*.example.com:443", "*.example.com", true},
		{"https://example.com", "", false},
		{"example.com/path", "", false},
		{"localhost", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, err := NormalizePattern(c.in)
		if c.ok && (err != nil || got != c.want) {
			t.Errorf("NormalizePattern(%q) = %q, %v — want %q", c.in, got, err, c.want)
		}
		if !c.ok && err == nil {
			t.Errorf("NormalizePattern(%q) = %q — want an error", c.in, got)
		}
	}
}

func TestProxyPerAgentAllowDenyAndAuth(t *testing.T) {
	var gotProxyAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotProxyAuth = r.Header.Get("Proxy-Authorization")
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	upURL, _ := url.Parse(upstream.URL)

	res := &stubResolver{agent: uuid.New(), token: "s3cret", allow: NewAllowlist([]string{upURL.Hostname()})}
	p := New(res, nil)
	addr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Client with correct proxy authentication (agentID:token in the URL).
	proxyAuthed, _ := url.Parse("http://" + res.agent.String() + ":s3cret@" + addr)
	authed := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyAuthed)}}

	// Allowed: the upstream itself.
	resp, err := authed.Get(upstream.URL)
	if err != nil {
		t.Fatalf("allowed GET failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("allowed: status=%d body=%q", resp.StatusCode, body)
	}
	if gotProxyAuth != "" {
		t.Errorf("Proxy-Authorization must not reach the upstream, but arrived as: %q", gotProxyAuth)
	}

	// Denied: a different host.
	req, _ := http.NewRequest("GET", "http://blocked.example.org/", nil)
	resp2, err := authed.Do(req)
	if err != nil {
		t.Fatalf("denied GET: transport error %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("denied: want 403, got %d", resp2.StatusCode)
	}

	// Missing/wrong credentials: 407.
	proxyNoAuth, _ := url.Parse("http://" + addr)
	anon := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyNoAuth)}}
	resp3, err := anon.Get(upstream.URL)
	if err != nil {
		t.Fatalf("anon GET: transport error %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("without credentials: want 407, got %d", resp3.StatusCode)
	}

	if len(res.logs) < 2 {
		t.Errorf("want logged decisions, got %v", res.logs)
	}
}
