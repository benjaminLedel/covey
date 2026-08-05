package github

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// serveAttachment starts an HTTPS test server and makes it pass for a GitHub
// attachment host for the duration of the test. Two package-level values are
// borrowed and given back:
//
//   - attachmentHosts, because the action refuses any host that is not
//     GitHub's — that check is the whole point of the action and must not be
//     weakened in production code just to be testable here.
//   - http.DefaultTransport, because the download goes through
//     reqlog.Client(…), which builds on it, and the test server's certificate
//     is self-signed.
func serveAttachment(t *testing.T, h http.HandlerFunc) *url.URL {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	attachmentHosts[host] = true
	prevTransport := http.DefaultTransport
	http.DefaultTransport = srv.Client().Transport
	t.Cleanup(func() {
		delete(attachmentHosts, host)
		http.DefaultTransport = prevTransport
	})
	return u
}

// TestDownloadAttachmentLandsInTheSandbox: the action exists so the agent can
// actually LOOK at a screenshot instead of guessing from the alt text. What
// matters is that the file arrives under the working directory with a usable
// name and that the answer says where.
func TestDownloadAttachmentLandsInTheSandbox(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nfake image bytes")
	u := serveAttachment(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("the brokered token must travel: %q", got)
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Disposition", `inline; filename="login-broken.png"`)
		w.Write(png)
	})
	u.Path = "/user-attachments/assets/2f0c-abc"

	workdir := t.TempDir()
	gc := NewClient("", "test-token")
	res, err := DownloadAttachmentToSandbox(context.Background(), gc, u.String(), workdir)
	if err != nil {
		t.Fatalf("DownloadAttachmentToSandbox: %v", err)
	}
	if res.Filename != "login-broken.png" {
		t.Errorf("the original name must survive: %q", res.Filename)
	}
	if res.Bytes != int64(len(png)) || res.ContentType != "image/png" {
		t.Errorf("size/type wrong: %+v", res)
	}
	if !strings.HasPrefix(res.Path, workdir) {
		t.Fatalf("the file must lie inside the sandbox: %q not under %q", res.Path, workdir)
	}
	onDisk, err := os.ReadFile(res.Path)
	if err != nil || !bytes.Equal(onDisk, png) {
		t.Fatalf("the file must be readable and complete: %v", err)
	}
	if res.Hint == "" {
		t.Error("the answer must tell the agent how to look at it")
	}
}

// TestDownloadAttachmentReportsHTTPFailure: a link that has expired or points
// at a private repo the token cannot see must say so — an empty file in the
// sandbox would send the agent analysing nothing.
func TestDownloadAttachmentReportsHTTPFailure(t *testing.T) {
	u := serveAttachment(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not Found"}`))
	})
	u.Path = "/user-attachments/assets/gone"

	workdir := t.TempDir()
	_, err := DownloadAttachmentToSandbox(context.Background(), NewClient("", "t"), u.String(), workdir)
	if err == nil || !strings.Contains(err.Error(), "404") {
		t.Fatalf("a failed download must be an error: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(workdir, "uploads"))
	if len(entries) != 0 {
		t.Errorf("nothing may be left behind on failure: %v", entries)
	}
}

// TestDownloadAttachmentRespectsTheSizeLimit: an accidentally linked huge asset
// must not fill the sandbox.
func TestDownloadAttachmentRespectsTheSizeLimit(t *testing.T) {
	u := serveAttachment(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		buf := make([]byte, 1<<20)
		for range (maxAttachmentBytes >> 20) + 2 {
			w.Write(buf)
		}
	})
	u.Path = "/user-attachments/assets/huge"

	_, err := DownloadAttachmentToSandbox(context.Background(), NewClient("", "t"), u.String(), t.TempDir())
	if err == nil {
		t.Fatal("an attachment past the limit must be refused")
	}
}

// TestExtractSkipsSymlinksAndKeepsDirectories: a symlink in an archive is an
// escape vector (it can point anywhere and later writes follow it), and it is
// unnecessary for reading code. Directories, by contrast, have to be created —
// an empty one the build expects would otherwise be missing.
func TestExtractSkipsSymlinksAndKeepsDirectories(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(hdr *tar.Header, body string) {
		hdr.Size = int64(len(body))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if body != "" {
			tw.Write([]byte(body))
		}
	}
	top := "acme-support-abc123"
	// GNU tar puts this pseudo-entry at the front of a GitHub archive. It is
	// not a file and must not be counted as one.
	write(&tar.Header{Name: "pax_global_header", Mode: 0o666, Typeflag: tar.TypeReg}, "junk")
	write(&tar.Header{Name: top + "/", Mode: 0o755, Typeflag: tar.TypeDir}, "")
	write(&tar.Header{Name: top + "/testdata/", Mode: 0o755, Typeflag: tar.TypeDir}, "")
	write(&tar.Header{Name: top + "/main.go", Mode: 0o644, Typeflag: tar.TypeReg}, "package main")
	write(&tar.Header{Name: top + "/evil", Mode: 0o777, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}, "")
	tw.Close()
	gz.Close()

	dest := t.TempDir()
	files, err := extractTarGzInto(bytes.NewReader(buf.Bytes()), dest)
	if err != nil {
		t.Fatalf("extractTarGzInto: %v", err)
	}
	if files != 1 {
		t.Errorf("only the regular file counts, got %d", files)
	}
	if fi, err := os.Stat(filepath.Join(dest, "testdata")); err != nil || !fi.IsDir() {
		t.Errorf("an empty directory must be created: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, "evil")); !os.IsNotExist(err) {
		t.Error("a symlink must not be unpacked")
	}
}

func TestExtractRefusesAbsolutePaths(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := "pwned"
	tw.WriteHeader(&tar.Header{Name: "/etc/passwd", Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
	tw.Write([]byte(body))
	tw.Close()
	gz.Close()

	if _, err := extractTarGzInto(bytes.NewReader(buf.Bytes()), t.TempDir()); err == nil {
		t.Fatal("an absolute path in the archive must be refused")
	}
}

func TestExtractRefusesBrokenArchive(t *testing.T) {
	if _, err := extractTarGzInto(strings.NewReader("this is not gzip"), t.TempDir()); err == nil {
		t.Fatal("a body that is not an archive must be an error")
	}
}

// TestCheckoutValidatesTheRepoFirst: an invalid repository name must fail
// before anything is downloaded or a directory created.
func TestCheckoutValidatesTheRepoFirst(t *testing.T) {
	workdir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("nothing may be requested: %s", r.URL.Path)
	}))
	defer srv.Close()

	_, err := Checkout(context.Background(), NewClient(srv.URL+"/api/v3", "t"), "../evil", "main", workdir)
	if err == nil || !strings.Contains(err.Error(), "owner/name") {
		t.Fatalf("an invalid repo must be named: %v", err)
	}
	if entries, _ := os.ReadDir(workdir); len(entries) != 0 {
		t.Error("nothing may be created in the sandbox")
	}
}

// TestStableCheckoutDirIsSafeAndStable: the name is built from values the AGENT
// supplies. It must stay a single path segment (nothing may escape `repos/`)
// and it must not change between two checkouts of the same thing — otherwise
// the dependency caches never carry over.
func TestStableCheckoutDirIsSafeAndStable(t *testing.T) {
	cases := []struct{ repo, ref string }{
		{"acme/support", "main"},
		{"acme/support", "feature/very-long-branch-name-that-goes-on-and-on-and-on-for-ever"},
		{"acme/support", "../../escape"},
		{"../../etc", "../../passwd"},
		{"acme/support", ""},
		{"", ""},
	}
	seen := map[string]string{}
	for _, tc := range cases {
		got := stableCheckoutDir(tc.repo, tc.ref)
		if got == "" {
			t.Errorf("%v: the name must not be empty", tc)
		}
		if strings.ContainsAny(got, `/\`) || got == "." || got == ".." {
			t.Errorf("%v: %q must stay ONE path segment", tc, got)
		}
		if got != stableCheckoutDir(tc.repo, tc.ref) {
			t.Errorf("%v: the name must be stable across calls", tc)
		}
		if prev, ok := seen[got]; ok {
			t.Errorf("%v collides with %s on %q", tc, prev, got)
		}
		seen[got] = fmt.Sprint(tc)
		// The finished path must survive the anchor check — that is the
		// promise Checkout relies on.
		if _, err := securePath(t.TempDir(), got); err != nil {
			t.Errorf("%v: %q must be anchorable: %v", tc, got, err)
		}
	}
}

func TestCheckoutRootAcceptsRelativePaths(t *testing.T) {
	workdir := t.TempDir()
	sub := filepath.Join(workdir, "repos", "acme-support-main")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := checkoutRoot(sub, workdir)
	if err != nil || got != sub {
		t.Fatalf("an absolute path inside the sandbox must be accepted: %v %q", err, got)
	}
	if _, err := checkoutRoot("", workdir); err == nil ||
		!strings.Contains(err.Error(), "checkout_path missing") {
		t.Errorf("an empty path must be named: %v", err)
	}
	if _, err := checkoutRoot(filepath.Join(workdir, "..", "elsewhere"), workdir); err == nil {
		t.Error("a path escaping the sandbox must be refused")
	}
	// The working directory itself is the boundary, not a violation of it.
	if _, err := checkoutRoot(workdir, workdir); err != nil {
		t.Errorf("the working directory itself must be allowed: %v", err)
	}
}

func TestPruneKeepsCacheDirsAndSurvivesAMissingDir(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "node_modules"), 0o755)
	os.MkdirAll(filepath.Join(dir, "internal"), 0o755)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("x"), 0o644)
	// A FILE named like a cache directory is not a cache — only directories are
	// spared, or a stale source file could survive by its name alone.
	os.WriteFile(filepath.Join(dir, "vendor"), []byte("not a dir"), 0o644)

	if err := pruneExceptPreserved(dir); err != nil {
		t.Fatalf("pruneExceptPreserved: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "node_modules")); err != nil {
		t.Errorf("the cache directory must survive: %v", err)
	}
	for _, gone := range []string{"internal", "main.go", "vendor"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%q must be cleared away", gone)
		}
	}
	if err := pruneExceptPreserved(filepath.Join(dir, "never-existed")); err != nil {
		t.Errorf("a directory that is not there is not an error: %v", err)
	}
}

// TestSecurePathAnchors is the promise everything unpacking relies on
// (zip slip, CWE-22).
func TestSecurePathAnchors(t *testing.T) {
	root := t.TempDir()
	for _, ok := range []string{"a.go", "internal/a.go", "./a.go", "a/../b.go"} {
		if _, err := securePath(root, ok); err != nil {
			t.Errorf("%q must be allowed: %v", ok, err)
		}
	}
	for _, bad := range []string{"../escape", "../../etc/passwd", "a/../../escape"} {
		if _, err := securePath(root, bad); err == nil {
			t.Errorf("%q must be refused", bad)
		}
	}
}
