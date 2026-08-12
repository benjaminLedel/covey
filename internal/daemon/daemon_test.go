package daemon

import (
	"testing"
	"time"
)

// TestCredFreshExpiresWithTTL guards the bug this replaces: a credential (or,
// before the fix, a custom secret) cached for the whole life of a warm
// sandbox's daemon connection kept serving a rotated-away value for hours,
// because nothing ever checked how old the cache entry was.
func TestCredFreshExpiresWithTTL(t *testing.T) {
	c := &Client{
		creds:        map[string]InjectCredentials{},
		credsFetched: map[string]time.Time{},
	}

	c.creds["gitlab"] = InjectCredentials{Granted: true, Token: "tok", TTLSecs: 60}
	c.credsFetched["gitlab"] = time.Now()
	if !c.credFresh("gitlab") {
		t.Fatal("a credential fetched just now, with a 60s TTL, must be fresh")
	}

	c.credsFetched["gitlab"] = time.Now().Add(-61 * time.Second)
	if c.credFresh("gitlab") {
		t.Fatal("a credential older than its own TTL must not be fresh")
	}
}

// TestCredFreshWithoutTTLNeverCaches: a credential granted without a TTL must
// not be treated as cacheable forever either — the failure mode this whole
// fix targets is exactly an indefinite cache.
func TestCredFreshWithoutTTLNeverCaches(t *testing.T) {
	c := &Client{
		creds:        map[string]InjectCredentials{"gitlab": {Granted: true, Token: "tok"}},
		credsFetched: map[string]time.Time{"gitlab": time.Now()},
	}
	if c.credFresh("gitlab") {
		t.Fatal("a credential with TTLSecs<=0 must never be reused from cache")
	}
}

// TestCredFreshUnknownSystem: nothing cached yet is, trivially, not fresh.
func TestCredFreshUnknownSystem(t *testing.T) {
	c := &Client{
		creds:        map[string]InjectCredentials{},
		credsFetched: map[string]time.Time{},
	}
	if c.credFresh("unknown") {
		t.Fatal("an uncached system must not report fresh")
	}
}
