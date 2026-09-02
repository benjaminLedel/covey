package homestore

import (
	"context"
	"os"
	"path/filepath"
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
