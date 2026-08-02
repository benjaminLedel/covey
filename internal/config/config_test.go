package config

import (
	"strings"
	"testing"
)

// SecurityWarnings ist das, was einem Betreiber beim Start sagt, dass seine
// Instanz unsicher steht. Die Funktion war ungetestet — ausgerechnet die, die
// auf Fehler HINWEISEN soll. Fällt sie still aus, merkt es niemand: Keine
// Warnung sieht genauso aus wie alles in Ordnung.
func TestSecurityWarnings(t *testing.T) {
	// Ein Betrieb, wie er sein soll: nichts zu melden.
	sauber := Config{
		PublicURL:     "https://covey.example.com",
		CookieSecure:  true,
		DatabaseURL:   "postgres://covey@db/covey?sslmode=require",
		EgressEnforce: true,
	}
	if w := sauber.SecurityWarnings(); len(w) != 0 {
		t.Errorf("saubere Konfiguration warnt trotzdem: %v", w)
	}

	// Und einer, bei dem alles vier zutrifft.
	schlecht := Config{
		PublicURL:     "http://covey.example.com",
		CookieSecure:  false,
		DatabaseURL:   "postgres://covey@db/covey?sslmode=disable",
		EgressEnforce: false,
	}
	w := schlecht.SecurityWarnings()
	if len(w) != 4 {
		t.Fatalf("erwartet 4 Warnungen, got %d: %v", len(w), w)
	}
	// Jede Warnung muss sagen, WAS zu tun ist — eine Meldung ohne Handgriff
	// ist nur Rauschen.
	for _, erwartet := range []string{"COVEY_PUBLIC_URL", "COVEY_COOKIE_SECURE", "sslmode", "COVEY_EGRESS_ENFORCE"} {
		if !strings.Contains(strings.Join(w, "\n"), erwartet) {
			t.Errorf("keine Warnung nennt %q: %v", erwartet, w)
		}
	}
}

// Einzeln geprüft, damit eine falsch verknüpfte Bedingung nicht von einer
// anderen verdeckt wird.
func TestSecurityWarningsEinzeln(t *testing.T) {
	basis := Config{
		PublicURL:     "https://covey.example.com",
		CookieSecure:  true,
		DatabaseURL:   "postgres://covey@db/covey?sslmode=require",
		EgressEnforce: true,
	}
	fälle := []struct {
		name   string
		ändern func(*Config)
		nennt  string
	}{
		{"ohne HTTPS", func(c *Config) { c.PublicURL = "http://covey.example.com" }, "COVEY_PUBLIC_URL"},
		{"Cookie ohne Secure", func(c *Config) { c.CookieSecure = false }, "COVEY_COOKIE_SECURE"},
		{"DB ohne TLS", func(c *Config) { c.DatabaseURL += "&sslmode=disable" }, "sslmode"},
		{"Egress aus", func(c *Config) { c.EgressEnforce = false }, "COVEY_EGRESS_ENFORCE"},
	}
	for _, f := range fälle {
		t.Run(f.name, func(t *testing.T) {
			c := basis
			f.ändern(&c)
			w := c.SecurityWarnings()
			if len(w) != 1 {
				t.Fatalf("erwartet genau eine Warnung, got %d: %v", len(w), w)
			}
			if !strings.Contains(w[0], f.nennt) {
				t.Errorf("Warnung nennt %q nicht: %s", f.nennt, w[0])
			}
		})
	}
}

// Auf localhost schweigt die Prüfung — das ist Entwicklung, kein Betrieb.
// Wichtig ist die Kehrseite: Eine echte Domain darf NICHT als Loopback
// durchgehen, sonst schweigt sie auch dort, wo es zählt.
func TestLoopbackErkennung(t *testing.T) {
	still := []string{
		"http://localhost:8494", "http://127.0.0.1:8494",
		"http://[::1]:8494", "https://localhost",
	}
	for _, u := range still {
		c := Config{PublicURL: u} // sonst alles unsicher
		if w := c.SecurityWarnings(); len(w) != 0 {
			t.Errorf("%s ist Entwicklung, sollte still bleiben: %v", u, w)
		}
	}
	laut := []string{
		"https://covey.example.com",
		"https://covey.work",
		// Der Klassiker: Der Hostname ENTHÄLT ein Schlüsselwort, ist aber
		// öffentlich. Hier muss gewarnt werden.
		"https://localhost.angreifer.example.com",
	}
	for _, u := range laut {
		c := Config{PublicURL: u, CookieSecure: true,
			DatabaseURL: "postgres://x?sslmode=require", EgressEnforce: true}
		if w := c.SecurityWarnings(); len(w) != 0 {
			t.Errorf("%s ist öffentlich und sauber konfiguriert, sollte still sein: %v", u, w)
		}
		// … und bei unsicherer Konfiguration muss dieselbe URL warnen.
		unsicher := Config{PublicURL: u}
		if w := unsicher.SecurityWarnings(); len(w) == 0 {
			t.Errorf("%s ist öffentlich — unsichere Konfiguration muss gemeldet werden", u)
		}
	}
}
