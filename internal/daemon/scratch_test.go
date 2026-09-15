package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const taskA = "25456bd1-83f7-487f-a7d2-2aa2f1a8dcea"
const taskB = "c975fd05-79db-48b1-935c-f44153149b7d"

func TestScratchDirIsCreatedPerTask(t *testing.T) {
	home := t.TempDir()
	dir := scratchDir(home, taskA)
	if dir != filepath.Join(home, "scratch", taskA) {
		t.Fatalf("scratch directory %q", dir)
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("scratch directory not created: %v", err)
	}
	// A resumed task comes back to the same directory, with what it left there.
	if err := os.WriteFile(filepath.Join(dir, "params.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if again := scratchDir(home, taskA); again != dir {
		t.Fatalf("second run of the same task got %q", again)
	}
	if _, err := os.Stat(filepath.Join(dir, "params.json")); err != nil {
		t.Fatal("the second run must not start empty")
	}
}

// No home, or a task id that is not one: the run starts in the home as before.
// The id becomes a path element, so "../x" must never reach MkdirAll.
func TestScratchDirRefusesWhatIsNotATaskID(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"", "t1", "../escape", "a/b", "{" + taskA + "}", "urn:uuid:" + taskA} {
		if dir := scratchDir(home, id); dir != "" {
			t.Fatalf("task id %q produced %q", id, dir)
		}
	}
	if dir := scratchDir("", taskA); dir != "" {
		t.Fatalf("without a home the directory would be relative to the daemon's cwd: %q", dir)
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Fatalf("something was created in the home: %v", entries)
	}
}

func TestSweepRemovesOnlyOldTaskDirectories(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "scratch")
	now := time.Now()
	old := now.Add(-8 * 24 * time.Hour)

	mk := func(name string, mtime time.Time) string {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(p, "inner"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			t.Fatal(err)
		}
		return p
	}
	stale := mk(taskA, old)
	fresh := mk(taskB, now.Add(-6*24*time.Hour))
	current := mk("0f8e2a3c-1111-4222-8333-944455556666", old)
	own := mk("befunde", old) // a directory the agent named itself
	if err := os.WriteFile(filepath.Join(root, "notes.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A symlink named like a task id is not followed, whatever it points at.
	target := t.TempDir()
	if err := os.WriteFile(filepath.Join(target, "keep.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "7a1c9f00-2222-4333-8444-955566667777")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	n, err := sweepScratch(home, now, current)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("removed %d, want 1", n)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("a task directory untouched for eight days must go")
	}
	for _, p := range []string{fresh, current, own, filepath.Join(root, "notes.md"), filepath.Join(target, "keep.txt")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("%s must stay: %v", p, err)
		}
	}
}

// A home that never had a scratch directory is not an error.
func TestSweepWithoutScratchRoot(t *testing.T) {
	if n, err := sweepScratch(t.TempDir(), time.Now(), ""); n != 0 || err != nil {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// Entering the directory is what keeps it: a run that only writes deeper down
// still stamps it at its start.
func TestScratchDirStampsItsEntry(t *testing.T) {
	home := t.TempDir()
	dir := scratchDir(home, taskA)
	old := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}
	scratchDir(home, taskA)
	if n, _ := sweepScratch(home, time.Now(), ""); n != 0 {
		t.Fatal("a directory entered just now was swept")
	}
}

func TestWithScratchNamesTheDirectory(t *testing.T) {
	dir := filepath.Join("/home/agent", "scratch", taskA)
	got := withScratch("Du bist Brunhilde.", dir)
	if !strings.HasPrefix(got, "Du bist Brunhilde.\n\n## Where your files go") {
		t.Fatalf("the paragraph belongs after the prompt:\n%s", got)
	}
	if !strings.Contains(got, "~/scratch/"+taskA+"/") {
		t.Fatalf("the directory is not named:\n%s", got)
	}
	if withScratch("Du bist Brunhilde.", "") != "Du bist Brunhilde." {
		t.Fatal("without a scratch directory the prompt must stay as it was")
	}
}
