package gitlab

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// Escape tests for the checkout extraction. They check BEHAVIOUR — "does
// anything land outside the destination directory?" — not the implementation,
// so they hold for the old string check as well as for os.Root.
//
// The vector is the one a path comparison cannot see: the check runs on the
// path STRING, the write resolves that path AGAIN through the file system and
// follows any symlink on the way. Between the two sits a window.
//
// It is not theoretical here. pruneExceptPreserved deliberately keeps the
// dependency caches (node_modules, .venv, vendor …) across checkouts, and the
// agent runs `npm install` in that checkout as a matter of course — with
// whatever postinstall scripts the project's third-party dependencies bring.
// What such a script leaves in node_modules is still there when the next
// archive is unpacked over it. The agent's home is a host directory mounted
// writable into the sandbox, so a write through such a link lands on the HOST.

// escapeArchive builds a gzipped archive with regular files under the usual
// GitLab top-level directory (<project>-<ref>-<sha>).
func escapeArchive(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range entries {
		hdr := &tar.Header{
			Name: "covey-main-abc123/" + name, Mode: 0o644,
			Size: int64(len(body)), Typeflag: tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

// TestExtractDoesNotFollowASymlinkOutOfTheDestination: a directory inside the
// checkout is a symlink pointing outside. An entry "below" it is textually
// inside the destination — the write must still not land outside.
func TestExtractDoesNotFollowASymlinkOutOfTheDestination(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "repos", "p15-main")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	// What a postinstall script could leave behind — and what survives the next
	// checkout, because node_modules is a preserved cache directory.
	if err := os.Symlink(outside, filepath.Join(dest, "node_modules")); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}

	archive := escapeArchive(t, map[string]string{"node_modules/pwned.txt": "escaped"})
	_, err := extractTarGzInto(bytes.NewReader(archive), dest)

	if _, statErr := os.Stat(filepath.Join(outside, "pwned.txt")); statErr == nil {
		t.Fatalf("the archive wrote THROUGH the symlink to %s — extraction escaped the destination (extract error was %v)", outside, err)
	}
}

// TestExtractDoesNotFollowASymlinkedParent: the same one level deeper — the
// escaping link sits inside a directory the archive also writes into.
func TestExtractDoesNotFollowASymlinkedParent(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "repos", "p15-main")
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(filepath.Join(dest, "vendor"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dest, "vendor", "escape")); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}

	archive := escapeArchive(t, map[string]string{"vendor/escape/pwned.txt": "escaped"})
	_, err := extractTarGzInto(bytes.NewReader(archive), dest)

	if _, statErr := os.Stat(filepath.Join(outside, "pwned.txt")); statErr == nil {
		t.Fatalf("the archive wrote through a symlinked parent to %s (extract error was %v)", outside, err)
	}
}

// TestExtractDoesNotOverwriteThroughASymlink: the file behind the link already
// exists. Following it would not create something outside but CHANGE something
// outside — the worse of the two.
func TestExtractDoesNotOverwriteThroughASymlink(t *testing.T) {
	base := t.TempDir()
	dest := filepath.Join(base, "repos", "p15-main")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(base, "important.conf")
	if err := os.WriteFile(victim, []byte("original"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dest, "config.conf")); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}

	archive := escapeArchive(t, map[string]string{"config.conf": "overwritten"})
	_, err := extractTarGzInto(bytes.NewReader(archive), dest)

	got, readErr := os.ReadFile(victim)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(got) != "original" {
		t.Fatalf("the archive overwrote %s through a symlink (extract error was %v)", victim, err)
	}
}

// TestExtractStillUnpacksOrdinaryArchives is the counterweight: containment
// must not be bought with a checkout that no longer works.
func TestExtractStillUnpacksOrdinaryArchives(t *testing.T) {
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "node_modules", "left-pad"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "node_modules", "left-pad", "index.js"), []byte("cached"), 0o644); err != nil {
		t.Fatal(err)
	}

	archive := escapeArchive(t, map[string]string{
		"README.md":              "# covey",
		"cmd/covey/main.go":      "package main",
		"internal/a/b/c/deep.go": "package c",
	})
	files, err := extractTarGzInto(bytes.NewReader(archive), dest)
	if err != nil {
		t.Fatalf("an ordinary archive must unpack: %v", err)
	}
	if files != 3 {
		t.Errorf("files = %d, want 3", files)
	}
	for _, rel := range []string{"README.md", "cmd/covey/main.go", "internal/a/b/c/deep.go"} {
		if _, err := os.Stat(filepath.Join(dest, rel)); err != nil {
			t.Errorf("%s missing: %v", rel, err)
		}
	}
	if got, _ := os.ReadFile(filepath.Join(dest, "node_modules", "left-pad", "index.js")); string(got) != "cached" {
		t.Error("the existing cache directory must survive untouched")
	}
}
