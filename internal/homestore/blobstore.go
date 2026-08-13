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
