package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	key := "1.2.3.4|user@example.com"

	// Up to maxFails-1 failed attempts: not yet blocked.
	for i := 0; i < l.maxFails-1; i++ {
		if l.blocked(key, now) {
			t.Fatalf("wrongly blocked after %d failed attempts", i)
		}
		l.fail(key, now)
	}
	// The maxFails-th failed attempt tips it into the blocked state.
	if l.blocked(key, now) {
		t.Fatalf("already blocked before the last failed attempt")
	}
	l.fail(key, now)
	if !l.blocked(key, now) {
		t.Fatalf("not blocked after %d failed attempts", l.maxFails)
	}

	// A successful login clears the key.
	l.reset(key)
	if l.blocked(key, now) {
		t.Fatalf("still blocked after reset")
	}

	// Old attempts drop out of the window.
	for i := 0; i < l.maxFails; i++ {
		l.fail(key, now)
	}
	if !l.blocked(key, now) {
		t.Fatalf("expected a block")
	}
	if l.blocked(key, now.Add(l.window+time.Second)) {
		t.Fatalf("block not lifted after the window expired")
	}
}

func TestLoginLimiterKeyIsolation(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	// Different emails from the same IP are capped independently, so that a
	// brute force against one account does not lock out other accounts (or a
	// whole office behind a NAT).
	for i := 0; i < l.maxFails; i++ {
		l.fail("1.2.3.4|a@example.com", now)
	}
	if !l.blocked("1.2.3.4|a@example.com", now) {
		t.Fatalf("account a should be blocked")
	}
	if l.blocked("1.2.3.4|b@example.com", now) {
		t.Fatalf("account b must not be blocked along with it")
	}
}

// The webhook limiter caps the wake rate of ONE agent. Unlike the login, every
// call counts, including the successful one — that is the expensive one.
func TestWebhookLimiter(t *testing.T) {
	l := newWebhookLimiter()
	l.maxHits, l.window = 3, time.Minute
	jetzt := time.Now()

	for i := 0; i < 3; i++ {
		if !l.allow("agent-a", jetzt) {
			t.Fatalf("call %d must get through", i+1)
		}
	}
	if l.allow("agent-a", jetzt) {
		t.Error("the fourth call within the window must be rejected")
	}

	// Another agent is untouched by it — otherwise a single overactive target
	// system would shut down the whole workforce.
	if !l.allow("agent-b", jetzt) {
		t.Error("another agent must not be blocked along with it")
	}

	// After the window it continues.
	if !l.allow("agent-a", jetzt.Add(61*time.Second)) {
		t.Error("delivery must be possible again once the window has expired")
	}
}

// The map must not grow without bound: whoever tries out changing agent names
// would otherwise create one entry per name.
func TestWebhookLimiterRaeumtAuf(t *testing.T) {
	l := newWebhookLimiter()
	l.window = time.Minute
	alt := time.Now().Add(-time.Hour)
	for i := 0; i < 10001; i++ {
		l.allow(string(rune(i%1000))+"-"+time.Duration(i).String(), alt)
	}
	l.allow("noch-einer", time.Now())
	l.mu.Lock()
	groesse := len(l.hits)
	l.mu.Unlock()
	if groesse > 10000 {
		t.Errorf("map grows without bound: %d entries", groesse)
	}
}

// clientIP decides whom a rate limit counts against — and that decision is
// what an attacker attacks first: whoever may write their own address picks
// their own bucket, and whoever hides behind a proxy shares one with everybody.
func TestClientIPHinterProxy(t *testing.T) {
	privat := func(t *testing.T) []netip.Prefix {
		t.Helper()
		var out []netip.Prefix
		for _, r := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12"} {
			out = append(out, netip.MustParsePrefix(r))
		}
		return out
	}
	anfrage := func(peer, xff string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/public/signup", nil)
		r.RemoteAddr = peer
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}

	faelle := []struct {
		name    string
		proxies []netip.Prefix
		peer    string
		xff     string
		want    string
	}{
		{
			// Without configured proxies the header stays what it is: a claim
			// by whoever sent the request.
			name: "unconfigured: header ignored",
			peer: "203.0.113.9:51000", xff: "1.2.3.4", want: "203.0.113.9",
		},
		{
			name:    "proxy in front: the client counts",
			proxies: privat(t),
			peer:    "10.0.0.2:8080", xff: "198.51.100.7", want: "198.51.100.7",
		},
		{
			// The attack: an invented entry travels along, but the proxy
			// appends the address it actually saw — and that is the rightmost.
			name:    "spoofed prefix does not help",
			proxies: privat(t),
			peer:    "10.0.0.2:8080", xff: "1.2.3.4, 198.51.100.7", want: "198.51.100.7",
		},
		{
			name:    "chain of two own proxies",
			proxies: privat(t),
			peer:    "10.0.0.2:8080", xff: "198.51.100.7, 172.16.0.9", want: "198.51.100.7",
		},
		{
			// A configured proxy that says nothing: nothing to derive, and the
			// peer is the honest answer.
			name:    "proxy without a header",
			proxies: privat(t),
			peer:    "10.0.0.2:8080", xff: "", want: "10.0.0.2",
		},
		{
			// The header comes from someone who is not a proxy of ours — then
			// it is a claim like any other.
			name:    "foreign peer with a header",
			proxies: privat(t),
			peer:    "203.0.113.9:51000", xff: "10.0.0.5", want: "203.0.113.9",
		},
		{
			// Unreadable entry: from there leftwards nothing is established.
			name:    "garbage in the chain",
			proxies: privat(t),
			peer:    "10.0.0.2:8080", xff: "198.51.100.7, kaputt", want: "10.0.0.2",
		},
		{
			// ::ffff:10.0.0.2 is 10.0.0.2 — an IPv6 socket in front of an IPv4
			// proxy must not fall out of the configured range.
			name:    "IPv4-mapped proxy",
			proxies: privat(t),
			peer:    "[::ffff:10.0.0.2]:8080", xff: "198.51.100.7", want: "198.51.100.7",
		},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			s := &Server{TrustedProxies: f.proxies}
			if got := s.clientIP(anfrage(f.peer, f.xff)); got != f.want {
				t.Fatalf("clientIP = %q, want %q", got, f.want)
			}
		})
	}
}
