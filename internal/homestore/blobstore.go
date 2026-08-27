// Package homestore keeps agent homes as content-addressed blocks: after every
// job a home is synced into a central store as a whole and materialised from
// there on wake (spec/16, "The central home store").
//
// The point of the construction is what it does NOT do: it never asks what in
// a home is valuable. A measured developer home is 7.1 GB, of which barely
// 48 MB exist nowhere else — and those 48 MB lie scattered all over the place,
// because an agent puts its interim results wherever seems sensible during the
// run. Every list of what to keep is a rule that can be wrong, and its error
// costs work that has already been paid for. So everything goes in, and
// deduplication makes that affordable.
package homestore

import (
	"context"
	"errors"
	"io"

	"github.com/google/uuid"
)

// BulkAsker is the optional half of a store that can answer for many blocks at
// once. Optional because a directory on the same disk has nothing to gain: its
// Has is a stat, and a thousand stats are a thousand stats either way.
//
// It exists for the store that lies behind a network. A sync asks for every
// block of a home whether it is already there, and a grown home has six figures
// of them — on a remote runner that used to be six figures of HTTPS round
// trips, one after another, before a single new byte was uploaded. A 16.9 GB
// home with 150,000 files did not finish inside the control plane's
// thirty-minute bound, so it never got synced at all.
//
// Whoever does not implement it is asked one by one, which is what happened
// before and stays correct.
type BulkAsker interface {
	// HasMany reports for each hash whether the store already holds it. The
	// answer may leave a hash out; missing means "not there".
	HasMany(ctx context.Context, orgID uuid.UUID, hashes []string) (map[string]bool, error)
}

// AskAll answers "which of these does the store already have" through the
// bulk route where there is one, and one by one where there is not.
func AskAll(ctx context.Context, blobs BlobStore, orgID uuid.UUID, hashes []string) (map[string]bool, error) {
	if b, ok := blobs.(BulkAsker); ok {
		return b.HasMany(ctx, orgID, hashes)
	}
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		has, err := blobs.Has(ctx, orgID, h)
		if err != nil {
			return nil, err
		}
		out[h] = has
	}
	return out, nil
}

// ErrNotFound: no block under this key.
var ErrNotFound = errors.New("block not found")

// BlobStore is where the blocks live — a port following the pattern of
// IdentityProvider and SecretStore (spec/10, "batteries included, but
// swappable"). Shipped is `builtin`, a directory; `s3` follows when
// durability or replication is wanted.
//
// Every operation carries the organisation, and that is not bookkeeping: a
// block namespace shared across tenants would be an existence oracle over
// hashes — whoever may ask whether a block is already there learns whether
// somebody else holds exactly that content.
type BlobStore interface {
	// Has reports whether the block is already stored. It is what makes a sync
	// cheap: only what is missing travels.
	Has(ctx context.Context, orgID uuid.UUID, hash string) (bool, error)
	// Put stores a block. Blocks are immutable — writing an existing hash
	// again is allowed and changes nothing.
	Put(ctx context.Context, orgID uuid.UUID, hash string, r io.Reader) error
	// Get opens a block for reading. ErrNotFound when it is not there.
	Get(ctx context.Context, orgID uuid.UUID, hash string) (io.ReadCloser, error)
	// Delete removes a block. Only the garbage collection calls this, and only
	// for blocks no remaining snapshot references.
	Delete(ctx context.Context, orgID uuid.UUID, hash string) error
	// List enumerates an organisation's blocks. Needed for the mark-and-sweep
	// of the retention: deduplication means a block belongs to no single
	// snapshot, so "delete this snapshot" cannot free space by itself.
	List(ctx context.Context, orgID uuid.UUID) ([]string, error)
}
