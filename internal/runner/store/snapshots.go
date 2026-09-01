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
//
// It replaces the agent's state rather than appending to a history: there is
// exactly one row per agent, and the database holds that promise (spec/16).
// The blocks the previous manifest alone referenced become garbage at this
// moment — which is why the sweep is not an occasional tidy-up here but the
// thing that keeps the store from growing with every single job.
func (s *Store) RecordSnapshotTimed(ctx context.Context, orgID, agentID uuid.UUID, runnerID *uuid.UUID,
	manifestHash string, totalSize int64, blocksUp int, bytesUp int64, durationMS int, reason string) (Snapshot, error) {
	if reason == "" {
		reason = "job"
	}
	snap, err := scanSnapshot(s.pool.QueryRow(ctx, `
		INSERT INTO home_snapshots (id, org_id, agent_id, runner_id, manifest_hash, total_size, blocks_up, bytes_up, duration_ms, reason)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (agent_id) DO UPDATE SET
			runner_id = EXCLUDED.runner_id, manifest_hash = EXCLUDED.manifest_hash,
			total_size = EXCLUDED.total_size, blocks_up = EXCLUDED.blocks_up,
			bytes_up = EXCLUDED.bytes_up, duration_ms = EXCLUDED.duration_ms,
			reason = EXCLUDED.reason, created_at = now()
		RETURNING `+snapshotCols,
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

// SnapshotChain are the states an agent's home can be materialised from,
// newest first — the one a wake uses and the ones it may fall back to.
//
// It exists because "the newest snapshot" and "a snapshot that can be read"
// are not the same thing. A block the store lost takes its snapshot with it,
// and until #138 that ended the agent: the control plane offered the same
// unreadable state every thirty seconds, for ever, while nine readable ones
// sat in the same table (covey.work, 780 wakes over six and a half hours).
//
// The limit is the retention's, not a number of its own: whatever the
// organisation keeps is what there is to fall back to.
func (s *Store) SnapshotChain(ctx context.Context, agentID uuid.UUID, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx,
		`SELECT manifest_hash FROM home_snapshots WHERE agent_id = $1
		 ORDER BY created_at DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	seen := map[string]bool{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		// Two syncs that changed nothing carry the same hash. As a fallback the
		// second one is not a second chance.
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out, rows.Err()
}

// HomeSummary is what the agent page shows about a home. The interesting figure
// is not the size but the difference: a 7 GB home, of which perhaps 200 MB only
// this agent holds (spec/16, "Interface").
type HomeSummary struct {
	// Latest is the one state there is — what the next wake materialises.
	// Absent means the agent has never synced, not that a history ran out.
	Latest *Snapshot `json:"latest,omitempty"`
	// RunnerName/RunnerKind describe where the working copy sits warm.
	RunnerName string `json:"runner_name,omitempty"`
	RunnerKind string `json:"runner_kind,omitempty"`
}

// HomeSummaryFor collects it. Deliberately without the block figures: those
// need the store, and this is the part the database can answer.
func (s *Store) HomeSummaryFor(ctx context.Context, agentID uuid.UUID) (HomeSummary, error) {
	var out HomeSummary
	latest, err := s.LatestSnapshot(ctx, agentID)
	if err != nil {
		return out, err
	}
	if latest.ManifestHash == "" {
		return out, nil
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
