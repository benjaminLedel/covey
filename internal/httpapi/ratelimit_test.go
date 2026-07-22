package httpapi

import (
	"testing"
	"time"
)

func TestLoginLimiter(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	key := "1.2.3.4|user@example.com"

	// Bis maxFails-1 Fehlversuche: noch nicht gesperrt.
	for i := 0; i < l.maxFails-1; i++ {
		if l.blocked(key, now) {
			t.Fatalf("nach %d Fehlversuchen fälschlich gesperrt", i)
		}
		l.fail(key, now)
	}
	// Der maxFails-te Fehlversuch kippt in den gesperrten Zustand.
	if l.blocked(key, now) {
		t.Fatalf("vor dem letzten Fehlversuch bereits gesperrt")
	}
	l.fail(key, now)
	if !l.blocked(key, now) {
		t.Fatalf("nach %d Fehlversuchen nicht gesperrt", l.maxFails)
	}

	// Erfolgreicher Login räumt den Schlüssel ab.
	l.reset(key)
	if l.blocked(key, now) {
		t.Fatalf("nach reset noch gesperrt")
	}

	// Alte Versuche fallen aus dem Fenster.
	for i := 0; i < l.maxFails; i++ {
		l.fail(key, now)
	}
	if !l.blocked(key, now) {
		t.Fatalf("erwartete Sperre")
	}
	if l.blocked(key, now.Add(l.window+time.Second)) {
		t.Fatalf("Sperre nach Ablauf des Fensters nicht aufgehoben")
	}
}

func TestLoginLimiterKeyIsolation(t *testing.T) {
	l := newLoginLimiter()
	now := time.Now()
	// Verschiedene E-Mails von derselben IP sind unabhängig gedeckelt, damit
	// ein Konto-Bruteforce nicht andere Konten (bzw. ein NAT-Büro) aussperrt.
	for i := 0; i < l.maxFails; i++ {
		l.fail("1.2.3.4|a@example.com", now)
	}
	if !l.blocked("1.2.3.4|a@example.com", now) {
		t.Fatalf("Konto a sollte gesperrt sein")
	}
	if l.blocked("1.2.3.4|b@example.com", now) {
		t.Fatalf("Konto b darf nicht mitgesperrt werden")
	}
}
