package homestore

import (
	"context"

	"github.com/google/uuid"
)

// SweepResult is what a cleanup freed.
type SweepResult struct {
	Removed int
	Kept    int
}

// Sweep removes every block of an organisation that no surviving snapshot
// references. live are the manifest hashes that remain.
//
// Mark-and-sweep rather than reference counting, on purpose: a counter has to
// be right at every write, and one that drifts either loses data or keeps
// rubbish forever, silently in both directions. Walking the surviving
// manifests is slower and cannot be wrong.
//
// It is also why "delete this snapshot" does not free space linearly: a block
// belongs to no single snapshot. A preview therefore has to name the space
// actually freed and not the sum of the snapshot sizes — anything else is a
// number that is never right (spec/16, "Retention").
func Sweep(ctx context.Context, blobs BlobStore, orgID uuid.UUID, live []string) (SweepResult, error) {
	keep := map[string]bool{}
	for _, manifestHash := range live {
		// The manifest itself has to survive — without it the snapshot is a
		// hash pointing at nothing.
		keep[manifestHash] = true
		m, err := Load(ctx, blobs, orgID, manifestHash)
		if err != nil {
			// A manifest that cannot be read is the one case where deleting
			// would be unforgivable: we do not know what it references, so
			// everything stays and the cleanup reports the failure instead.
			return SweepResult{}, err
		}
		for block := range m.BlockSet() {
			keep[block] = true
		}
	}

	all, err := blobs.List(ctx, orgID)
	if err != nil {
		return SweepResult{}, err
	}
	var res SweepResult
	for _, hash := range all {
		if keep[hash] {
			res.Kept++
			continue
		}
		if err := blobs.Delete(ctx, orgID, hash); err != nil {
			return res, err
		}
		res.Removed++
	}
	return res, nil
}
