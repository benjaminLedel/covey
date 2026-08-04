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
