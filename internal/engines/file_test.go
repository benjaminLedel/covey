package engines

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// oneFile is what a vendor who ships a CLI as one Node bundle serves: not an
// archive, not a package — one executable file.
func oneFile() []byte {
	return []byte("#!/usr/bin/env node\nconsole.log(1)\n")
}

// fileRelease is a release of the new kind with an artefact on disk.
func fileRelease(t *testing.T, dir string, body []byte, binary string) Release {
	t.Helper()
	path := filepath.Join(dir, "cli")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	return Release{Version: "1", Kind: KindFile, URL: "file://" + path,
		Integrity: "sha256:" + sha256Hex(body), Binary: binary}
}

func TestStoreInstallsOneFile(t *testing.T) {
	dir := t.TempDir()
	body := oneFile()
	store := &Store{Dir: filepath.Join(dir, "engines")}
	rel := fileRelease(t, dir, body, "bin/tool-file")
	rel.engine = "tool-file"

	layer, err := store.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if got := filepath.Base(layer.Exec); got != "tool-file" {
		t.Fatalf("exec = %q", layer.Exec)
	}
	got, err := os.ReadFile(layer.Exec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatal("the file on the layer is not the bytes that were fetched")
	}
	// One file written is not executable unless the install made it so, and not
	// readable for the party that matters: the mode of a WriteFile goes through
	// the umask, and the sandbox that has to run this file is a different user
	// than the runner that wrote it.
	info, err := os.Stat(layer.Exec)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o055 != 0o055 {
		t.Fatalf("the file is not executable for the sandbox user: %v", info.Mode())
	}
	if _, err := os.Stat(filepath.Join(layer.Root, markerFile)); err != nil {
		t.Fatalf("a layer without a marker is a crashed install: %v", err)
	}
}

// `binary` has no consequences for a tarball — the archive answers with its own
// executable — so the field is read literally nowhere else. Here it says where a
// byte sequence is written, inside a directory that is bind-mounted into someone
// else's sandbox. Which is why it is read as a path inside the layer and not as
// a path.
func TestFileRefusesABinaryOutOfTheLayer(t *testing.T) {
	dir := t.TempDir()
	store := &Store{Dir: filepath.Join(dir, "engines")}
	rel := fileRelease(t, dir, oneFile(), "../../escape")
	rel.engine = "tool-escape"

	if _, err := store.Ensure(context.Background(), rel, Auth{}); err == nil {
		t.Fatal("a binary outside the layer was accepted")
	} else if !strings.Contains(err.Error(), "out of the layer") {
		t.Errorf("refused, but not as an escape: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escape")); !os.IsNotExist(err) {
		t.Errorf("something was written beside the layer: %v", err)
	}
}

// The case the kind came about for: the bytes sit behind a login. The auth pair
// is handled by the fetch, before any kind is looked at — this says so about the
// kind, not only about the fetch.
func TestFileFetchesBehindALoginWithTheNamedHeader(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Write(oneFile())
	}))
	defer srv.Close()
	t.Setenv("COVEY_TEST_FILE_TOKEN", "Bearer sc-test")

	store := &Store{Dir: t.TempDir()}
	rel := Release{Version: "1", Kind: KindFile, URL: srv.URL + "/cli",
		Integrity: "sha256:" + sha256Hex(oneFile()), Binary: "bin/tool",
		AuthHeader: "Authorization", AuthEnv: "COVEY_TEST_FILE_TOKEN"}
	rel.engine = "tool-login"

	if _, err := store.Ensure(context.Background(), rel, Auth{}); err != nil {
		t.Fatalf("a file behind a login did not install: %v", err)
	}
	if auth != "Bearer sc-test" {
		t.Fatalf("the header went out as %q", auth)
	}
}
