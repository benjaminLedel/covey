package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// loginLimiter bremst Brute-Force auf den Login: pro (Client-IP, E-Mail) sind
// nur maxFails fehlgeschlagene Versuche innerhalb von window erlaubt, danach
// wird für den Rest des Fensters mit 429 abgewiesen. In-Memory und damit
// pro-Prozess — für die Single-Binary-Deployment-Topologie ausreichend; bei
// mehreren Instanzen greift das Limit pro Instanz.
//
// Bewusst nach (IP, E-Mail) geschlüsselt, nicht nur nach IP: so sperrt ein
// falsch getipptes Passwort nicht ein ganzes Büro hinter einer NAT/Proxy aus,
// während der eigentliche Angriffsvektor — viele Versuche gegen ein Konto —
// trotzdem gedeckelt ist.
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

// webhookLimiter bremst die Webhook-Endpunkte. Die sind bewusst
// UNAUTHENTIFIZIERT erreichbar — ein Zielsystem soll ohne Covey-Konto
// zustellen können — und jeder angenommene Aufruf weckt einen Agenten, also
// einen LLM-Lauf mit echten Kosten. Wer die URL kennt (sie steht in der
// Konfiguration des Zielsystems), könnte sonst beliebig Kosten treiben.
//
// Anders als beim Login zählt hier JEDER Aufruf, nicht nur der
// fehlgeschlagene: Der teure Fall ist gerade der erfolgreiche.
//
// Der Schlüssel ist der Agent, nicht die IP: Ein Zielsystem stellt aus
// wechselnden Adressen zu (Cloud-Dienste tun das ständig), und was gedeckelt
// gehört, ist die Weckrate EINES Agenten. 60 pro Minute liegt weit über dem,
// was ein Ticketsystem im Alltag erzeugt, und weit unter dem, was wehtut.
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

// allow verbucht einen Aufruf und meldet, ob er noch im Rahmen liegt.
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
	// Gegen unbegrenztes Wachstum der Map: Ein Angreifer, der wechselnde
	// Agenten-Namen probiert, legt sonst je Name einen Eintrag an.
	if len(l.hits) > 10000 {
		for k, ts := range l.hits {
			if len(ts) == 0 || !ts[len(ts)-1].After(cutoff) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

// blocked meldet true, wenn für den Schlüssel im aktuellen Fenster bereits
// maxFails Fehlversuche liegen.
func (l *loginLimiter) blocked(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.recent(key, now)) >= l.maxFails
}

// fail verbucht einen Fehlversuch.
func (l *loginLimiter) fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.attempts[key] = append(l.recent(key, now), now)
	if len(l.attempts) > 10000 {
		l.sweep(now)
	}
}

// reset räumt den Schlüssel nach erfolgreichem Login ab.
func (l *loginLimiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, key)
}

// recent liefert die Fehlversuche im Fenster (mit Aufräumen alter). Aufrufer
// hält bereits das Lock.
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

// sweep entfernt komplett abgelaufene Schlüssel (Schutz gegen Map-Wachstum).
// Aufrufer hält bereits das Lock.
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

// clientIP extrahiert die Client-IP aus RemoteAddr (ohne Port). Hinter einem
// Reverse-Proxy ist das die Proxy-IP; X-Forwarded-For wird bewusst nicht
// vertraut (spoofbar).
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// loginKey ist der Rate-Limit-Schlüssel: Client-IP + kleingeschriebene E-Mail.
func loginKey(r *http.Request, email string) string {
	return clientIP(r) + "|" + strings.ToLower(strings.TrimSpace(email))
}
