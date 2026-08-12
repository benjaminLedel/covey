package httpapi

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// loginLimiter throttles brute force against the login: per (client IP, email)
// only maxFails failed attempts within window are allowed, after that the rest
// of the window is rejected with a 429. In-memory and therefore per process —
// sufficient for the single-binary deployment topology; with several instances
// the limit applies per instance.
//
// Deliberately keyed by (IP, email), not by IP alone: that way a mistyped
// password does not lock out a whole office behind a NAT/proxy, while the actual
// attack vector — many attempts against one account — is still capped.
type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
	maxFails int
	window   time.Duration
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		attempts: make(map[string][]time.Time),
		maxFails: 5,
		window:   15 * time.Minute,
	}
}

// webhookLimiter throttles the webhook endpoints. Those are deliberately
// reachable UNAUTHENTICATED — a target system should be able to deliver without
// a Covey account — and every accepted call wakes an agent, i.e. an LLM run with
// real cost. Whoever knows the URL (it sits in the target system's
// configuration) could otherwise run up arbitrary cost.
//
// Unlike the login, EVERY call counts here, not just the failed one: the
// expensive case is precisely the successful one.
//
// The key is the agent, not the IP: a target system delivers from changing
// addresses (cloud services do that constantly), and what belongs capped is the
// wake rate of ONE agent. 60 per minute is far above what a ticket system
// produces day to day, and far below what hurts.
type webhookLimiter struct {
	mu      sync.Mutex
	hits    map[string][]time.Time
	maxHits int
	window  time.Duration
}

func newWebhookLimiter() *webhookLimiter {
	return &webhookLimiter{
		hits:    make(map[string][]time.Time),
		maxHits: 60,
		window:  time.Minute,
	}
}

// newSignupLimiter throttles the sign-up. Same mechanism as for the webhooks,
// different numbers and a different reason: what is being throttled here is
// GUESSING — every attempt, not just the failed one, because an attacker who
// hits a valid code on the twentieth try has succeeded, not failed.
//
// The key is the client address. Ten attempts an hour is far above what a
// person needs who is typing a code off a screen, and far below what makes a
// 50-bit code worth attacking.
func newSignupLimiter() *webhookLimiter {
	return &webhookLimiter{
		hits:    make(map[string][]time.Time),
		maxHits: 10,
		window:  time.Hour,
	}
}

// allow books a call and reports whether it is still within bounds.
func (l *webhookLimiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.maxHits {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	// Against unbounded growth of the map: an attacker trying changing agent
	// names would otherwise create one entry per name.
	if len(l.hits) > 10000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// blocked reports true if maxFails failed attempts already sit on the key
// within the current window.
func (l *loginLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= l.maxFails
}

// fail books a failed attempt.
func (l *loginLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[key] = append(l.recent(key, now), now)
	if len(l.attempts) > 10000 {
		l.sweep(now)
	}
}

// reset clears the key after a successful login.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// recent returns the failed attempts within the window (cleaning out old ones).
// The caller already holds the lock.
func (l *loginLimiter) recent(key string, now time.Time) []time.Time {
	cutoff := now.Add(-l.window)
	kept := l.attempts[key][:0]
	for _, t := range l.attempts[key] {
		if t.After(cutoff) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.attempts, key)
		return nil
	}
	l.attempts[key] = kept
	return kept
}

// sweep removes keys that have expired entirely (protection against map
// growth). The caller already holds the lock.
func (l *loginLimiter) sweep(now time.Time) {
	cutoff := now.Add(-l.window)
	for k, ts := range l.attempts {
		fresh := false
		for _, t := range ts {
			if t.After(cutoff) {
				fresh = true
				break
			}
		}
		if !fresh {
			delete(l.attempts, k)
		}
	}
}

// clientIP is who a rate limit counts against.
//
// Without configured proxies it is the peer address, and X-Forwarded-For is
// ignored: the header is written by whoever sends the request, so an instance
// reachable directly would let every attacker pick their own bucket.
//
// Behind a reverse proxy — the setup docs/ops-deployment.md recommends for
// production — the peer address is the PROXY, the same one for everybody.
// Every limit keyed on it then becomes one shared bucket: ten sign-up attempts
// an hour for the whole installation, and the eleventh guest at the conference
// the codes were handed out for gets a 429. COVEY_TRUSTED_PROXIES therefore
// names the addresses whose X-Forwarded-For may be believed.
//
// Read from the RIGHT: the proxy appends the peer it saw, so the rightmost
// entry is the only one written by something we trust. Everything to the left
// of it comes from the request and may be invented — an attacker sending
// "X-Forwarded-For: 1.2.3.4" produces "1.2.3.4, <their real address>" after
// the proxy has appended, and the first untrusted address from the right is
// still theirs. Chained proxies are skipped as long as they are trusted too.
func (s *Server) clientIP(r *http.Request) string {
	peer := peerIP(r)
	if len(s.TrustedProxies) == 0 {
		return peer
	}
	addr, err := netip.ParseAddr(peer)
	if err != nil || !s.trustedProxy(addr) {
		return peer
	}
	fields := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(fields) - 1; i >= 0; i-- {
		hop, ok := parseForwarded(fields[i])
		if !ok {
			// Unreadable entry: from here leftwards nothing can be
			// established any more. The peer is the honest answer — it is what
			// we would have used without the header at all.
			return peer
		}
		if s.trustedProxy(hop) {
			continue // one more proxy of our own in the chain
		}
		return hop.String()
	}
	return peer
}

// trustedProxy reports whether an address is one of the configured proxies.
func (s *Server) trustedProxy(addr netip.Addr) bool {
	addr = addr.Unmap() // ::ffff:10.0.0.1 is 10.0.0.1
	for _, p := range s.TrustedProxies {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

// peerIP is the address of the direct connection, without the port.
func peerIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// parseForwarded reads one X-Forwarded-For entry. Bare addresses are the rule;
// some proxies write "ip:port", which is accepted as well.
func parseForwarded(field string) (netip.Addr, bool) {
	field = strings.TrimSpace(field)
	if field == "" {
		return netip.Addr{}, false
	}
	if addr, err := netip.ParseAddr(field); err == nil {
		return addr.Unmap(), true
	}
	if host, _, err := net.SplitHostPort(field); err == nil {
		if addr, err := netip.ParseAddr(host); err == nil {
			return addr.Unmap(), true
		}
	}
	return netip.Addr{}, false
}

// loginKey is the rate-limit key: client IP + lowercased email.
func (s *Server) loginKey(r *http.Request, email string) string {
	return s.clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(email))
}
