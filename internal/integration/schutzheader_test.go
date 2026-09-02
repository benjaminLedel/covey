package integration

import (
	"net/http"
	"strings"
	"testing"
)

// The admin interface shows content agents bring in from foreign sources —
// ticket texts, mails, wiki pages, output from target systems. The protective
// headers are the second line in case any of it ever gets through as markup.
// They go missing easily and unnoticed: without them the page looks exactly the
// same as with them.
func TestSchutzHeaderDerOberflaeche(t *testing.T) {
	s := newStack(t)

	resp, err := http.Get(s.http.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("no Content-Security-Policy")
	}
	for _, pflicht := range []string{
		"default-src 'self'",
		"script-src 'self'",      // no 'unsafe-inline' for scripts
		"frame-ancestors 'none'", // no clickjacking
		"object-src 'none'",
		"base-uri 'none'",
	} {
		if !strings.Contains(csp, pflicht) {
			t.Errorf("CSP without %q: %s", pflicht, csp)
		}
	}
	// Scripts must NOT be allowed inline — that would be the very gap the whole
	// CSP stands against.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("the CSP allows inline scripts — that makes it ineffective")
	}

	for header, erwartet := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := resp.Header.Get(header); got != erwartet {
			t.Errorf("%s = %q, expected %q", header, got, erwartet)
		}
	}

	// HSTS only on an HTTPS instance: the test stack runs over HTTP, where the
	// header would be an own goal (the browser remembers https for months).
	if h := resp.Header.Get("Strict-Transport-Security"); h != "" {
		t.Errorf("the HTTP instance sets HSTS (%q) — that locks access out", h)
	}
}

// And what an HTTPS instance promises, it promises about itself.
//
// The header used to carry includeSubDomains on every HTTPS instance — a claim
// about every sibling name under a domain that is not ours, remembered by each
// visitor's browser for a year (#132). An organisation at covey.example.com
// with an internal tool on http://tools.example.com loses that tool, and
// max-age=0 only reaches browsers that come back to covey.
//
// The HTTP case above and this one belong together: one says the header must
// not be there, the other says how far it may reach when it is.
func TestHSTSReachesThisHostOnlyByDefault(t *testing.T) {
	s := newStack(t)
	// The stack runs over HTTP; CookieSecure is what tells the server it is on
	// HTTPS, and it is the same switch the header hangs off.
	s.srv.CookieSecure = true
	t.Cleanup(func() { s.srv.CookieSecure = false })

	resp, err := http.Get(s.http.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	got := resp.Header.Get("Strict-Transport-Security")
	if got != "max-age=31536000" {
		t.Errorf("HSTS = %q, expected max-age=31536000 without includeSubDomains", got)
	}

	// An operator who knows every name below the domain speaks HTTPS says so.
	s.srv.HSTS = "subdomains"
	resp2, err := http.Get(s.http.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if got := resp2.Header.Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
		t.Errorf("with subdomains: %q", got)
	}

	// And an installation whose proxy owns the header can stay quiet: nginx
	// appends rather than replaces, and a browser takes the first one.
	s.srv.HSTS = "off"
	resp3, err := http.Get(s.http.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	if got := resp3.Header.Get("Strict-Transport-Security"); got != "" {
		t.Errorf(`with "off" the binary still sets %q — the proxy cannot own the header then`, got)
	}
}
