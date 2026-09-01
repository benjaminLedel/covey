package homestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Dir is the built-in BlobStore: a directory next to the homes. It is the
// default on purpose — for an installation on one machine an object store is
// unnecessary operational surface, and the promise "one binary + Postgres"
// should not quietly become "one binary + Postgres + MinIO".
//
// Layout: <root>/<org-id>/<first two hex digits>/<hash>. The two digits keep
// the directories from growing into the hundreds of thousands of entries that
// make `ls` unusable and some file systems slow.
type Dir struct {
	root string
}

func NewDir(root string) (*Dir, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return nil, err
	}
	return &Dir{root: abs}, nil
}

// Root is where the blocks lie — for the operational statement that this
// directory needs backup like the database. It is a cache in its function, not
// in its need for protection: 99 % of it would only have to be downloaded
// again, but the rest exists nowhere else (spec/16).
func (d *Dir) Root() string { return d.root }

func (d *Dir) path(orgID uuid.UUID, hash string) (string, error) {
	// The hash comes from our own hashing, but it also comes back over the
	// protocol from a runner. A "hash" of ".." would otherwise write outside
	// the store — checked here, at the one place that turns a hash into a path.
	if len(hash) < 4 || !isHex(hash) {
		return "", fmt.Errorf("%w: invalid block hash %q", ErrNotFound, hash)
	}
	return filepath.Join(d.root, orgID.String(), hash[:2], hash), nil
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (d *Dir) Has(_ context.Context, orgID uuid.UUID, hash string) (bool, error) {
	p, err := d.path(orgID, hash)
	if err != nil {
		return false, err
	}
	_, err = os.Stat(p)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

func (d *Dir) Put(_ context.Context, orgID uuid.UUID, hash string, r io.Reader) error {
	p, err := d.path(orgID, hash)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	// Write to a temporary file and rename: a block is addressed by the hash of
	// its content, so a half-written one under the right name would be a lie
	// that survives every later check.
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}

func (d *Dir) Get(_ context.Context, orgID uuid.UUID, hash string) (io.ReadCloser, error) {
	p, err := d.path(orgID, hash)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNotFound, hash)
	}
	return f, err
}

func (d *Dir) Delete(_ context.Context, orgID uuid.UUID, hash string) error {
	p, err := d.path(orgID, hash)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (d *Dir) List(_ context.Context, orgID uuid.UUID) ([]string, error) {
	root := filepath.Join(d.root, orgID.String())
	var out []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil // an organisation without a single block yet
			}
			return err
		}
		if entry.IsDir() || strings.HasPrefix(entry.Name(), ".tmp-") {
			return nil
		}
		out = append(out, entry.Name())
		return nil
	})
	return out, err
}

// Size is what an organisation's blocks occupy. Walked rather than counted:
// the store's fill level belongs on the dashboard, and a figure derived from
// the manifests would answer a different question — what the snapshots
// reference, not what lies on the disk. The difference between the two is
// exactly the rubbish a cleanup would remove.
func (d *Dir) Size(_ context.Context, orgID uuid.UUID) (int64, error) {
	var total int64
	err := filepath.WalkDir(filepath.Join(d.root, orgID.String()), func(_ string, entry os.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// BlockSize is how much one block occupies — what the cleanup preview adds up.
// BlockModified is when this block was written — what a sweep needs in order
// to spare one that a sync may still be finishing (#137).
func (d *Dir) BlockModified(_ context.Context, orgID uuid.UUID, hash string) (time.Time, error) {
	p, err := d.path(orgID, hash)
	if err != nil {
		return time.Time{}, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}

func (d *Dir) BlockSize(_ context.Context, orgID uuid.UUID, hash string) (int64, error) {
	p, err := d.path(orgID, hash)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(p)
	if err != nil {
		return 0, err
	}
	return info.Size(), nil
}
