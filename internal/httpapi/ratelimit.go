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
