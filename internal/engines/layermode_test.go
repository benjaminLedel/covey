package engines

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The layer directory is the one thing the sandbox has to walk through to reach
// its engine, and it comes from os.MkdirTemp: 0700, the runner alone. The binary
// inside is 0755 — the two disagree, and what the sandbox answers is permission
// denied. The mode is set before a layer is published.
func TestALayerIsEnterableByTheSandbox(t *testing.T) {
	dir := t.TempDir()
	store := &Store{Dir: filepath.Join(dir, "engines")}
	rel := fileRelease(t, dir, oneFile(), "bin/tool-mode")
	rel.engine = "tool-mode"

	layer, err := store.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	for _, p := range []string{layer.Root, layer.Exec} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o055 != 0o055 {
			t.Errorf("%s stands at %v: the sandbox user cannot reach the engine",
				filepath.Base(p), st.Mode().Perm())
		}
	}
}

// A layer installed by an earlier build stands closed on every host that tried
// one, and a fix that only covers new installs leaves those hosts answering the
// same permission denied after an update, forever. The path every start takes
// therefore puts the bits right too.
func TestAClosedLayerIsOpenedOnTheNextStart(t *testing.T) {
	dir := t.TempDir()
	store := &Store{Dir: filepath.Join(dir, "engines")}
	rel := fileRelease(t, dir, oneFile(), "bin/tool-closed")
	rel.engine = "tool-closed"

	layer, err := store.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if err := os.Chmod(layer.Root, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Ensure(context.Background(), rel, Auth{}); err != nil {
		t.Fatalf("the second Ensure: %v", err)
	}
	st, err := os.Stat(layer.Root)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o055 != 0o055 {
		t.Errorf("the layer still stands at %v after a start", st.Mode().Perm())
	}
}
