package store

import (
	"context"

	"github.com/google/uuid"

	"covey/internal/homestore"
)

// Cleanup is what one pass over an organisation's home store did — or, with
// Preview, what it would have done.
//
// FreedBytes comes from the plan, not from the sweep: the sweep counts blocks,
// and only the plan measures them. The two sets are the same except for a
// snapshot recorded while the pass was running, which is why the figure is
// what the pass set out to free rather than a receipt.
type Cleanup struct {
	Snapshots     int   `json:"snapshots"`
	BlocksRemoved int   `json:"blocks_removed"`
	FreedBytes    int64 `json:"freed_bytes"`
	Preview       bool  `json:"preview"`
}

// CleanupOrg enforces an organisation's retention rules and then sweeps every
// block no surviving manifest references any more. With preview nothing is
// deleted and the figures say what a real pass would free.
//
// The sweep runs even when retention catches no row. Blocks fall out of use by
// more than an expiring snapshot: a deleted agent takes its rows with it
// through ON DELETE CASCADE, an aborted sync leaves behind what it had already
// uploaded. A cleanup that returns early on "nothing caught" can never reclaim
// any of that, and the store grows in a way no retention setting explains.
func (s *Store) CleanupOrg(ctx context.Context, blobs homestore.BlobStore, orgID uuid.UUID, preview bool) (Cleanup, error) {
	ret, err := s.RetentionFor(ctx, orgID)
	if err != nil {
		return Cleanup{}, err
	}
	caught, err := s.SnapshotsCaughtBy(ctx, orgID, ret)
	if err != nil {
		return Cleanup{}, err
	}
	out := Cleanup{Snapshots: len(caught), Preview: preview}

	ids := make([]uuid.UUID, 0, len(caught))
	for _, snap := range caught {
		ids = append(ids, snap.ID)
	}
	surviving, err := s.ManifestsExcept(ctx, orgID, ids)
	if err != nil {
		return out, err
	}
	plan, err := homestore.Plan(ctx, blobs, orgID, surviving)
	if err != nil {
		return out, err
	}
	out.BlocksRemoved, out.FreedBytes = plan.Blocks, plan.Bytes
	if preview {
		return out, nil
	}

	if len(ids) > 0 {
		if _, err := s.ApplyRetention(ctx, orgID, ret); err != nil {
			return out, err
		}
	}
	// Read the surviving manifests again, AFTER the rows are gone. A sync that
	// recorded a snapshot while we were planning is in the database but not in
	// the list we planned with — sweeping against the older list would delete
	// the blocks it had just uploaded and leave its row pointing at nothing.
	live, err := s.Manifests(ctx, orgID)
	if err != nil {
		return out, err
	}
	res, err := homestore.Sweep(ctx, blobs, orgID, live)
	if err != nil {
		return out, err
	}
	out.BlocksRemoved = res.Removed
	return out, nil
}

// Manifests are all manifest hashes an organisation still holds rows for.
//
// Deliberately its own query rather than ManifestsExcept with an empty list:
// there, an empty slice and a nil slice mean opposite things to Postgres, and
// the nil case would report that nothing survives — which a sweep would read
// as permission to delete everything.
func (s *Store) Manifests(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT DISTINCT manifest_hash FROM home_snapshots WHERE org_id = $1`, orgID)
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

// OrgIDs lists every organisation. The passes that answer to no request — the
// periodic cleanup, the CLI without an argument — run for all of them, because
// an organisation whose admin never opens the page is exactly the one whose
// store grows unnoticed.
func (s *Store) OrgIDs(ctx context.Context) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT id FROM organizations ORDER BY created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
