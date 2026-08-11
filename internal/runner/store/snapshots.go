package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Snapshot is one state of an agent's home. The snapshot IS its manifest hash;
// everything else here is for the interface.
type Snapshot struct {
	ID           uuid.UUID  `json:"id"`
	AgentID      uuid.UUID  `json:"agent_id"`
	RunnerID     *uuid.UUID `json:"runner_id,omitempty"`
	ManifestHash string     `json:"manifest_hash"`
	TotalSize    int64      `json:"total_size"`
	BlocksUp     int        `json:"blocks_up"`
	BytesUp      int64      `json:"bytes_up"`
	// DurationMS is how long the sync ran — the one question anybody has about
	// the sleep path: whether it is expensive.
	DurationMS int       `json:"duration_ms"`
	Reason     string    `json:"reason"`
	CreatedAt  time.Time `json:"created_at"`
}

const snapshotCols = `id, agent_id, runner_id, manifest_hash, total_size, blocks_up, bytes_up, duration_ms, reason, created_at`

func scanSnapshot(row pgx.Row) (Snapshot, error) {
	var s Snapshot
	err := row.Scan(&s.ID, &s.AgentID, &s.RunnerID, &s.ManifestHash, &s.TotalSize,
		&s.BlocksUp, &s.BytesUp, &s.DurationMS, &s.Reason, &s.CreatedAt)
	return s, err
}

// RecordSnapshot files a completed sync. Only afterwards may anything be
// cleaned up locally — the hard rule of the whole construction is: no prune
// before a successful sync (spec/16).
func (s *Store) RecordSnapshot(ctx context.Context, orgID, agentID uuid.UUID, runnerID *uuid.UUID,
	manifestHash string, totalSize int64, blocksUp int, bytesUp int64, reason string) (Snapshot, error) {
	return s.RecordSnapshotTimed(ctx, orgID, agentID, runnerID, manifestHash, totalSize, blocksUp, bytesUp, 0, reason)
}

// RecordSnapshotTimed files a completed sync together with what it cost.
func (s *Store) RecordSnapshotTimed(ctx context.Context, orgID, agentID uuid.UUID, runnerID *uuid.UUID,
	manifestHash string, totalSize int64, blocksUp int, bytesUp int64, durationMS int, reason string) (Snapshot, error) {
	if reason == "" {
		reason = "job"
	}
	snap, err := scanSnapshot(s.pool.QueryRow(ctx, `
		INSERT INTO home_snapshots (id, org_id, agent_id, runner_id, manifest_hash, total_size, blocks_up, bytes_up, duration_ms, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING `+snapshotCols,
		uuid.New(), orgID, agentID, runnerID, manifestHash, totalSize, blocksUp, bytesUp, durationMS, reason))
	if err != nil {
		return Snapshot{}, err
	}
	// The scheduler's preference: this is where the working copy sits warm.
	// Deliberately a hint and not a promise — an agent whose preferred runner
	// is missing wakes up on a different one.
	if runnerID != nil {
		_, _ = s.pool.Exec(ctx, `UPDATE agents SET last_runner_id = $2 WHERE id = $1`, agentID, *runnerID)
	}
	return snap, nil
}

// LatestSnapshot is the state an agent's home is materialised from on wake.
// Empty manifest hash = the agent has never had a snapshot; its working copy
// is then whatever is on the runner, which is the case on the very first wake.
func (s *Store) LatestSnapshot(ctx context.Context, agentID uuid.UUID) (Snapshot, error) {
	snap, err := scanSnapshot(s.pool.QueryRow(ctx,
		`SELECT `+snapshotCols+` FROM home_snapshots WHERE agent_id = $1 ORDER BY created_at DESC LIMIT 1`, agentID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, nil
	}
	return snap, err
}

// ListSnapshots returns an agent's snapshots, newest first.
func (s *Store) ListSnapshots(ctx context.Context, agentID uuid.UUID, limit int) ([]Snapshot, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx,
		`SELECT `+snapshotCols+` FROM home_snapshots WHERE agent_id = $1 ORDER BY created_at DESC LIMIT $2`,
		agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// Retention is the org-wide rule for how many snapshots survive.
type Retention struct {
	// KeepPerAgent: the last N per agent. 0 = no limit by count.
	KeepPerAgent int
	// MaxAge: remove snapshots older than this. 0 = no limit by age.
	MaxAge time.Duration
}

// ApplyRetention removes the snapshot ROWS the rules catch and returns how
// many went. It frees no space by itself: a block belongs to no single
// snapshot, so the sweep over the surviving manifests decides that
// (homestore.Sweep).
//
// Always kept: every agent's most recent snapshot, even when both rules would
// catch it. A retention that takes an agent's last home away is a delete
// command by a detour.
func (s *Store) ApplyRetention(ctx context.Context, orgID uuid.UUID, r Retention) (int64, error) {
	keep := r.KeepPerAgent
	if keep < 1 {
		keep = 1 // the most recent one always survives
	}
	var maxAge any
	if r.MaxAge > 0 {
		maxAge = time.Now().Add(-r.MaxAge)
	}
	tag, err := s.pool.Exec(ctx, `
		WITH ranked AS (
			SELECT id, created_at,
			       row_number() OVER (PARTITION BY agent_id ORDER BY created_at DESC) AS rang
			  FROM home_snapshots WHERE org_id = $1
		)
		DELETE FROM home_snapshots WHERE id IN (
			SELECT id FROM ranked
			 WHERE rang > 1
			   AND (rang > $2 OR ($3::timestamptz IS NOT NULL AND created_at < $3::timestamptz))
		)`, orgID, keep, maxAge)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// HomeSummary is what the agent page shows about a home. The interesting figure
// is not the size but the difference: a 7 GB home, of which perhaps 200 MB only
// this agent holds (spec/16, "Interface").
type HomeSummary struct {
	// Snapshots and the span they cover — what the retention currently leaves.
	Snapshots int        `json:"snapshots"`
	Oldest    *time.Time `json:"oldest,omitempty"`
	// Latest is the state the next wake materialises.
	Latest *Snapshot `json:"latest,omitempty"`
	// RunnerName/RunnerKind describe where the working copy sits warm.
	RunnerName string `json:"runner_name,omitempty"`
	RunnerKind string `json:"runner_kind,omitempty"`
}

// HomeSummaryFor collects it. Deliberately without the block figures: those
// need the store, and this is the part the database can answer.
func (s *Store) HomeSummaryFor(ctx context.Context, agentID uuid.UUID) (HomeSummary, error) {
	var out HomeSummary
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), min(created_at) FROM home_snapshots WHERE agent_id = $1`, agentID).
		Scan(&out.Snapshots, &out.Oldest)
	if err != nil {
		return out, err
	}
	if out.Snapshots == 0 {
		return out, nil
	}
	latest, err := s.LatestSnapshot(ctx, agentID)
	if err != nil {
		return out, err
	}
	out.Latest = &latest
	if latest.RunnerID != nil {
		var kind, name string
		if err := s.pool.QueryRow(ctx,
			`SELECT kind, name FROM runners WHERE id = $1`, *latest.RunnerID).Scan(&kind, &name); err == nil {
			out.RunnerKind, out.RunnerName = kind, name
		}
	}
	return out, nil
}

// GetSnapshot reads one snapshot of an organisation.
func (s *Store) GetSnapshot(ctx context.Context, orgID, id uuid.UUID) (Snapshot, error) {
	snap, err := scanSnapshot(s.pool.QueryRow(ctx,
		`SELECT `+snapshotCols+` FROM home_snapshots WHERE id = $1 AND org_id = $2`, id, orgID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Snapshot{}, ErrNotFound
	}
	return snap, err
}

// Retention rules of an organisation.
func (s *Store) RetentionFor(ctx context.Context, orgID uuid.UUID) (Retention, error) {
	var keep, days int
	err := s.pool.QueryRow(ctx,
		`SELECT home_retention_keep, home_retention_days FROM organizations WHERE id = $1`, orgID).
		Scan(&keep, &days)
	return Retention{KeepPerAgent: keep, MaxAge: time.Duration(days) * 24 * time.Hour}, err
}

// SetRetention writes them.
func (s *Store) SetRetention(ctx context.Context, orgID uuid.UUID, keep, days int) error {
	if keep < 0 {
		keep = 0
	}
	if days < 0 {
		days = 0
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE organizations SET home_retention_keep = $2, home_retention_days = $3 WHERE id = $1`,
		orgID, keep, days)
	return err
}

// SnapshotsCaughtBy names the snapshots the rules would remove — the basis of
// the preview. Nothing is deleted; the same statement as ApplyRetention, read
// instead of executed.
func (s *Store) SnapshotsCaughtBy(ctx context.Context, orgID uuid.UUID, r Retention) ([]Snapshot, error) {
	keep := r.KeepPerAgent
	if keep < 1 {
		keep = 1
	}
	var maxAge any
	if r.MaxAge > 0 {
		maxAge = time.Now().Add(-r.MaxAge)
	}
	rows, err := s.pool.Query(ctx, `
		WITH ranked AS (
			SELECT `+snapshotCols+`, org_id,
			       row_number() OVER (PARTITION BY agent_id ORDER BY created_at DESC) AS rang
			  FROM home_snapshots WHERE org_id = $1
		)
		SELECT `+snapshotCols+` FROM ranked
		 WHERE rang > 1
		   AND (rang > $2 OR ($3::timestamptz IS NOT NULL AND created_at < $3::timestamptz))
		 ORDER BY created_at`, orgID, keep, maxAge)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Snapshot
	for rows.Next() {
		snap, err := scanSnapshot(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, snap)
	}
	return out, rows.Err()
}

// ManifestsExcept are the manifest hashes of an organisation that survive when
// the given snapshots go — what the sweep has to keep.
func (s *Store) ManifestsExcept(ctx context.Context, orgID uuid.UUID, removing []uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT manifest_hash FROM home_snapshots
		  WHERE org_id = $1 AND NOT (id = ANY($2::uuid[]))`, orgID, removing)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
