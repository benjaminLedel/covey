package homestore

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// outsideHome lays out a home and, beside it, a file and a directory that are
// not part of it — what a sandbox must never get into its snapshot.
func outsideHome(t *testing.T) (home, outFile, outDir string, secret []byte) {
	t.Helper()
	base := t.TempDir()
	home = filepath.Join(base, "home")
	outDir = filepath.Join(base, "outside")
	outFile = filepath.Join(outDir, "secret")
	secret = []byte("content that lives outside the home")
	for _, d := range []string{home, outDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(outFile, secret, 0o600); err != nil {
		t.Fatal(err)
	}
	return home, outFile, outDir, secret
}

func withScanHook(t *testing.T, hook func(rel string)) {
	t.Helper()
	scanHook = hook
	t.Cleanup(func() { scanHook = nil })
}

// swapForLink replaces path with a symlink to target, atomically, the way a
// process in the sandbox would.
func swapForLink(t *testing.T, path, target string) {
	t.Helper()
	tmp := path + ".swap"
	if err := os.Symlink(target, tmp); err != nil {
		t.Error(err)
		return
	}
	if err := os.RemoveAll(path); err != nil {
		t.Error(err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Error(err)
	}
}

// A regular file that becomes a link between the walk's Lstat and the read is
// skipped, not followed: the snapshot never carries what the link points at.
func TestScanSkipsFileSwappedForLinkBeforeRead(t *testing.T) {
	home, outFile, _, secret := outsideHome(t)
	if err := os.WriteFile(filepath.Join(home, "f"), []byte("harmless"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "g"), []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	withScanHook(t, func(rel string) {
		switch rel {
		case "f":
			swapForLink(t, filepath.Join(home, "f"), outFile)
		case "g":
			// A link that stays inside the home is no better: it is not the
			// file that was classified.
			swapForLink(t, filepath.Join(home, "g"), "f")
		}
	})

	m, err := Scan(home, nil, func(_ string, data []byte) error {
		if bytes.Equal(data, secret) {
			t.Errorf("the scan read the file outside the home")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for _, e := range m.Entries {
		if (e.Path == "f" || e.Path == "g") && len(e.Blocks) > 0 {
			t.Errorf("entry %q was swapped for a link and still carries content: %+v", e.Path, e)
		}
	}
}

// A directory that becomes a link before the walk descends into it is not
// walked: nothing beneath the link's target turns up in the manifest.
func TestScanDoesNotDescendIntoDirSwappedForLink(t *testing.T) {
	home, _, outDir, secret := outsideHome(t)
	if err := os.MkdirAll(filepath.Join(home, "d"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "d", "x"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "keep"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	withScanHook(t, func(rel string) {
		if rel == "d" {
			swapForLink(t, filepath.Join(home, "d"), outDir)
		}
	})

	m, err := Scan(home, nil, func(_ string, data []byte) error {
		if bytes.Equal(data, secret) {
			t.Errorf("the scan read a file outside the home")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	var kept bool
	for _, e := range m.Entries {
		if strings.HasPrefix(e.Path, "d/") {
			t.Errorf("walked beneath a directory swapped for a link: %q", e.Path)
		}
		if e.Path == "keep" && len(e.Blocks) == 1 {
			kept = true
		}
	}
	if !kept {
		t.Errorf("the rest of the home was lost: %+v", m.Entries)
	}
}

// The block a cached hash stood for is re-read only from a regular file in
// the home, never through a link.
func TestReadBlockRefusesLink(t *testing.T) {
	home, outFile, _, secret := outsideHome(t)
	if err := os.Symlink(outFile, filepath.Join(home, "l")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(home)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if data, err := readBlock(root, "l", int64(len(secret)), 0, Hash(secret)); err == nil {
		t.Fatalf("readBlock followed a link out of the home: %q", data)
	}
}

// End to end through Sync, with a writer that keeps swapping a regular file
// and a link to a file outside the home. Bounded by time; the deterministic
// tests above pin the two windows down, this one guards the whole path.
func TestSyncNeverStoresLinkTargetUnderRace(t *testing.T) {
	if testing.Short() {
		t.Skip("race loop")
	}
	home, outFile, _, secret := outsideHome(t)
	blobs, err := NewDir(filepath.Join(filepath.Dir(home), "store"))
	if err != nil {
		t.Fatal(err)
	}
	org := uuid.New()
	target := filepath.Join(home, "f")
	regTmp := filepath.Join(home, ".reg")
	lnkTmp := filepath.Join(home, ".lnk")
	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
			_ = os.WriteFile(regTmp, []byte("harmless"), 0o644)
			_ = os.Rename(regTmp, target)
			_ = os.Remove(lnkTmp)
			_ = os.Symlink(outFile, lnkTmp)
			_ = os.Rename(lnkTmp, target)
		}
	}()
	defer func() { stop.Store(true); <-done }()

	ctx := context.Background()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		res, err := Sync(ctx, blobs, org, home, nil)
		if err != nil {
			// The swap is a change in the home, not a reason to fail.
			t.Fatalf("sync: %v", err)
		}
		m, err := Load(ctx, blobs, org, res.ManifestHash)
		if err != nil {
			continue
		}
		for _, e := range m.Entries {
			if e.Path != "f" || len(e.Blocks) != 1 {
				continue
			}
			rc, err := blobs.Get(ctx, org, e.Blocks[0])
			if err != nil {
				continue
			}
			data, _ := io.ReadAll(rc)
			rc.Close()
			if bytes.Equal(data, secret) {
				t.Fatalf("the snapshot stores the file outside the home as %q", e.Path)
			}
		}
	}
}
