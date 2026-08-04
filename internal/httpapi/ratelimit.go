package httpapi

import (
	"net"
	"net/http"
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

// clientIP extracts the client IP from RemoteAddr (without the port). Behind a
// reverse proxy that is the proxy IP; X-Forwarded-For is deliberately not
// trusted (spoofable).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginKey is the rate-limit key: client IP + lowercased email.
func loginKey(r *http.Request, email string) string {
	return clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(email))
}
