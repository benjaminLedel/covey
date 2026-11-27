package agents

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	// The window zone of nur-zwischen: is validated against the IANA
	// database. Embedded, so the check does not depend on the image carrying
	// /usr/share/zoneinfo.
	_ "time/tzdata"

	"github.com/google/uuid"
)

// Heartbeat is an entry from HEARTBEAT.md: a recurring task that the control
// plane puts into the agent's backlog on schedule.
// Exactly one of the two forms is set: Every (interval) or DailyAt
// (fixed time of day, server time).
type Heartbeat struct {
	Name    string        `json:"name"`     // titel: — display name, and at the same time the dedup anchor
	Task    string        `json:"task"`     // aufgabe: — task text for the backlog
	Every   time.Duration `json:"every"`    // alle: — interval (0 for the time-of-day form)
	DailyAt string        `json:"daily_at"` // täglich: — "HH:MM" (empty for the interval form)
	OnlyIf  string        `json:"only_if"`  // nur-wenn: — target system that has to report work (empty = always fire)
	// nur-zwischen: — the time of day the entry may fire in, "HH:MM-HH:MM"
	// (empty = any time). A window may wrap midnight ("18:00-08:00").
	Window string `json:"window,omitempty"`
	// The IANA zone the window is read in (empty = server time, as for
	// täglich:). A zone follows daylight saving time; server time does not.
	WindowZone string `json:"window_zone,omitempty"`
}

// heartbeatKeywords are the attribute keys of a HEARTBEAT.md line.
var heartbeatKeywords = map[string]bool{
	"alle:": true, "täglich:": true, "taeglich:": true, "titel:": true, "aufgabe:": true, "nur-wenn:": true,
	"nur-zwischen:": true,
}

type heartbeatDialect struct {
	every, daily, title, task string
}

var heartbeatDialects = []heartbeatDialect{
	{"every:", "daily:", "title:", "task:"},
	{"cada:", "diario:", "título:", "tarea:"},
	{"toutes-les:", "quotidien:", "titre:", "tâche:"},
	{"ogni:", "giornaliero:", "titolo:", "compito:"},
	{"elke:", "dagelijks:", "titel:", "taak:"},
	{"co:", "codziennie:", "tytuł:", "zadanie:"},
	{"a-cada:", "diário:", "título:", "tarefa:"},
	{"間隔:", "毎日:", "タイトル:", "タスク:"},
	{"每隔:", "每天:", "标题:", "任务:"},
}

func heartbeatColon(key string) string {
	if stem, ok := strings.CutSuffix(key, "："); ok {
		return stem + ":"
	}
	return key
}

// A translated schedule and a title opt a line into its dialect. In particular,
// words like "daily:" in legacy titles or tasks remain ordinary text.
func localizedHeartbeat(fields []string) *heartbeatDialect {
	first := heartbeatColon(fields[0])
	for i := range heartbeatDialects {
		dialect := &heartbeatDialects[i]
		if first != dialect.every && first != dialect.daily {
			continue
		}
		for _, field := range fields[1:] {
			if key := heartbeatColon(field); key == dialect.title || key == "titel:" {
				return dialect
			}
		}
	}
	return nil
}

func (d *heartbeatDialect) keyword(field string) string {
	if d != nil {
		field = heartbeatColon(field)
		switch field {
		case d.every:
			return "alle:"
		case d.daily:
			return "täglich:"
		case d.title:
			return "titel:"
		case d.task:
			return "aufgabe:"
		}
	}
	if heartbeatKeywords[field] {
		return field
	}
	return ""
}

// ParseHeartbeat reads HEARTBEAT.md lines of the form
//
//   - alle: 30m      titel: Check inbox     aufgabe: Check new tickets and triage them.
//   - täglich: 09:00 titel: Daily report    aufgabe: Summarise yesterday.
//   - alle: 5m nur-wenn: email titel: Inbox aufgabe: Work through the unread mails.
//   - alle: 5m nur-zwischen: 08:00-18:00 Europe/Berlin titel: Queue aufgabe: …
//   - daily: 07:00 title: Daily review task: Follow the review playbook.
//
// nur-wenn: <system> only fires the entry if the target system plugin reports
// work in the control plane's cheap pre-check (target.WorkChecker, e.g. unread
// mails) — otherwise the run is dropped without waking the agent.
//
// nur-zwischen: HH:MM-HH:MM [Zone] limits the time of day the entry may fire
// in. Outside the window it is not due at all — it neither fires nor advances
// last_fired_at, so the first tick inside the window fires it. Two entries with
// complementary windows give a day and a night cadence.
//
// Lines that do not start with one of the keys are prose and are ignored.
// Unlike ParseAccess the parser here is strict: a recognised line without
// titel: or without (exactly one) schedule is an error — a silent typo would
// otherwise mean that the task never runs again.
func ParseHeartbeat(content string) ([]Heartbeat, error) {
	var out []Heartbeat
	for _, raw := range strings.Split(content, "\n") {
		line := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(raw), "-"))
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		dialect := localizedHeartbeat(fields)
		if dialect.keyword(fields[0]) == "" {
			continue
		}
		var hb Heartbeat
		for i := 0; i < len(fields); {
			key := dialect.keyword(fields[i])
			if dialect != nil && (key == "alle:" || key == "täglich:" || key == "taeglich:") &&
				(hb.Every != 0 || hb.DailyAt != "") {
				return nil, fmt.Errorf("heartbeat %q: only one schedule is allowed", line)
			}
			// Value: all tokens up to the next key (titel/aufgabe are free text).
			next := i + 1
			for next < len(fields) && dialect.keyword(fields[next]) == "" {
				next++
			}
			val := fields[i+1 : next]
			i = next
			v := strings.Join(val, " ")
			switch key {
			case "alle:":
				every, err := parseEvery(v)
				if err != nil {
					return nil, fmt.Errorf("heartbeat %q: %w", line, err)
				}
				hb.Every = every
			case "täglich:", "taeglich:":
				t, err := time.Parse("15:04", v)
				if err != nil {
					return nil, fmt.Errorf("heartbeat %q: invalid time of day %q (expected HH:MM)", line, v)
				}
				hb.DailyAt = t.Format("15:04")
			case "titel:":
				hb.Name = v
			case "aufgabe:":
				hb.Task = v
			case "nur-wenn:":
				if len(val) != 1 {
					return nil, fmt.Errorf("heartbeat %q: nur-wenn: needs exactly one target system name (e.g. email)", line)
				}
				hb.OnlyIf = v
			case "nur-zwischen:":
				window, zone, err := parseWindow(val)
				if err != nil {
					return nil, fmt.Errorf("heartbeat %q: %w", line, err)
				}
				hb.Window, hb.WindowZone = window, zone
			}
		}
		if hb.Name == "" {
			return nil, fmt.Errorf("heartbeat %q: titel: missing", line)
		}
		if (hb.Every == 0) == (hb.DailyAt == "") {
			return nil, fmt.Errorf("heartbeat %q: exactly one schedule required (alle: OR täglich:)", hb.Name)
		}
		if hb.Task == "" {
			hb.Task = hb.Name
		}
		out = append(out, hb)
	}
	return out, nil
}

// HeartbeatInWindowSQL is the condition "heartbeat h may fire now" for the
// nur-zwischen: window — one expression, shared by the scheduler and the
// status view so the two cannot disagree. The window is half-open
// [start, end): 08:00-18:00 fires at 08:00 and no longer at 18:00, so two
// complementary windows never both hold. start > end wraps midnight.
const HeartbeatInWindowSQL = `(h.window_start IS NULL OR (
		CASE WHEN h.window_start < h.window_end
		     THEN heartbeat_local_time(h.window_zone) >= h.window_start
		      AND heartbeat_local_time(h.window_zone) <  h.window_end
		     ELSE heartbeat_local_time(h.window_zone) >= h.window_start
		       OR heartbeat_local_time(h.window_zone) <  h.window_end END))`

// HeartbeatStatus is the monitoring view of a materialised heartbeat: schedule,
// last run, computed next run and whether a task of the last run is still open
// (in which case it does not fire again).
type HeartbeatStatus struct {
	Name         string    `json:"name"`
	Task         string    `json:"task"`
	EverySeconds *int64    `json:"every_seconds,omitempty"`
	DailyAt      *string   `json:"daily_at,omitempty"` // "HH:MM", server time
	OnlyIf       string    `json:"only_if,omitempty"`  // target system of the firing condition
	Window       string    `json:"window,omitempty"`   // nur-zwischen: "HH:MM-HH:MM"
	WindowZone   string    `json:"window_zone,omitempty"`
	InWindow     bool      `json:"in_window"`        // true when there is no window, too
	Source       string    `json:"source,omitempty"` // "config" (HEARTBEAT.md) | "system" (platform default)
	LastFiredAt  time.Time `json:"last_fired_at"`
	NextRun      time.Time `json:"next_run"`
	Pending      bool      `json:"pending"`
}

// Heartbeats returns an agent's materialised heartbeats including the next run.
// The computation mirrors fireHeartbeats: the interval form runs at
// last_fired + interval, the time-of-day form today at daily_at (if it has not
// fired today yet), otherwise tomorrow. If next_run lies in the past, the entry
// is due and the next tick picks it up.
func (r *Registry) Heartbeats(ctx context.Context, agentID uuid.UUID) ([]HeartbeatStatus, error) {
	rows, err := r.pool.Query(ctx, `SELECT h.name, h.task_body, h.every_seconds,
			to_char(h.daily_at, 'HH24:MI'), h.only_if,
			COALESCE(to_char(h.window_start, 'HH24:MI') || '-' || to_char(h.window_end, 'HH24:MI'), ''),
			h.window_zone, `+HeartbeatInWindowSQL+`,
			h.source, h.last_fired_at,
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
			&hb.OnlyIf, &hb.Window, &hb.WindowZone, &hb.InWindow,
			&hb.Source, &hb.LastFiredAt, &hb.NextRun, &hb.Pending); err != nil {
			return nil, err
		}
		out = append(out, hb)
	}
	return out, rows.Err()
}

// Sentinel errors of the manual trigger (POST …/heartbeats/{name}/fire).
var (
	ErrHeartbeatPending = errors.New("task of the last run is still open")
	ErrAgentKilled      = errors.New("agent or fleet is stopped")
)

// FireHeartbeat fires a heartbeat manually, independent of the schedule (button
// in the UI). Semantics as in fireHeartbeats in the orchestrator: killed
// agents/fleets do not fire, and as long as the task of the last run is open it
// does not fire again. It advances last_fired_at — the schedule continues from
// now on — and returns org and task text; the backlog task is created by the
// caller (Create fires NOTIFY and wakes the agent). FOR UPDATE OF h serialises
// against the scheduler tick so that the two do not create the same run.
func (r *Registry) FireHeartbeat(ctx context.Context, agentID uuid.UUID, name string) (orgID uuid.UUID, taskBody string, err error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, "", err
	}
	defer tx.Rollback(ctx)
	var killed, pending bool
	err = tx.QueryRow(ctx, `SELECT a.org_id, h.task_body,
			a.killed OR org.fleet_killed,
			EXISTS (SELECT 1 FROM backlog_tasks t
				WHERE t.agent_id=h.agent_id AND t.origin='heartbeat' AND t.title=h.name
				  AND t.state NOT IN ('done','failed','cancelled'))
		FROM agent_heartbeats h
		JOIN agents a ON a.id=h.agent_id
		JOIN organizations org ON org.id=a.org_id
		WHERE h.agent_id=$1 AND h.name=$2
		FOR UPDATE OF h`, agentID, name).Scan(&orgID, &taskBody, &killed, &pending)
	if err != nil {
		return uuid.Nil, "", err
	}
	if killed {
		return uuid.Nil, "", ErrAgentKilled
	}
	if pending {
		return uuid.Nil, "", ErrHeartbeatPending
	}
	if _, err := tx.Exec(ctx,
		"UPDATE agent_heartbeats SET last_fired_at=now() WHERE agent_id=$1 AND name=$2",
		agentID, name); err != nil {
		return uuid.Nil, "", err
	}
	return orgID, taskBody, tx.Commit(ctx)
}

// parseWindow parses the value of nur-zwischen: — "HH:MM-HH:MM" and an
// optional IANA zone name. An empty window (start equal to end) is refused
// rather than read as "always" or "never": either reading would be a guess,
// and a heartbeat that silently never fires is the failure the strict parser
// exists to prevent.
func parseWindow(val []string) (window, zone string, err error) {
	if len(val) == 0 || len(val) > 2 {
		return "", "", fmt.Errorf("nur-zwischen: expects HH:MM-HH:MM and optionally a zone (e.g. 08:00-18:00 Europe/Berlin)")
	}
	from, to, ok := strings.Cut(val[0], "-")
	if !ok {
		return "", "", fmt.Errorf("nur-zwischen: %q is not a window (expected HH:MM-HH:MM)", val[0])
	}
	start, err1 := time.Parse("15:04", from)
	end, err2 := time.Parse("15:04", to)
	if err1 != nil || err2 != nil {
		return "", "", fmt.Errorf("nur-zwischen: %q is not a window (expected HH:MM-HH:MM)", val[0])
	}
	if start.Equal(end) {
		return "", "", fmt.Errorf("nur-zwischen: %q is empty — start and end must differ", val[0])
	}
	if len(val) == 2 {
		// The database evaluates the window (AT TIME ZONE), the parser only
		// vouches for the name. Go and Postgres both read the IANA database,
		// so a name Go knows is one Postgres knows.
		if _, err := time.LoadLocation(val[1]); err != nil || val[1] == "Local" {
			return "", "", fmt.Errorf("nur-zwischen: unknown timezone %q (expected an IANA name such as Europe/Berlin)", val[1])
		}
		zone = val[1]
	}
	return start.Format("15:04") + "-" + end.Format("15:04"), zone, nil
}

// WindowBounds splits a parsed window into its two clock times — the form the
// database stores. Both empty when the heartbeat has no window.
func (hb Heartbeat) WindowBounds() (start, end *string) {
	from, to, ok := strings.Cut(hb.Window, "-")
	if !ok {
		return nil, nil
	}
	return &from, &to
}

// parseEvery parses the interval of the alle: form. On top of Go durations
// (30m, 2h, 1h30m), "Nd" for days is allowed. A sensible lower bound is one
// minute; shorter values are meant for tests only.
func parseEvery(v string) (time.Duration, error) {
	if v == "" {
		return 0, fmt.Errorf("alle: needs an interval (e.g. 30m, 2h, 1d)")
	}
	if days, err := strconv.Atoi(strings.TrimSuffix(v, "d")); err == nil && strings.HasSuffix(v, "d") {
		if days <= 0 {
			return 0, fmt.Errorf("invalid interval %q", v)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("invalid interval %q (e.g. 30m, 2h, 1d)", v)
	}
	return d, nil
}

// WikiCleanupName is the reserved title of the platform-wide wiki cleanup
// heartbeat. At the same time the dedup anchor: if an agent defines an entry of
// the same title in HEARTBEAT.md, that one wins (the system default is skipped
// for that agent).
const WikiCleanupName = "Wiki cleanup"

// wikiCleanupTask is the task text of the cleanup heartbeat. Pure maintenance —
// the agent uses its wiki_* tools (spec/05), it does not create new content.
const wikiCleanupTask = "Curate your wiki memory — pure cleanup work, do NOT record new insights. " +
	"1) Use wiki_search to find similar or duplicate pages and merge them: transfer the content of the " +
	"weaker page into the more fitting one with wiki_write, then remove the superfluous one with wiki_delete. " +
	"2) Check the [[references]] for dead links (the target page no longer exists) and correct or " +
	"strike them. 3) Consolidate outdated or contradictory statements. If there is nothing to clean up, " +
	"close the task promptly."

// WikiCleanupHeartbeat builds the default cleanup heartbeat from a schedule
// string. Schedule forms: "HH:MM" (daily, server time) or an interval like
// "24h"/"12h"/"1d" (see parseEvery). Empty string -> enabled=false
// (feature off). If the string is set but invalid, err != nil.
func WikiCleanupHeartbeat(schedule string) (hb Heartbeat, enabled bool, err error) {
	schedule = strings.TrimSpace(schedule)
	if schedule == "" {
		return Heartbeat{}, false, nil
	}
	hb = Heartbeat{Name: WikiCleanupName, Task: wikiCleanupTask}
	if t, e := time.Parse("15:04", schedule); e == nil {
		hb.DailyAt = t.Format("15:04")
		return hb, true, nil
	}
	every, e := parseEvery(schedule)
	if e != nil {
		return Heartbeat{}, false, fmt.Errorf("COVEY_WIKI_CLEANUP %q: expected HH:MM or an interval (e.g. 24h, 1d)", schedule)
	}
	hb.Every = every
	return hb, true, nil
}
