package httpapi

import (
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
