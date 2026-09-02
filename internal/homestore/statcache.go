package homestore

import (
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"time"
)

// A stat cache beside the working copy: for every regular file the last scan
// or materialise saw, its size, modification time and inode, and the block
// hashes that content had. A file whose three figures have not changed has
// the same content — that is the bet rsync and git make, and it is what
// turns a sync of an unchanged home from a full read of every byte into a
// walk over metadata (#174).
//
// What travels was always the diff: the store is asked which blocks it holds
// and only the rest goes up. What the cache removes is the work BEFORE that —
// reading and hashing gigabytes to learn the hashes of files a run did not
// touch. The store is still asked about every block, cached or not; a cached
// hash the store does not hold is read from disk then, so a block the sweep
// lost is uploaded again rather than referenced from a manifest that cannot be
// materialised.
//
// The bet has one known hole, and it is guarded: a file modified in the same
// instant the cache was written may keep size and mtime while its content
// changed (the "racy" case). A file whose mtime lies within racyWindow of the
// cache's own time is therefore read regardless.

// statCacheSuffix names the cache file, beside the copy like the state mark.
const statCacheSuffix = ".stat"

// racyWindow is how close to the cache's write a file's mtime may lie before
// the cache stops trusting it. Two seconds is the coarsest mtime any file
// system in use rounds to, with room to spare.
const racyWindow = 2 * time.Second

// StatCache is the cache of one working copy.
type StatCache struct {
	At    time.Time            `json:"at"`
	Files map[string]statEntry `json:"files"`
	// dirty marks a change worth writing.
	dirty bool
}

type statEntry struct {
	Size   int64    `json:"size"`
	MTime  int64    `json:"mtime"` // UnixNano
	Ino    uint64   `json:"ino,omitempty"`
	Blocks []string `json:"blocks"`
}

func statCacheFile(root string) string { return strings.TrimRight(root, "/\\") + statCacheSuffix }

// LoadStatCache reads the cache of a working copy. A missing or unreadable
// cache is an empty one: every file is then read, as before the cache
// existed, and the next save writes a full one.
func LoadStatCache(root string) *StatCache {
	c := &StatCache{Files: map[string]statEntry{}}
	if root == "" {
		return c
	}
	raw, err := os.ReadFile(statCacheFile(root))
	if err != nil {
		return c
	}
	var stored StatCache
	if err := json.Unmarshal(raw, &stored); err != nil || stored.Files == nil {
		return c
	}
	stored.dirty = false
	return &stored
}

// Save writes the cache beside the copy — atomically, so a crash mid-write
// leaves the previous cache rather than half of a new one.
func (c *StatCache) Save(root string) error {
	if c == nil || root == "" || !c.dirty {
		return nil
	}
	c.At = time.Now()
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	path := statCacheFile(root)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	c.dirty = false
	return nil
}

// Lookup answers the block hashes of a file if the cache knows this exact
// file — same size, same mtime, same inode — and the mtime is old enough to
// be trusted.
func (c *StatCache) Lookup(rel string, info fs.FileInfo) ([]string, bool) {
	if c == nil || c.At.IsZero() {
		return nil, false
	}
	e, ok := c.Files[rel]
	if !ok || e.Size != info.Size() || e.MTime != info.ModTime().UnixNano() {
		return nil, false
	}
	if ino := inodeOf(info); ino != 0 && e.Ino != 0 && ino != e.Ino {
		return nil, false
	}
	if info.ModTime().After(c.At.Add(-racyWindow)) {
		return nil, false
	}
	return e.Blocks, true
}

// Note records what a file's content hashed to at this moment.
func (c *StatCache) Note(rel string, info fs.FileInfo, blocks []string) {
	if c == nil {
		return
	}
	c.Files[rel] = statEntry{
		Size: info.Size(), MTime: info.ModTime().UnixNano(), Ino: inodeOf(info), Blocks: blocks,
	}
	c.dirty = true
}

// Forget drops what is no longer there. Called with the paths a walk saw; the
// rest are files that were deleted since.
func (c *StatCache) Forget(seen map[string]bool) {
	if c == nil {
		return
	}
	for rel := range c.Files {
		if !seen[rel] {
			delete(c.Files, rel)
			c.dirty = true
		}
	}
}
