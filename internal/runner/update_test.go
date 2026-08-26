package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/google/uuid"
)

// release is a stand-in for the published artefacts: one archive per platform
// and the checksum file that is their table of contents.
func release(t *testing.T, version, content string) *httptest.Server {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := []byte(content)
	if err := tw.WriteHeader(&tar.Header{Name: "covey-runner", Mode: 0o755, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	archive := buf.Bytes()
	name := fmt.Sprintf("covey-runner_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive)
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)

	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(sums))
	})
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	})
	s := httptest.NewServer(mux)
	t.Cleanup(s.Close)
	return s
}

// A host replaces its own binary and says what became of it. The whole point is
// that this does not need an SSH session: runner and control plane are
// delivered separately, so they drift apart, and a fix in the data plane used
// to mean logging into every machine that has one.
func TestARunnerReplacesItsOwnBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no exec that replaces the process")
	}
	dir := t.TempDir()
	// The "binary" this test runs as: updateSelf replaces the file the process
	// was started from, so the test points it at a copy of its own path.
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	origin := release(t, "v9.9.9", "the new one")

	restarted := 0
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { restarted++; return nil }
	node.executable = func() (string, error) { return exe, nil }

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: origin.URL})
	if res.Err != "" {
		t.Fatalf("update: %s", res.Err)
	}
	if !res.Restarting || res.To != "v9.9.9" {
		t.Errorf("the answer has to say what happens next: %+v", res)
	}
	if got, err := os.ReadFile(exe); err != nil || string(got) != "the new one" {
		t.Errorf("the binary was not replaced: %q, %v", got, err)
	}
	// Executable, or the restart runs into a file nobody may start.
	if info, err := os.Stat(exe); err != nil || info.Mode().Perm()&0o111 == 0 {
		t.Errorf("the new binary is not executable: %v, %v", info.Mode(), err)
	}
}

// What is downloaded here is a program that is then run. "Over HTTPS" is a
// statement about the transport and not about the file — so the checksum
// decides, and a mismatch leaves the old binary exactly where it was.
func TestAnArchiveThatDoesNotMatchItsChecksumIsNotInstalled(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("covey-runner_v9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	mux := http.NewServeMux()
	mux.HandleFunc("/SHA256SUMS", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n", "00000000000000000000000000000000000000000000000000000000000000ff", name)
	})
	mux.HandleFunc("/"+name, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("something else entirely"))
	})
	origin := httptest.NewServer(mux)
	defer origin.Close()

	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { t.Error("a failed update must not restart anything"); return nil }
	node.executable = func() (string, error) { return exe, nil }

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: origin.URL})
	if res.Err == "" || res.Restarting {
		t.Fatalf("an archive that does not match must not be installed: %+v", res)
	}
	if got, _ := os.ReadFile(exe); string(got) != "the old one" {
		t.Errorf("the old binary was touched: %q", got)
	}
}

// Not while it is carrying anything. Replacing the process would survive the
// containers — they belong to Docker — but not the watchers, and a sandbox
// nobody watches any more is worse than an update that waits.
func TestAnUpdateWaitsForTheSandboxes(t *testing.T) {
	dir := t.TempDir()
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { t.Error("must not restart while a sandbox is running"); return nil }
	node.mu.Lock()
	node.running[uuid.New()] = &sandboxProc{container: "c1", cancel: func() {}}
	node.mu.Unlock()

	res := node.updateSelf(context.Background(), Update{Version: "v9.9.9", BaseURL: "http://127.0.0.1:1"})
	if res.Err == "" || res.Restarting {
		t.Fatalf("a busy host has to refuse: %+v", res)
	}
	if !bytes.Contains([]byte(res.Err), []byte("sandbox")) {
		t.Errorf("the reason has to name what is in the way: %q", res.Err)
	}
}

// The built-in runner is this process. Offering it an update would download a
// binary nobody would ever start.
func TestTheBuiltInRunnerIsNotUpdatedThroughTheProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p, runnerID := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), org)
	if _, err := p.Update(context.Background(), runnerID, "v9.9.9", ""); err == nil {
		t.Fatal("the built-in runner has to refuse the update")
	}
}
