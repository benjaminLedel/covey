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
		t.Error("leere Allowlist muss fail-closed alles abweisen")
	}
}

// stubResolver akzeptiert genau ein (agentID, token)-Paar und liefert dann eine
// feste Allowlist; alles andere → 407.
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

func TestProxyPerAgentAllowDenyAndAuth(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

	// Client mit korrekter Proxy-Authentifizierung (agentID:token in der URL).
	proxyAuthed, _ := url.Parse("http://" + res.agent.String() + ":s3cret@" + addr)
	authed := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyAuthed)}}

	// Erlaubt: der Upstream selbst.
	resp, err := authed.Get(upstream.URL)
	if err != nil {
		t.Fatalf("erlaubter GET scheiterte: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("erlaubt: status=%d body=%q", resp.StatusCode, body)
	}

	// Verweigert: anderer Host.
	req, _ := http.NewRequest("GET", "http://blocked.example.org/", nil)
	resp2, err := authed.Do(req)
	if err != nil {
		t.Fatalf("verweigerter GET: transport-fehler %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("verweigert: erwartete 403, bekam %d", resp2.StatusCode)
	}

	// Ohne/falsche Credentials: 407.
	proxyNoAuth, _ := url.Parse("http://" + addr)
	anon := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyNoAuth)}}
	resp3, err := anon.Get(upstream.URL)
	if err != nil {
		t.Fatalf("anon GET: transport-fehler %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusProxyAuthRequired {
		t.Fatalf("ohne credentials: erwartete 407, bekam %d", resp3.StatusCode)
	}

	if len(res.logs) < 2 {
		t.Errorf("erwartete geloggte Entscheidungen, bekam %v", res.logs)
	}
}
