package agents

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

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
}

// heartbeatKeywords are the attribute keys of a HEARTBEAT.md line.
var heartbeatKeywords = map[string]bool{
	"alle:": true, "täglich:": true, "taeglich:": true, "titel:": true, "aufgabe:": true, "nur-wenn:": true,
}

// ParseHeartbeat reads HEARTBEAT.md lines of the form
//
//   - alle: 30m      titel: Check inbox     aufgabe: Check new tickets and triage them.
//   - täglich: 09:00 titel: Daily report    aufgabe: Summarise yesterday.
//   - alle: 5m nur-wenn: email titel: Inbox aufgabe: Work through the unread mails.
//
// nur-wenn: <system> only fires the entry if the target system plugin reports
// work in the control plane's cheap pre-check (target.WorkChecker, e.g. unread
// mails) — otherwise the run is dropped without waking the agent.
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
		if len(fields) == 0 || !heartbeatKeywords[fields[0]] {
			continue
		}
		var hb Heartbeat
		for i := 0; i < len(fields); i++ {
			if !heartbeatKeywords[fields[i]] {
				continue
			}
			// Value: all tokens up to the next key (titel/aufgabe are free text).
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

// HeartbeatStatus is the monitoring view of a materialised heartbeat: schedule,
// last run, computed next run and whether a task of the last run is still open
// (in which case it does not fire again).
type HeartbeatStatus struct {
	Name         string    `json:"name"`
	Task         string    `json:"task"`
	EverySeconds *int64    `json:"every_seconds,omitempty"`
	DailyAt      *string   `json:"daily_at,omitempty"` // "HH:MM", server time
	OnlyIf       string    `json:"only_if,omitempty"`  // target system of the firing condition
	Source       string    `json:"source,omitempty"`   // "config" (HEARTBEAT.md) | "system" (platform default)
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
			to_char(h.daily_at, 'HH24:MI'), h.only_if, h.source, h.last_fired_at,
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
			&hb.OnlyIf, &hb.Source, &hb.LastFiredAt, &hb.NextRun, &hb.Pending); err != nil {
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
