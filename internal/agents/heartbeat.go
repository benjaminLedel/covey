package agents

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
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

// HeartbeatStatus ist die Monitoring-Sicht auf einen materialisierten
// Heartbeat: Zeitplan, letzter Lauf, berechneter nächster Lauf und ob gerade
// noch eine Aufgabe des letzten Laufs offen ist (dann wird nicht neu gefeuert).
type HeartbeatStatus struct {
	Name         string    `json:"name"`
	Task         string    `json:"task"`
	EverySeconds *int64    `json:"every_seconds,omitempty"`
	DailyAt      *string   `json:"daily_at,omitempty"` // "HH:MM", Serverzeit
	LastFiredAt  time.Time `json:"last_fired_at"`
	NextRun      time.Time `json:"next_run"`
	Pending      bool      `json:"pending"`
}

// Heartbeats liefert die materialisierten Heartbeats eines Agenten inklusive
// nächstem Lauf. Die Berechnung spiegelt fireHeartbeats: Intervall-Form läuft
// bei last_fired + Intervall, Tageszeit-Form heute um daily_at (falls heute
// noch nicht gefeuert), sonst morgen. Liegt next_run in der Vergangenheit,
// ist der Eintrag fällig und der nächste Tick nimmt ihn mit.
func (r *Registry) Heartbeats(ctx context.Context, agentID uuid.UUID) ([]HeartbeatStatus, error) {
	rows, err := r.pool.Query(ctx, `SELECT h.name, h.task_body, h.every_seconds,
			to_char(h.daily_at, 'HH24:MI'), h.last_fired_at,
			CASE WHEN h.every_seconds IS NOT NULL
			     THEN h.last_fired_at + make_interval(secs => h.every_seconds)
			     WHEN h.last_fired_at::date < CURRENT_DATE
			     THEN (CURRENT_DATE + h.daily_at)::timestamptz
			     ELSE (CURRENT_DATE + 1 + h.daily_at)::timestamptz END,
			EXISTS (SELECT 1 FROM backlog_tasks t
				WHERE t.agent_id=h.agent_id AND t.origin='heartbeat' AND t.title=h.name
				  AND t.state NOT IN ('done','failed','cancelled'))
		FROM agent_heartbeats h WHERE h.agent_id=$1 ORDER BY h.name`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HeartbeatStatus{}
	for rows.Next() {
		var hb HeartbeatStatus
		if err := rows.Scan(&hb.Name, &hb.Task, &hb.EverySeconds, &hb.DailyAt,
			&hb.LastFiredAt, &hb.NextRun, &hb.Pending); err != nil {
			return nil, err
		}
		out = append(out, hb)
	}
	return out, rows.Err()
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
