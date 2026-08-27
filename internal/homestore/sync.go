package homestore

import (
	"bytes"
	"context"
	"encoding/json"
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
	// The manifest itself obeys the block size, and it took a production
	// instance to notice that it did not: every file in a home is chunked at
	// chunkSize, but the manifest went as ONE object of whatever size it had.
	// A home of 16.9 GB carries hundreds of thousands of entries, each with a
	// path and a 64-character hash — tens of megabytes in one PUT. Over a
	// remote runner that is an HTTP request, and it died against the request
	// limit of whatever sits in front of the control plane. The home was
	// therefore unsyncable, permanently, while every small home worked and made
	// the installation look healthy.
	hash, stored, err := putManifest(ctx, blobs, orgID, raw)
	if err != nil {
		return SyncResult{}, err
	}
	// The manifest is a block like any other and counts as one: the figure the
	// interface shows is "what travelled", and a sync that stores a snapshot
	// has moved at least this.
	res.Blocks += stored
	if stored > 0 {
		res.BytesUp += int64(len(raw))
	}
	res.ManifestHash = hash
	res.TotalSize = manifest.TotalSize()
	return res, nil
}

// manifestIndexMarker is what tells a stored object apart from a manifest: an
// index names the chunks the manifest was split into. It is a field name and
// not a length rule, because a reader must be able to decide from the CONTENT —
// a snapshot written by an older version is a plain manifest of any size, and
// it has to keep loading.
type manifestIndex struct {
	Chunks []string `json:"covey_manifest_chunks"`
}

// putManifest stores the manifest and returns the hash a snapshot refers to.
// Small enough, and it lies there as before — a reader of any version
// understands it. Above chunkSize it travels in pieces with a small index in
// front, which is the only object the snapshot hash then points at.
//
// Returns how many objects were actually written (0 = everything was already
// there, which is the normal case for an unchanged home).
func putManifest(ctx context.Context, blobs BlobStore, orgID uuid.UUID, raw []byte) (string, int, error) {
	if len(raw) <= chunkSize {
		hash := Hash(raw)
		has, err := blobs.Has(ctx, orgID, hash)
		if err != nil {
			return "", 0, err
		}
		if has {
			return hash, 0, nil
		}
		if err := blobs.Put(ctx, orgID, hash, bytes.NewReader(raw)); err != nil {
			return "", 0, err
		}
		return hash, 1, nil
	}

	var idx manifestIndex
	written := 0
	for off := 0; off < len(raw); off += chunkSize {
		end := off + chunkSize
		if end > len(raw) {
			end = len(raw)
		}
		part := raw[off:end]
		h := Hash(part)
		idx.Chunks = append(idx.Chunks, h)
		has, err := blobs.Has(ctx, orgID, h)
		if err != nil {
			return "", 0, err
		}
		if has {
			continue
		}
		if err := blobs.Put(ctx, orgID, h, bytes.NewReader(part)); err != nil {
			return "", 0, err
		}
		written++
	}
	enc, err := json.Marshal(idx)
	if err != nil {
		return "", 0, err
	}
	hash := Hash(enc)
	has, err := blobs.Has(ctx, orgID, hash)
	if err != nil {
		return "", 0, err
	}
	if !has {
		if err := blobs.Put(ctx, orgID, hash, bytes.NewReader(enc)); err != nil {
			return "", 0, err
		}
		written++
	}
	return hash, written, nil
}

// Load reads a snapshot's manifest back — whether it lies there whole or as an
// index over its chunks.
func Load(ctx context.Context, blobs BlobStore, orgID uuid.UUID, manifestHash string) (Manifest, error) {
	raw, err := fetch(ctx, blobs, orgID, manifestHash)
	if err != nil {
		return Manifest{}, err
	}
	var idx manifestIndex
	if err := json.Unmarshal(raw, &idx); err == nil && len(idx.Chunks) > 0 {
		var buf bytes.Buffer
		for _, h := range idx.Chunks {
			part, err := fetch(ctx, blobs, orgID, h)
			if err != nil {
				return Manifest{}, fmt.Errorf("manifest chunk %s: %w", h, err)
			}
			buf.Write(part)
		}
		raw = buf.Bytes()
	}
	return DecodeManifest(raw)
}

func fetch(ctx context.Context, blobs BlobStore, orgID uuid.UUID, hash string) ([]byte, error) {
	r, err := blobs.Get(ctx, orgID, hash)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return io.ReadAll(r)
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
// unchanged: is the file on disk already the one the snapshot describes?
//
// It reads the file to answer that, LARGE FILES INCLUDED, and the earlier
// version deliberately did not — "hashing a 3 GB SDK tarball to save writing it
// is a bad trade". The trade it actually made was worse: everything above
// wholeFileLimit counted as changed, always, and was therefore fetched and
// written again on every single wake. Measured on a production instance: an
// agent whose home holds an SDK, a JDK and package caches pulled 8.3 GB across
// the network at every wake — eleven minutes, before its first turn, every
// time. Reading those same bytes off the local disk costs seconds.
//
// The comparison mirrors how the blocks were made (blocksOf): one block means
// the whole file was hashed at once, several mean fixed chunkSize pieces.
func unchanged(path string, e Entry) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() != e.Size {
		return false
	}
	if len(e.Blocks) == 0 {
		return e.Size == 0
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	if len(e.Blocks) == 1 {
		data, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		return Hash(data) == e.Blocks[0]
	}
	buf := make([]byte, chunkSize)
	for _, want := range e.Blocks {
		n, err := io.ReadFull(f, buf)
		if n == 0 || (err != nil && err != io.ErrUnexpectedEOF) {
			return false
		}
		if Hash(buf[:n]) != want {
			return false
		}
	}
	// Nothing may follow: a longer file with the same prefix is not this one.
	// The size check above already says so — this is the belt to its braces.
	if n, _ := f.Read(buf[:1]); n != 0 {
		return false
	}
	return true
}

// updateInPlace repairs a chunked file that is already there, writing ONLY the
// chunks that differ. Without it a changed file is rewritten whole: a
// transcript that grew by a line costs its full size in local writes, and on a
// home of gigabytes that is the second half of the same waste the reuse of
// local chunks removes from the wire.
//
// Not writing through a temporary file is the deliberate part. The temp+rename
// dance protects against a half-written file that looks finished — and the
// protection is now elsewhere and stronger: unchanged() compares by content,
// so a file left mixed by a crash is detected at the next materialisation and
// repaired from the store. The working copy is replaceable; that is the whole
// premise of the home store.
//
// Reports what had to be FETCHED, like writeFile — the bytes taken from the
// file itself never travelled.
func updateInPlace(ctx context.Context, blobs BlobStore, orgID uuid.UUID, f *os.File, e Entry) (int64, error) {
	buf := make([]byte, chunkSize)
	var n int64
	for i, hash := range e.Blocks {
		off := int64(i) * int64(chunkSize)
		read, err := f.ReadAt(buf, off)
		if read > 0 && (err == nil || err == io.EOF) && Hash(buf[:read]) == hash {
			continue
		}
		r, err := blobs.Get(ctx, orgID, hash)
		if err != nil {
			return n, fmt.Errorf("block %s of %s: %w", hash[:min(8, len(hash))], e.Path, err)
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			return n, err
		}
		if _, err := f.WriteAt(data, off); err != nil {
			return n, err
		}
		n += int64(len(data))
	}
	// A file that used to be longer keeps its tail without this.
	if err := f.Truncate(e.Size); err != nil {
		return n, err
	}
	return n, f.Sync()
}

func writeFile(ctx context.Context, blobs BlobStore, orgID uuid.UUID, target string, e Entry) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	// A chunked file that is already here is repaired where it lies — only the
	// chunks that differ are written, and nothing is copied that has not
	// changed. Everything else takes the route below.
	if len(e.Blocks) > 1 {
		if f, err := os.OpenFile(target, os.O_RDWR, e.Mode); err == nil {
			info, statErr := f.Stat()
			if statErr == nil && info.Mode().IsRegular() {
				n, err := updateInPlace(ctx, blobs, orgID, f, e)
				closeErr := f.Close()
				if err == nil && closeErr == nil {
					return n, os.Chmod(target, e.Mode)
				}
				// Anything unexpected falls through to the whole rewrite —
				// which is correct in every case, only more expensive.
			} else {
				f.Close()
			}
		}
	}
	// Into a temporary file and rename, so a failed restore does not leave a
	// half file behind that looks like the real one.
	tmp, err := os.CreateTemp(filepath.Dir(target), ".covey-tmp-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())

	// No reuse of local bytes here on purpose: this is the route for a file
	// that is NOT there yet (or could not be repaired), and what is not there
	// has nothing to reuse. The saving lives in one place — updateInPlace above
	// — and a second copy of it here would be a mechanism nothing exercises:
	// removing it made no test go red, which is how it was found.
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
