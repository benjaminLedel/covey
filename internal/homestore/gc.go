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

// SweepPlan is what a cleanup would free — measured, not estimated.
type SweepPlan struct {
	Blocks int
	Bytes  int64
}

// Plan measures a sweep without performing it. It is what the preview shows,
// and it has to be the space ACTUALLY freed: a block goes only when no
// remaining snapshot references it, so the sum of the snapshot sizes would be
// a number that is never right (spec/16, "Retention").
func Plan(ctx context.Context, blobs BlobStore, orgID uuid.UUID, live []string) (SweepPlan, error) {
	keep, err := liveBlocks(ctx, blobs, orgID, live)
	if err != nil {
		return SweepPlan{}, err
	}
	all, err := blobs.List(ctx, orgID)
	if err != nil {
		return SweepPlan{}, err
	}
	sizer, _ := blobs.(interface {
		BlockSize(context.Context, uuid.UUID, string) (int64, error)
	})
	var out SweepPlan
	for _, hash := range all {
		if keep[hash] {
			continue
		}
		out.Blocks++
		if sizer != nil {
			if n, err := sizer.BlockSize(ctx, orgID, hash); err == nil {
				out.Bytes += n
			}
		}
	}
	return out, nil
}

// liveBlocks is everything the surviving snapshots reference — the files they
// hold AND the objects the manifests themselves are made of.
//
// The second half is not decoration. A manifest over the chunk limit lies in
// the store as an index over its pieces (putManifest), and those pieces are
// blocks like any other. A keep-set built from the snapshot hash plus
// BlockSet() named the index and the file contents, but never the chunks in
// between — so the next sweep deleted them, the index survived pointing at
// nothing, and the agent's home could not be materialised again. That is why
// this asks for the objects rather than only the manifest.
func liveBlocks(ctx context.Context, blobs BlobStore, orgID uuid.UUID, live []string) (map[string]bool, error) {
	keep := map[string]bool{}
	for _, manifestHash := range live {
		m, objects, err := LoadWithObjects(ctx, blobs, orgID, manifestHash)
		if err != nil {
			return nil, err
		}
		for _, o := range objects {
			keep[o] = true
		}
		for block := range m.BlockSet() {
			keep[block] = true
		}
	}
	return keep, nil
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
	// A manifest that cannot be read is the one case where deleting would be
	// unforgivable: we do not know what it references, so everything stays and
	// the cleanup reports the failure instead.
	keep, err := liveBlocks(ctx, blobs, orgID, live)
	if err != nil {
		return SweepResult{}, err
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
