package homestore

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// SweepResult is what a cleanup freed.
type SweepResult struct {
	Removed int
	Kept    int
	// Spared are blocks too young to touch — no snapshot names them, but a
	// sync that is still running would name them in a moment (#137).
	Spared int
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
// The grace period counts here as well. A preview that promises space the run
// then spares would be exactly the number this function exists to avoid.
func Plan(ctx context.Context, blobs BlobStore, orgID uuid.UUID, live []string, grace time.Duration) (SweepPlan, error) {
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
	ager, _ := blobs.(interface {
		BlockModified(context.Context, uuid.UUID, string) (time.Time, error)
	})
	juenger := time.Now().Add(-grace)
	var out SweepPlan
	for _, hash := range all {
		if keep[hash] {
			continue
		}
		if ager != nil && grace > 0 {
			if when, err := ager.BlockModified(ctx, orgID, hash); err != nil || when.After(juenger) {
				continue
			}
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
	all, err := blobs.List(ctx, orgID)
	if err != nil {
		return SweepResult{}, err
	}
	return SweepList(ctx, blobs, orgID, live, all, DefaultGrace)
}

// DefaultGrace is how young a block has to be to be safe from a sweep.
//
// The control plane allows a sync thirty minutes. Two hours is that with room
// for a slow line and a clock that is not quite the same on both machines, and
// it costs nothing but one cleanup cycle of disk: what is spared today goes in
// the next pass.
const DefaultGrace = 2 * time.Hour

// SweepList removes every block of an organisation that no surviving snapshot
// references — from a list of blocks the caller took, and sparing everything
// younger than grace.
//
// Both of those exist because of one production incident (#137). Mark-and-sweep
// over uploads that are not transactional has a window: the sweep reads which
// manifests are alive, then walks the store deleting what they do not name, and
// a sync that lands IN BETWEEN has its blocks uploaded while its row is not yet
// in the list. On covey.work that window was minutes wide — the biggest home
// takes over two minutes to travel — and the sweep took the manifest chunks of
// a snapshot that had been written fifteen minutes earlier. The row survived,
// pointing at nothing; the agent could not be woken again.
//
// Two answers, and the cheap one is not enough on its own:
//
//   - The caller takes the block list BEFORE reading the manifests. Then a
//     block that arrives during the sweep is not in the list and cannot be
//     deleted, however long the sweep runs.
//   - A block younger than grace stays regardless. That covers the remaining
//     order — a sync that had finished uploading but whose row was not written
//     yet when the manifests were read.
//
// A store that cannot say how old a block is gets the first half only. That is
// better than before and worse than both, and it is said out loud here rather
// than looking like a guarantee.
func SweepList(ctx context.Context, blobs BlobStore, orgID uuid.UUID, live, all []string, grace time.Duration) (SweepResult, error) {
	// A manifest that cannot be read is the one case where deleting would be
	// unforgivable: we do not know what it references, so everything stays and
	// the cleanup reports the failure instead.
	keep, err := liveBlocks(ctx, blobs, orgID, live)
	if err != nil {
		return SweepResult{}, err
	}

	ager, _ := blobs.(interface {
		BlockModified(context.Context, uuid.UUID, string) (time.Time, error)
	})
	juenger := time.Now().Add(-grace)

	var res SweepResult
	for _, hash := range all {
		if keep[hash] {
			res.Kept++
			continue
		}
		if ager != nil && grace > 0 {
			// An unreadable timestamp is not a reason to delete: whoever does
			// not know how old a block is does not know whether a sync is
			// still writing it.
			if when, err := ager.BlockModified(ctx, orgID, hash); err != nil || when.After(juenger) {
				res.Spared++
				continue
			}
		}
		if err := blobs.Delete(ctx, orgID, hash); err != nil {
			return res, err
		}
		res.Removed++
	}
	return res, nil
}
