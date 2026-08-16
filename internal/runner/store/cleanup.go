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
// and only the plan measures them. The two sets are the same except for a sync
// that landed while the pass was running, which is why the figure is what the
// pass set out to free rather than a receipt.
type Cleanup struct {
	BlocksRemoved int   `json:"blocks_removed"`
	FreedBytes    int64 `json:"freed_bytes"`
	Preview       bool  `json:"preview"`
}

// CleanupOrg sweeps every block that no agent's current manifest references any
// more. With preview nothing is deleted and the figures say what a real pass
// would free.
//
// There is nothing to weigh up here and therefore nothing to configure. With
// one state per agent (spec/16) each sync replaces a manifest, and the blocks
// only that one still referenced are garbage from that moment. So are those of
// a deleted agent, whose row went with it through ON DELETE CASCADE, and those
// of a sync that broke off after uploading. All three look the same from here:
// unreferenced.
//
// Which is why this has to run on a timer rather than on a button. The garbage
// is proportional to how often agents work, not to anything anyone chose.
func (s *Store) CleanupOrg(ctx context.Context, blobs homestore.BlobStore, orgID uuid.UUID, preview bool) (Cleanup, error) {
	out := Cleanup{Preview: preview}
	live, err := s.Manifests(ctx, orgID)
	if err != nil {
		return out, err
	}
	plan, err := homestore.Plan(ctx, blobs, orgID, live)
	if err != nil {
		return out, err
	}
	out.BlocksRemoved, out.FreedBytes = plan.Blocks, plan.Bytes
	if preview {
		return out, nil
	}

	// Read the manifests again rather than sweeping against the list the plan
	// used: a sync that landed in between is in the database but not in that
	// list, and sweeping against the older one would delete the blocks it had
	// just uploaded and leave its row pointing at nothing.
	live, err = s.Manifests(ctx, orgID)
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
