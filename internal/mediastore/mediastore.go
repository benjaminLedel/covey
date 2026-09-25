// Package mediastore is the port for a person's media (spec/14, #344): the
// pictures in their notes today, the media of the human wiki later.
//
// Not homestore.BlobStore, although both hold bytes: that one is
// content-addressed per organisation, and its mark-and-sweep deletes every
// block no home snapshot references — a note's picture put there would be
// collected by the next retention run. A medium here has an id and an owner,
// and it goes when no note of that owner references it.
//
// Interface here, implementations in subpackages — "batteries included, but
// swappable" (spec/10): pg keeps the bytes in Postgres, which is right for an
// installation's notes and needs nothing new to run; an S3-compatible store
// is the swap for an installation whose media outgrow the database.
//
// Every call is scoped to an owner (the human seat). A medium belongs to the
// person who put it; there is no read without the owner, and no listing at
// all — a medium is found by the note that references it.
package mediastore

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotFound: no medium with this id belongs to this owner — the same answer
// whether it does not exist or is somebody else's.
var ErrNotFound = errors.New("medium not found")

// Medium is one stored medium.
type Medium struct {
	ContentType string
	Data        []byte
}

type Store interface {
	// Put stores data for the owner and returns its id.
	Put(ctx context.Context, orgID, owner uuid.UUID, contentType string, data []byte) (uuid.UUID, error)
	// Get returns the owner's medium.
	Get(ctx context.Context, owner, id uuid.UUID) (Medium, error)
	// Delete removes the owner's medium; a missing one is not an error.
	Delete(ctx context.Context, owner, id uuid.UUID) error
}
