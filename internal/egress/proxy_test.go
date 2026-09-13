package egress

import (
	"bufio"
	"context"
	"encoding/base64"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

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

// CONNECT is the path everything encrypted takes, which is nearly everything a
// sandbox does: an HTTPS request reaches the proxy as CONNECT host:port, and
// what the proxy decides there it decides blind — it sees the host and nothing
// else. So the three answers have to be right at exactly that point.
func TestProxyConnectAllowsDeniesAndDemandsCredentials(t *testing.T) {
	// A plain TCP listener standing in for the far end: CONNECT does not care
	// what speaks there, only that the tunnel carries bytes both ways.
	upstream, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	go func() {
		for {
			c, err := upstream.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				buf := make([]byte, 64)
				n, err := c.Read(buf)
				if err != nil {
					return
				}
				_, _ = c.Write(append([]byte("echo:"), buf[:n]...))
			}()
		}
	}()
	upHost, upPort, _ := net.SplitHostPort(upstream.Addr().String())

	res := &stubResolver{agent: uuid.New(), token: "s3cret", allow: NewAllowlist([]string{upHost})}
	p := New(res, nil)
	addr, err := p.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	connect := func(auth, target string) (int, net.Conn) {
		t.Helper()
		conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		req := "CONNECT " + target + " HTTP/1.1\r\nHost: " + target + "\r\n"
		if auth != "" {
			req += "Proxy-Authorization: Basic " +
				base64.StdEncoding.EncodeToString([]byte(auth)) + "\r\n"
		}
		req += "\r\n"
		if _, err := conn.Write([]byte(req)); err != nil {
			t.Fatal(err)
		}
		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			conn.Close()
			return resp.StatusCode, nil
		}
		return resp.StatusCode, conn
	}

	// Allowed, and the tunnel really carries bytes: a 200 that tunnelled
	// nothing would look identical from the status line.
	code, conn := connect(res.agent.String()+":s3cret", upHost+":"+upPort)
	if code != http.StatusOK || conn == nil {
		t.Fatalf("an allowed CONNECT answered %d", code)
	}
	if _, err := conn.Write([]byte("hallo")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	n, err := conn.Read(buf)
	conn.Close()
	if err != nil {
		t.Fatalf("the tunnel carried nothing back: %v", err)
	}
	if got := string(buf[:n]); got != "echo:hallo" {
		t.Errorf("the tunnel delivered %q", got)
	}

	// A host that is not on the list: refused at the proxy, and the refusal is
	// what the sandbox sees instead of a connection.
	if code, _ := connect(res.agent.String()+":s3cret", "blocked.example.org:443"); code != http.StatusForbidden {
		t.Errorf("a host off the list answered %d, expected 403", code)
	}

	// Without credentials the proxy does not even look at the host — an
	// unauthenticated CONNECT must not be able to probe what is allowed.
	if code, _ := connect("", upHost+":"+upPort); code != http.StatusProxyAuthRequired {
		t.Errorf("CONNECT without credentials answered %d, expected 407", code)
	}
	if code, _ := connect(res.agent.String()+":falsch", upHost+":"+upPort); code != http.StatusProxyAuthRequired {
		t.Errorf("CONNECT with a wrong token answered %d, expected 407", code)
	}

	// An allowed host that nothing answers on is a gateway error, not a
	// tunnel — the sandbox learns the far end is down rather than hanging.
	res.allow = NewAllowlist([]string{upHost, "127.0.0.1"})
	if code, _ := connect(res.agent.String()+":s3cret", "127.0.0.1:1"); code != http.StatusBadGateway {
		t.Errorf("an unreachable far end answered %d, expected 502", code)
	}
}
