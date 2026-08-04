package config

import (
	"strings"
	"testing"
)

// SecurityWarnings is what tells an operator at startup that their instance is
// insecure. The function was untested — of all things the one that is supposed
// to POINT OUT mistakes. If it fails silently nobody notices: no warning looks
// exactly like everything is fine.
func TestSecurityWarnings(t *testing.T) {
	// An operation as it should be: nothing to report.
	clean := Config{
		PublicURL:     "https://covey.example.com",
		CookieSecure:  true,
		DatabaseURL:   "postgres://covey@db/covey?sslmode=require",
		EgressEnforce: true,
	}
	if w := clean.SecurityWarnings(); len(w) != 0 {
		t.Errorf("clean configuration warns anyway: %v", w)
	}

	// And one where all four apply.
	bad := Config{
		PublicURL:     "http://covey.example.com",
		CookieSecure:  false,
		DatabaseURL:   "postgres://covey@db/covey?sslmode=disable",
		EgressEnforce: false,
	}
	w := bad.SecurityWarnings()
	if len(w) != 4 {
		t.Fatalf("expected 4 warnings, got %d: %v", len(w), w)
	}
	// Every warning has to say WHAT to do — a message without a remedy is just
	// noise.
	for _, expected := range []string{"COVEY_PUBLIC_URL", "COVEY_COOKIE_SECURE", "sslmode", "COVEY_EGRESS_ENFORCE"} {
		if !strings.Contains(strings.Join(w, "\n"), expected) {
			t.Errorf("no warning mentions %q: %v", expected, w)
		}
	}
}

// Checked one by one so that a wrongly wired condition is not masked by
// another.
func TestSecurityWarningsIndividually(t *testing.T) {
	base := Config{
		PublicURL:     "https://covey.example.com",
		CookieSecure:  true,
		DatabaseURL:   "postgres://covey@db/covey?sslmode=require",
		EgressEnforce: true,
	}
	cases := []struct {
		name     string
		modify   func(*Config)
		mentions string
	}{
		{"without HTTPS", func(c *Config) { c.PublicURL = "http://covey.example.com" }, "COVEY_PUBLIC_URL"},
		{"cookie without Secure", func(c *Config) { c.CookieSecure = false }, "COVEY_COOKIE_SECURE"},
		{"DB without TLS", func(c *Config) { c.DatabaseURL += "&sslmode=disable" }, "sslmode"},
		{"egress off", func(c *Config) { c.EgressEnforce = false }, "COVEY_EGRESS_ENFORCE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base
			tc.modify(&c)
			w := c.SecurityWarnings()
			if len(w) != 1 {
				t.Fatalf("expected exactly one warning, got %d: %v", len(w), w)
			}
			if !strings.Contains(w[0], tc.mentions) {
				t.Errorf("warning does not mention %q: %s", tc.mentions, w[0])
			}
		})
	}
}

// On localhost the check stays silent — that is development, not production.
// What matters is the flip side: a real domain must NOT pass as loopback,
// otherwise it also stays silent where it counts.
func TestLoopbackDetection(t *testing.T) {
	silent := []string{
		"http://localhost:8494", "http://127.0.0.1:8494",
		"http://[::1]:8494", "https://localhost",
	}
	for _, u := range silent {
		c := Config{PublicURL: u} // everything else insecure
		if w := c.SecurityWarnings(); len(w) != 0 {
			t.Errorf("%s is development, should stay silent: %v", u, w)
		}
	}
	loud := []string{
		"https://covey.example.com",
		"https://covey.work",
		// The classic: the hostname CONTAINS a keyword but is public. This has
		// to warn.
		"https://localhost.attacker.example.com",
	}
	for _, u := range loud {
		c := Config{PublicURL: u, CookieSecure: true,
			DatabaseURL: "postgres://x?sslmode=require", EgressEnforce: true}
		if w := c.SecurityWarnings(); len(w) != 0 {
			t.Errorf("%s is public and cleanly configured, should be silent: %v", u, w)
		}
		// … and with an insecure configuration the same URL has to warn.
		insecure := Config{PublicURL: u}
		if w := insecure.SecurityWarnings(); len(w) == 0 {
			t.Errorf("%s is public — an insecure configuration has to be reported", u)
		}
	}
}

// DataPlaneWarnings guards the address that leads the sandboxes back. It grew
// out of an outage: COVEY_PUBLIC_URL was set to the website's domain, whereupon
// every sandbox dialled back over the open network and failed at the egress
// allowlist. This test pins down both — that the warning comes when there is
// reason for suspicion, and that it stays silent where everything is right. A
// warning that always fires is soon read by nobody.
func TestDataPlaneWarnings(t *testing.T) {
	docker := func(publicURL string) Config {
		return Config{SandboxProvider: "docker", PublicURL: publicURL}
	}

	// Reachable from inside the container: loopback is rewritten to
	// host.docker.internal, host.docker.internal already is that.
	for _, ok := range []string{
		"http://localhost:8494",
		"http://127.0.0.1:8494",
		"http://host.docker.internal:8494",
	} {
		if w := docker(ok).DataPlaneWarnings(); len(w) != 0 {
			t.Fatalf("%s: unexpected warning %q", ok, w[0])
		}
	}

	// The case that shut down the data plane.
	w := docker("https://covey.example.com").DataPlaneWarnings()
	if len(w) != 1 {
		t.Fatalf("no warning for a public address: %v", w)
	}
	for _, part := range []string{"COVEY_PUBLIC_URL", "sandbox", "COVEY_SITE_URL"} {
		if !strings.Contains(w[0], part) {
			t.Fatalf("warning does not mention %q: %s", part, w[0])
		}
	}

	// Other providers rewrite nothing — there any address is conceivable, so
	// there is nothing to assert.
	foreign := Config{SandboxProvider: "e2b", PublicURL: "https://covey.example.com"}
	if w := foreign.DataPlaneWarnings(); len(w) != 0 {
		t.Fatalf("warning for a foreign provider: %q", w[0])
	}
}
