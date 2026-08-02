package integration

import (
	"net/http"
	"strings"
	"testing"
)

// Die Admin-Oberfläche zeigt Inhalte, die Agenten aus fremden Quellen
// mitbringen — Ticket-Texte, Mails, Wiki-Seiten, Ausgaben von Zielsystemen.
// Die Schutz-Header sind die zweite Linie, falls davon je etwas als Markup
// durchschlägt. Sie fehlen leicht unbemerkt: Ohne sie sieht die Seite genauso
// aus wie mit.
func TestSchutzHeaderDerOberflaeche(t *testing.T) {
	s := newStack(t)

	resp, err := http.Get(s.http.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	csp := resp.Header.Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("keine Content-Security-Policy")
	}
	for _, pflicht := range []string{
		"default-src 'self'",
		"script-src 'self'",      // kein 'unsafe-inline' für Skripte
		"frame-ancestors 'none'", // kein Clickjacking
		"object-src 'none'",
		"base-uri 'none'",
	} {
		if !strings.Contains(csp, pflicht) {
			t.Errorf("CSP ohne %q: %s", pflicht, csp)
		}
	}
	// Skripte dürfen NICHT inline erlaubt sein — das wäre die Lücke, gegen die
	// die ganze CSP steht.
	if strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Error("CSP erlaubt Inline-Skripte — damit ist sie wirkungslos")
	}

	for header, erwartet := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "no-referrer",
	} {
		if got := resp.Header.Get(header); got != erwartet {
			t.Errorf("%s = %q, erwartet %q", header, got, erwartet)
		}
	}

	// HSTS nur bei einer HTTPS-Instanz: Der Test-Stack läuft über HTTP, hier
	// wäre der Header ein Eigentor (Browser merkt sich https für Monate).
	if h := resp.Header.Get("Strict-Transport-Security"); h != "" {
		t.Errorf("HTTP-Instanz setzt HSTS (%q) — das sperrt den Zugang aus", h)
	}
}
