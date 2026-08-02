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

// Der Webhook-Limiter deckelt die Weckrate EINES Agenten. Anders als beim
// Login zählt jeder Aufruf, auch der erfolgreiche — der ist ja der teure.
func TestWebhookLimiter(t *testing.T) {
	l := newWebhookLimiter()
	l.maxHits, l.window = 3, time.Minute
	jetzt := time.Now()

	for i := 0; i < 3; i++ {
		if !l.allow("agent-a", jetzt) {
			t.Fatalf("Aufruf %d muss durchgehen", i+1)
		}
	}
	if l.allow("agent-a", jetzt) {
		t.Error("der vierte Aufruf im Fenster muss abgewiesen werden")
	}

	// Ein anderer Agent ist davon unberührt — sonst legte ein einziges
	// überaktives Zielsystem die ganze Belegschaft still.
	if !l.allow("agent-b", jetzt) {
		t.Error("ein anderer Agent darf nicht mitgesperrt werden")
	}

	// Nach dem Fenster geht es weiter.
	if !l.allow("agent-a", jetzt.Add(61*time.Second)) {
		t.Error("nach Ablauf des Fensters muss wieder zugestellt werden können")
	}
}

// Die Map darf nicht unbegrenzt wachsen: Wer wechselnde Agenten-Namen
// durchprobiert, legte sonst je Name einen Eintrag an.
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
		t.Errorf("Map wächst unbegrenzt: %d Einträge", groesse)
	}
}
