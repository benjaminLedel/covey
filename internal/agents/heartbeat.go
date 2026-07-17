package agents

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Heartbeat ist ein Eintrag aus HEARTBEAT.md: eine wiederkehrende Aufgabe,
// die die Control Plane nach Zeitplan in das Backlog des Agenten legt.
// Genau eine der beiden Formen ist gesetzt: Every (Intervall) oder DailyAt
// (feste Tageszeit, Serverzeit).
type Heartbeat struct {
	Name    string        `json:"name"`     // titel: — Anzeigename, zugleich Dedup-Anker
	Task    string        `json:"task"`     // aufgabe: — Aufgabentext für das Backlog
	Every   time.Duration `json:"every"`    // alle: — Intervall (0 bei Tageszeit-Form)
	DailyAt string        `json:"daily_at"` // täglich: — "HH:MM" (leer bei Intervall-Form)
}

// heartbeatKeywords sind die Attribut-Schlüssel einer HEARTBEAT.md-Zeile.
var heartbeatKeywords = map[string]bool{
	"alle:": true, "täglich:": true, "taeglich:": true, "titel:": true, "aufgabe:": true,
}

// ParseHeartbeat liest HEARTBEAT.md-Zeilen der Form
//
//   - alle: 30m      titel: Posteingang sichten   aufgabe: Prüfe neue Tickets und triagiere sie.
//   - täglich: 09:00 titel: Tagesbericht          aufgabe: Fasse den gestrigen Tag zusammen.
//
// Zeilen, die nicht mit einem der Schlüssel beginnen, sind Prosa und werden
// ignoriert. Anders als ParseAccess ist der Parser hier streng: eine erkannte
// Zeile ohne titel: oder ohne (genau einen) Zeitplan ist ein Fehler — ein
// stiller Tippfehler hieße sonst, dass die Aufgabe nie wieder läuft.
func ParseHeartbeat(content string) ([]Heartbeat, error) {
	var out []Heartbeat
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "-"))
		fields := strings.Fields(line)
		if len(fields) == 0 || !heartbeatKeywords[fields[0]] {
			continue
		}
		var hb Heartbeat
		for i := 0; i < len(fields); i++ {
			if !heartbeatKeywords[fields[i]] {
				continue
			}
			// Wert: alle Tokens bis zum nächsten Schlüssel (titel/aufgabe sind Freitext).
			var val []string
			for j := i + 1; j < len(fields) && !heartbeatKeywords[fields[j]]; j++ {
				val = append(val, fields[j])
			}
			v := strings.Join(val, " ")
			switch fields[i] {
			case "alle:":
				every, err := parseEvery(v)
				if err != nil {
					return nil, fmt.Errorf("heartbeat %q: %w", line, err)
				}
				hb.Every = every
			case "täglich:", "taeglich:":
				t, err := time.Parse("15:04", v)
				if err != nil {
					return nil, fmt.Errorf("heartbeat %q: ungültige Tageszeit %q (erwartet HH:MM)", line, v)
				}
				hb.DailyAt = t.Format("15:04")
			case "titel:":
				hb.Name = v
			case "aufgabe:":
				hb.Task = v
			}
		}
		if hb.Name == "" {
			return nil, fmt.Errorf("heartbeat %q: titel: fehlt", line)
		}
		if (hb.Every == 0) == (hb.DailyAt == "") {
			return nil, fmt.Errorf("heartbeat %q: genau ein Zeitplan nötig (alle: ODER täglich:)", hb.Name)
		}
		if hb.Task == "" {
			hb.Task = hb.Name
		}
		out = append(out, hb)
	}
	return out, nil
}

// parseEvery parst das Intervall der alle:-Form. Zusätzlich zu Go-Dauern
// (30m, 2h, 1h30m) ist "Nd" für Tage erlaubt. Sinnvolle Untergrenze ist
// eine Minute; kürzere Werte sind nur für Tests gedacht.
func parseEvery(v string) (time.Duration, error) {
	if v == "" {
		return 0, fmt.Errorf("alle: braucht ein Intervall (z. B. 30m, 2h, 1d)")
	}
	if days, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && strings.HasSuffix(v, "d") {
		if days <= 0 {
			return 0, fmt.Errorf("ungültiges Intervall %q", v)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("ungültiges Intervall %q (z. B. 30m, 2h, 1d)", v)
	}
	return d, nil
}
