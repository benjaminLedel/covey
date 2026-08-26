package store

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/google/uuid"

	"covey/internal/runner"
)

// The runner's log, kept where the runner is administered.
//
// Two decisions are worth stating, because both look like over-engineering
// until the moment they matter:
//
// It is written in ONE statement per batch, not one per line. A runner at debug
// level produces hundreds of lines during a start, and a round trip each would
// make the log more expensive than the work it describes.
//
// And it is capped twice — by age and by count per runner. Age alone leaves a
// host that logs in a loop free to fill the disk within its retention window;
// count alone would keep a quiet runner's lines from three months ago and call
// that a log. Whichever bites first is the right one.

// LogLine is one line as the interface reads it.
type LogLine struct {
	ID      int64             `json:"id"`
	Time    time.Time         `json:"ts"`
	Level   string            `json:"level"`
	Msg     string            `json:"msg"`
	Attrs   map[string]string `json:"attrs,omitempty"`
	AgentID *uuid.UUID        `json:"agent_id,omitempty"`
}

// AppendLogs records a batch. A line whose runner has been deleted in the
// meantime disappears with it (ON DELETE CASCADE); a batch that arrives for one
// is not an error worth reporting to a runner that cannot act on it.
func (s *Store) AppendLogs(ctx context.Context, orgID, runnerID uuid.UUID, entries []runner.LogEntry) error {
	if len(entries) == 0 {
		return nil
	}
	ts := make([]time.Time, len(entries))
	levels := make([]string, len(entries))
	msgs := make([]string, len(entries))
	attrs := make([][]byte, len(entries))
	agents := make([]*uuid.UUID, len(entries))
	for i, e := range entries {
		ts[i] = e.Time
		if ts[i].IsZero() {
			ts[i] = time.Now()
		}
		levels[i] = e.Level
		msgs[i] = e.Msg
		if len(e.Attrs) > 0 {
			raw, err := json.Marshal(e.Attrs)
			if err == nil {
				attrs[i] = raw
			}
		}
		if e.AgentID != uuid.Nil {
			id := e.AgentID
			agents[i] = &id
		}
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO runner_logs (runner_id, org_id, ts, level, msg, attrs, agent_id)
		SELECT $1, $2, t, l, m, a, ag
		FROM unnest($3::timestamptz[], $4::text[], $5::text[], $6::jsonb[], $7::uuid[]) AS x(t, l, m, a, ag)`,
		runnerID, orgID, ts, levels, msgs, attrs, agents)
	return err
}

// LogFilter narrows what the interface asks for.
type LogFilter struct {
	// Level: "debug" delivers everything, "info" leaves the debug lines out.
	// Empty means everything — the filter is the reader's, not the store's.
	Level string
	// AgentID limits to the lines of one start.
	AgentID uuid.UUID
	// Search matches the message, case-insensitively.
	Search string
	// Limit caps the answer; 0 takes the default.
	Limit int
	// Before pages backwards: only lines with a smaller id. The id and not the
	// timestamp, because two lines can share a millisecond and a page boundary
	// that repeats or skips a line is worse than no paging.
	Before int64
}

// Logs returns a runner's lines, newest first.
func (s *Store) Logs(ctx context.Context, orgID, runnerID uuid.UUID, f LogFilter) ([]LogLine, error) {
	limit := f.Limit
	if limit <= 0 || limit > 1000 {
		limit = 200
	}
	q := `SELECT id, ts, level, msg, attrs, agent_id FROM runner_logs
		WHERE runner_id = $1 AND org_id = $2`
	args := []any{runnerID, orgID}
	if f.Level == runner.LogLevelInfo {
		q += ` AND level <> 'debug'`
	}
	if f.AgentID != uuid.Nil {
		args = append(args, f.AgentID)
		q += ` AND agent_id = $` + strconv.Itoa(len(args))
	}
	if f.Search != "" {
		args = append(args, "%"+f.Search+"%")
		q += ` AND msg ILIKE $` + strconv.Itoa(len(args))
	}
	if f.Before > 0 {
		args = append(args, f.Before)
		q += ` AND id < $` + strconv.Itoa(len(args))
	}
	args = append(args, limit)
	q += ` ORDER BY id DESC LIMIT $` + strconv.Itoa(len(args))

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LogLine{}
	for rows.Next() {
		var l LogLine
		var raw []byte
		if err := rows.Scan(&l.ID, &l.Time, &l.Level, &l.Msg, &raw, &l.AgentID); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &l.Attrs)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// CleanupLogs throws away what is older than maxAge and, beyond that, whatever
// exceeds keepPerRunner lines on a single runner. Returns how many rows went.
func (s *Store) CleanupLogs(ctx context.Context, maxAge time.Duration, keepPerRunner int) (int64, error) {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM runner_logs WHERE ts < now() - $1::interval", maxAge.String())
	if err != nil {
		return 0, err
	}
	removed := tag.RowsAffected()
	if keepPerRunner <= 0 {
		return removed, nil
	}
	// The second cap: per runner, keep the newest N. A host that logs in a
	// loop would otherwise be within its retention window and still fill the
	// disk.
	tag, err = s.pool.Exec(ctx, `DELETE FROM runner_logs WHERE id IN (
		SELECT id FROM (
			SELECT id, row_number() OVER (PARTITION BY runner_id ORDER BY id DESC) AS rn
			FROM runner_logs
		) ranked WHERE rn > $1)`, keepPerRunner)
	if err != nil {
		return removed, err
	}
	return removed + tag.RowsAffected(), nil
}

// SetLogLevel records what a runner is asked to report at.
func (s *Store) SetLogLevel(ctx context.Context, orgID, runnerID uuid.UUID, level string) error {
	_, err := s.pool.Exec(ctx,
		"UPDATE runners SET log_level = $3 WHERE id = $1 AND org_id = $2", runnerID, orgID, level)
	return err
}
