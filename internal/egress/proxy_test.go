package egress

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
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
		{"example.com", true}, // Apex mit erlaubt
		{"deep.sub.example.com", true},
		{"helpdesk.local", true},
		{"evil.com", false},
		{"notexample.com", false},
		{"anthropic.com", false}, // kein Suffix-Match auf exaktem Eintrag
		{"", false},
	}
	for _, c := range cases {
		if got := a.Allows(c.host); got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.host, got, c.want)
		}
	}
}

func TestAllowlistEmptyFailsClosed(t *testing.T) {
	a := NewAllowlist(nil)
	if !a.Empty() {
		t.Fatal("erwartete leere Allowlist")
	}
	if a.Allows("api.anthropic.com") {
		t.Error("leere Allowlist muss fail-closed alles abweisen")
	}
}

// TestProxyHTTPAllowAndDeny fährt den Proxy gegen einen echten Upstream und
// prüft, dass nur erlaubte Hosts durchkommen (Klartext-HTTP-Pfad).
func TestProxyHTTPAllowAndDeny(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()

	upURL, _ := url.Parse(upstream.URL)

	// Allowlist enthält genau den Upstream-Host.
	p := New(NewAllowlist([]string{upURL.Hostname()}), nil)
	addr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	proxyURL, _ := url.Parse("http://" + addr)
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}

	// Erlaubt: der Upstream selbst.
	resp, err := client.Get(upstream.URL)
	if err != nil {
		t.Fatalf("erlaubter GET scheiterte: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || string(body) != "ok" {
		t.Fatalf("erlaubt: status=%d body=%q", resp.StatusCode, body)
	}

	// Verweigert: ein anderer Host (über denselben Proxy geroutet).
	req, _ := http.NewRequest("GET", "http://blocked.example.org/", nil)
	resp2, err := client.Do(req)
	if err != nil {
		t.Fatalf("verweigerter GET: transport-fehler %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("verweigert: erwartete 403, bekam %d", resp2.StatusCode)
	}
}
