package homestore

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// SyncResult is what a sync cost and what it produced.
type SyncResult struct {
	ManifestHash string
	TotalSize    int64
	// Blocks/BytesUp is what actually travelled — the figure that says whether
	// the deduplication is doing its job. A typical run changes megabytes in a
	// 7 GB home.
	Blocks   int
	BytesUp  int64
	Excluded int
}

// Sync writes a home into the store as a snapshot and returns the manifest
// hash. Everything goes in; the question "what is valuable?" is never asked
// (spec/16).
func Sync(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, excludes Excludes) (SyncResult, error) {
	var res SyncResult
	seen := map[string]bool{}

	manifest, err := Scan(root, excludes, func(hash string, data []byte) error {
		if seen[hash] {
			return nil
		}
		seen[hash] = true
		// Only what is missing travels. This is where the 4 GB of toolchain
		// caches that are byte-for-byte identical on every developer home stop
		// costing anything after the first agent.
		has, err := blobs.Has(ctx, orgID, hash)
		if err != nil {
			return err
		}
		if has {
			return nil
		}
		if err := blobs.Put(ctx, orgID, hash, bytes.NewReader(data)); err != nil {
			return err
		}
		res.Blocks++
		res.BytesUp += int64(len(data))
		return nil
	})
	if err != nil {
		return SyncResult{}, err
	}

	raw, err := manifest.Encode()
	if err != nil {
		return SyncResult{}, err
	}
	hash := Hash(raw)
	has, err := blobs.Has(ctx, orgID, hash)
	if err != nil {
		return SyncResult{}, err
	}
	if !has {
		if err := blobs.Put(ctx, orgID, hash, bytes.NewReader(raw)); err != nil {
			return SyncResult{}, err
		}
		// The manifest is a block like any other and counts as one: the figure
		// the interface shows is "what travelled", and a sync that stores a
		// snapshot has moved at least this.
		res.Blocks++
		res.BytesUp += int64(len(raw))
	}
	res.ManifestHash = hash
	res.TotalSize = manifest.TotalSize()
	return res, nil
}

// Load reads a snapshot's manifest back.
func Load(ctx context.Context, blobs BlobStore, orgID uuid.UUID, manifestHash string) (Manifest, error) {
	r, err := blobs.Get(ctx, orgID, manifestHash)
	if err != nil {
		return Manifest{}, err
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		return Manifest{}, err
	}
	return DecodeManifest(raw)
}

// MaterializeResult is what restoring cost.
type MaterializeResult struct {
	Written int   // files actually written
	Kept    int   // files already correct on disk
	Removed int   // paths the snapshot does not know
	BytesIn int64 // what had to be fetched
}

// Materialize brings a working copy to the state of a snapshot. It writes only
// what differs and leaves the rest alone: on the runner an agent last ran on
// that is the normal case, and then materialising costs nothing at all.
//
// It also removes what the snapshot does not know. That is not tidiness but
// correctness — a working copy that keeps files from a previous state is not
// the home the snapshot describes, and the difference would show up as a
// mystery in the agent's next run.
func Materialize(ctx context.Context, blobs BlobStore, orgID uuid.UUID, root string, m Manifest) (MaterializeResult, error) {
	var res MaterializeResult
	if err := os.MkdirAll(root, 0o755); err != nil {
		return res, err
	}

	wanted := map[string]bool{}
	// Directories first, and shallow before deep — a file cannot be written
	// into a directory that does not exist yet.
	entries := append([]Entry(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Dir != entries[j].Dir {
			return entries[i].Dir
		}
		return entries[i].Path < entries[j].Path
	})

	for _, e := range entries {
		target, err := safeJoin(root, e.Path)
		if err != nil {
			return res, err
		}
		wanted[e.Path] = true
		switch {
		case e.Dir:
			if err := os.MkdirAll(target, e.Mode|0o700); err != nil {
				return res, err
			}
		case e.Link != "":
			if old, err := os.Readlink(target); err == nil && old == e.Link {
				res.Kept++
				continue
			}
			_ = os.Remove(target)
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return res, err
			}
			if err := os.Symlink(e.Link, target); err != nil {
				return res, err
			}
			res.Written++
		default:
			if unchanged(target, e) {
				res.Kept++
				continue
			}
			n, err := writeFile(ctx, blobs, orgID, target, e)
			if err != nil {
				return res, err
			}
			res.Written++
			res.BytesIn += n
		}
	}

	removed, err := removeUnknown(root, wanted)
	if err != nil {
		return res, err
	}
	res.Removed = removed
	return res, nil
}

// unchanged answers whether the file on disk is already the one the snapshot
// describes. Size plus the hash of the content — not the mtime: a materialised
// file gets a fresh one, and comparing it would make every restore rewrite
// everything.
func unchanged(path string, e Entry) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
		return false
	}
	// Only for small files: hashing a 3 GB SDK tarball to save writing it is a
	// bad trade, and large files in a home are the ones that rarely change.
	if e.Size > wholeFileLimit || len(e.Blocks) != 1 {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return Hash(data) == e.Blocks[0]
}

func writeFile(ctx context.Context, blobs BlobStore, orgID uuid.UUID, target string, e Entry) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	// Into a temporary file and rename, so a failed restore does not leave a
	// half file behind that looks like the real one.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".covey-tmp-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())

	var n int64
	for _, hash := range e.Blocks {
		r, err := blobs.Get(ctx, orgID, hash)
		if err != nil {
			tmp.Close()
			return n, fmt.Errorf("block %s of %s: %w", hash[:min(8, len(hash))], e.Path, err)
		}
		written, err := io.Copy(tmp, r)
		r.Close()
		if err != nil {
			tmp.Close()
			return n, err
		}
		n += written
	}
	if err := tmp.Close(); err != nil {
		return n, err
	}
	if err := os.Chmod(tmp.Name(), e.Mode); err != nil {
		return n, err
	}
	return n, os.Rename(tmp.Name(), target)
}

// removeUnknown clears away what the snapshot does not describe.
func removeUnknown(root string, wanted map[string]bool) (int, error) {
	var toRemove []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || path == root {
			return nil //nolint:nilerr // a path that vanished needs no removing
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if wanted[rel] {
			return nil
		}
		toRemove = append(toRemove, path)
		if d.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	for _, path := range toRemove {
		if err := os.RemoveAll(path); err != nil {
			return 0, err
		}
	}
	return len(toRemove), nil
}

// safeJoin keeps a manifest path inside the home. The manifest comes back over
// the protocol from a runner, and a path of "../.." would otherwise write into
// the control plane's own directories.
func safeJoin(root, rel string) (string, error) {
	slashed := strings.ReplaceAll(rel, "\\", "/")
	// Refused, not quietly clamped: a manifest with ".." in it is a protocol
	// violation, and bending it into a path inside the home would restore an
	// agent's work under names nobody wrote.
	for _, part := range strings.Split(slashed, "/") {
		if part == ".." {
			return "", fmt.Errorf("path %q leaves the home", rel)
		}
	}
	target := filepath.Join(root, filepath.FromSlash(filepath.Clean("/"+slashed)))
	if target != root && !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q leaves the home", rel)
	}
	return target, nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
