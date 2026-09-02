package homestore

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// ageFiles moves a home's files into the past: what the cache trusts is a
// file whose mtime lies well before the cache's own write, and a file written
// by the test a moment ago would sit inside the racy window.
func ageFiles(t *testing.T, home string) {
	t.Helper()
	past := time.Now().Add(-time.Hour)
	entries, err := os.ReadDir(home)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := os.Chtimes(filepath.Join(home, e.Name()), past, past); err != nil {
			t.Fatal(err)
		}
	}
}

// rewriteKeepingStat changes a file's content without changing what the cache
// looks at: same size, same mtime, same inode.
func rewriteKeepingStat(t *testing.T, path string, content []byte) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(content)) != info.Size() {
		t.Fatalf("test needs equal sizes: %d vs %d", len(content), info.Size())
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
}

// A second sync of an unchanged home does not read it: the cache vouches for
// every file, the store confirms it holds every block, nothing goes up. The
// proof that nothing was read is the bet itself — a file changed under an
// unchanged stat, past the racy window, is not noticed (#174).
func TestASyncOfAnUnchangedHomeReadsNothing(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	orgID := uuid.New()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "notes.md")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageFiles(t, home)
	first, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	rewriteKeepingStat(t, path, []byte("FIRST"))
	second, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ManifestHash != first.ManifestHash {
		t.Fatal("the file was read although its stat had not changed")
	}
	if second.Blocks != 0 || second.BytesUp != 0 {
		t.Fatalf("nothing should have travelled: %+v", second)
	}
}

// The bet is guarded where it is known to be wrong: a file whose mtime lies
// within the racy window of the cache's write is read regardless.
func TestAFileChangedInTheRacyWindowIsRead(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	orgID := uuid.New()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "notes.md")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	first, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Cache written just now, file written just before: inside the window.
	rewriteKeepingStat(t, path, []byte("FIRST"))
	second, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if second.ManifestHash == first.ManifestHash {
		t.Fatal("a file inside the racy window has to be read")
	}
}

// A real change is a change: new content with a new mtime misses the cache,
// and only that file's block travels.
func TestAChangedFileIsFoundAndOnlyItTravels(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	orgID := uuid.New()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := os.WriteFile(filepath.Join(home, name), []byte("content "+name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ageFiles(t, home)
	if _, err := Sync(ctx, blobs, orgID, home, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "b"), []byte("content b, changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	// One block for b, plus the new manifest.
	if res.Blocks != 2 {
		t.Fatalf("one changed file means one block and one manifest, got %d", res.Blocks)
	}
}

// A cached hash the store no longer holds — the sweep took it — is read from
// disk and uploaded again. The cache saves reading, never correctness.
func TestACachedBlockTheStoreLostIsUploadedAgain(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	orgID := uuid.New()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.md"), []byte("kept"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageFiles(t, home)
	if _, err := Sync(ctx, blobs, orgID, home, nil); err != nil {
		t.Fatal(err)
	}
	block := Hash([]byte("kept"))
	if err := blobs.Delete(ctx, orgID, block); err != nil {
		t.Fatal(err)
	}
	res, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ok, _ := blobs.Has(ctx, orgID, block); !ok {
		t.Fatal("the lost block has to be back in the store")
	}
	if res.Blocks != 1 {
		t.Fatalf("exactly the lost block travels, got %d", res.Blocks)
	}
}

// Materialising trusts the same cache: a file the cache vouches for with the
// manifest's own hashes is kept without being read.
func TestMaterialisingKeepsWhatTheCacheVouchesFor(t *testing.T) {
	ctx := context.Background()
	blobs := newDir(t)
	orgID := uuid.New()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "notes.md")
	if err := os.WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatal(err)
	}
	ageFiles(t, home)
	res, err := Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := Load(ctx, blobs, orgID, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	rewriteKeepingStat(t, path, []byte("FIRST"))
	back, err := Materialize(ctx, blobs, orgID, home, m)
	if err != nil {
		t.Fatal(err)
	}
	if back.Kept != 1 || back.Written != 0 {
		t.Fatalf("the cache vouched for the file; it must not be read or rewritten: %+v", back)
	}
	if got, _ := os.ReadFile(path); string(got) != "FIRST" {
		t.Fatal("the file was rewritten")
	}
}

// The mark and the cache lie beside the copy and survive its removal. A copy
// that is not there has no state and no cache, whatever the files say (#173).
func TestARemovedCopyHasNoStateAndNoCache(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	MarkSynced(home, "abc")
	c := &StatCache{Files: map[string]statEntry{"x": {Size: 1}}, dirty: true}
	if err := c.Save(home); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	if got := SyncedHash(home); got != "" {
		t.Fatalf("a removed copy claims to be synced to %q", got)
	}
	if got := LoadStatCache(home); len(got.Files) != 0 {
		t.Fatal("a removed copy still has a cache")
	}
}

// slowBlobs is a store behind a slow line: every Get and Put takes a moment,
// and it counts how many are in flight at once.
type slowBlobs struct {
	*Dir
	inFlight atomic.Int32
	peak     atomic.Int32
}

func (s *slowBlobs) enter() {
	n := s.inFlight.Add(1)
	for {
		p := s.peak.Load()
		if n <= p || s.peak.CompareAndSwap(p, n) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
}

func (s *slowBlobs) Get(ctx context.Context, orgID uuid.UUID, hash string) (io.ReadCloser, error) {
	s.enter()
	defer s.inFlight.Add(-1)
	return s.Dir.Get(ctx, orgID, hash)
}

func (s *slowBlobs) Put(ctx context.Context, orgID uuid.UUID, hash string, r io.Reader) error {
	s.enter()
	defer s.inFlight.Add(-1)
	return s.Dir.Put(ctx, orgID, hash, r)
}

// Blocks travel several at a time, in both directions. One HTTP round trip per
// block, in sequence, left the line idle for minutes on a cold start (#175).
func TestBlocksTravelSeveralAtATime(t *testing.T) {
	ctx := context.Background()
	store := &slowBlobs{Dir: newDir(t)}
	orgID := uuid.New()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 48; i++ {
		if err := os.WriteFile(filepath.Join(home, fmt.Sprintf("f%02d", i)), []byte(fmt.Sprintf("content %d", i)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	res, err := Sync(ctx, store, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	if peak := store.peak.Load(); peak < 2 {
		t.Fatalf("uploads went one at a time (peak %d in flight)", peak)
	}

	// And down again, onto a fresh host.
	store.peak.Store(0)
	m, err := Load(ctx, store, orgID, res.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	fresh := filepath.Join(t.TempDir(), "fresh")
	back, err := Materialize(ctx, store, orgID, fresh, m)
	if err != nil {
		t.Fatal(err)
	}
	if back.Written != 48 {
		t.Fatalf("48 files expected, %d written", back.Written)
	}
	if peak := store.peak.Load(); peak < 2 {
		t.Fatalf("fetches went one at a time (peak %d in flight)", peak)
	}
	for i := 0; i < 48; i++ {
		got, _ := os.ReadFile(filepath.Join(fresh, fmt.Sprintf("f%02d", i)))
		if string(got) != fmt.Sprintf("content %d", i) {
			t.Fatalf("f%02d came back as %q", i, got)
		}
	}
}
