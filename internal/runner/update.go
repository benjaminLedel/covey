package runner

// The runner replaces its own binary — asked for from the control plane, and
// carried out on the host.
//
// Why this exists at all: runner and control plane are delivered separately
// (spec/16, "Delivery"), so their versions drift apart, and that is by design —
// nobody should have to update ten machines to upgrade one server. But the
// consequence used to be that a fix in the data plane meant an SSH session per
// host, and a host nobody logs into keeps its bug. The runner view already
// names version drift ("outdated"); naming a problem whose remedy is not in the
// same place is half a feature.
//
// It does exactly what installer/install.sh does, because that is the path that
// is exercised on every installation: fetch the checksums of a release, fetch
// the archive for this platform, compare, unpack, replace, start again. The
// checksums are not decoration — this downloads a program and then runs it, and
// "over HTTPS" is a statement about the transport, not about the file.
//
// What it deliberately does NOT do: decide when. An update replaces a running
// process, and a host in the middle of a job would lose the sandboxes it is
// watching. Somebody presses the button, and this refuses while work is on it.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"covey/internal/buildinfo"
)

// Repo is the public repository the releases lie in. The same string stands in
// installer/install.sh; there is no third place, and both have to be changed by
// whoever forks this.
const Repo = "benjaminLedel/covey"

// releaseBase is where the archives of one version lie.
func releaseBase(version string) string {
	return "https://github.com/" + Repo + "/releases/download/" + version
}

// maxBinary bounds what is read from the network. A runner binary is around
// 30 MB; a hundred is not a runner binary, and reading it into memory to check
// its checksum would be the moment to find that out too late.
const maxBinary = 200 << 20

// updateSelf carries out the order. It answers before it restarts — the
// connection ends with the restart, and a caller who only saw it drop could not
// tell success from a host that fell over.
func (n *Node) updateSelf(ctx context.Context, req Update) UpdateResult {
	info := buildinfo.Get()
	from := info.Version
	res := UpdateResult{From: from}

	// Not while it is in the middle of something. The replacement itself would
	// survive a running sandbox — the containers belong to Docker, not to this
	// process — but the watchers do not, and a sandbox nobody is watching any
	// more is worse than an update that waits ten minutes.
	//
	// A working copy being written is worse still, and it is the case that
	// actually happened: an agent finished, the sandbox went away, the host
	// looked idle to the control plane, and the update restarted the process
	// into a home sync that had eleven minutes left to run. The snapshot never
	// moved. The sandbox count alone does not see that, so it is not the only
	// thing asked.
	if busy := n.busy(); busy != "" {
		res.Busy = true
		res.Err = busy
		return res
	}

	version := strings.TrimSpace(req.Version)
	if version == "" {
		v, err := latestRelease(ctx)
		if err != nil {
			res.Err = "no published version found: " + err.Error()
			return res
		}
		version = v
	}
	res.To = version
	// Schon da — aber nur, wenn der Name auch für dasselbe Binary steht.
	//
	// Ein von Hand gebauter Runner trägt den Namen des Tags, auf dem sein Baum
	// steht, und ist trotzdem etwas anderes: auf covey.work lief
	// „v0.7.2 (45c9c48-dirty)", während v0.7.2 die neueste Veröffentlichung
	// war. Der Vergleich sagte „schon aktuell", der Knopf meldete Erfolg, und
	// ersetzt wurde nichts — tagelang, ohne dass jemand sah, warum der Host
	// hinter der Steuerebene zurückblieb.
	//
	// Wer aktualisiert, will das veröffentlichte Binary. Ein schmutziger Baum
	// ist der Beweis, dass das laufende nicht dieses ist.
	if version == from && !info.Dirty {
		// An answer, not a failure: whoever presses the button on a host that
		// is already current should read that, and nothing should be replaced.
		return res
	}

	base := strings.TrimRight(strings.TrimSpace(req.BaseURL), "/")
	if base == "" {
		base = releaseBase(version)
	}
	archive := fmt.Sprintf("covey-runner_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)

	sums, err := fetch(ctx, base+"/SHA256SUMS", 1<<20)
	if err != nil {
		res.Err = "checksums not readable: " + err.Error()
		return res
	}
	want, ok := checksumFor(string(sums), archive)
	if !ok {
		// The checksum file is the release's table of contents: what is not in
		// it does not exist for this platform, and that is a better message
		// than a 404 on the archive.
		res.Err = fmt.Sprintf("%s does not publish %s", version, archive)
		return res
	}

	blob, err := fetch(ctx, base+"/"+archive, maxBinary)
	if err != nil {
		res.Err = "download failed: " + err.Error()
		return res
	}
	sum := sha256.Sum256(blob)
	if got := hex.EncodeToString(sum[:]); got != want {
		res.Err = "checksum does not match — the archive was not installed"
		return res
	}

	binary, err := binaryFromArchive(blob, "covey-runner")
	if err != nil {
		res.Err = err.Error()
		return res
	}
	if err := n.replaceSelf(binary); err != nil {
		res.Err = err.Error()
		return res
	}
	res.Restarting = true
	return res
}

// latestRelease resolves "the newest" — the same question install.sh asks, at
// the same address.
func latestRelease(ctx context.Context) (string, error) {
	body, err := fetch(ctx, "https://api.github.com/repos/"+Repo+"/releases/latest", 1<<20)
	if err != nil {
		return "", err
	}
	var release struct {
		Tag string `json:"tag_name"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", err
	}
	if release.Tag == "" {
		return "", fmt.Errorf("the release carries no tag_name")
	}
	return release.Tag, nil
}

func fetch(ctx context.Context, url string, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "covey-runner/"+buildinfo.Get().Version)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s answered %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

// checksumFor reads one line out of SHA256SUMS. The format is the one
// sha256sum writes: hash, blanks, file name.
func checksumFor(sums, file string) (string, bool) {
	for _, line := range strings.Split(sums, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == file {
			return fields[0], true
		}
	}
	return "", false
}

// binaryFromArchive picks one file out of the tar.gz — the archives hold
// exactly one program each, and which one is not a matter of position.
func binaryFromArchive(blob []byte, name string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(blob))
	if err != nil {
		return nil, fmt.Errorf("archive not readable: %w", err)
	}
	defer func() { _ = gz.Close() }()
	tr := tar.NewReader(gz)
	for {
		head, err := tr.Next()
		if err == io.EOF {
			return nil, fmt.Errorf("the archive contains no %s", name)
		}
		if err != nil {
			return nil, fmt.Errorf("archive not readable: %w", err)
		}
		if filepath.Base(head.Name) != name || head.Typeflag != tar.TypeReg {
			continue
		}
		return io.ReadAll(io.LimitReader(tr, maxBinary))
	}
}

// replaceSelf writes the new binary where this process was started from.
//
// Beside it first and then renamed: a rename within one directory is atomic, so
// there is no moment at which the file exists half-written. Writing over it
// directly would produce exactly that moment — and on Linux it would not work
// at all, because a running program's file cannot be written to (ETXTBSY).
// Replacing the directory entry is allowed; the running process keeps the old
// inode until it ends.
func (n *Node) replaceSelf(binary []byte) error {
	locate := n.executable
	if locate == nil {
		locate = os.Executable
	}
	exe, err := locate()
	if err != nil {
		return fmt.Errorf("own path not resolvable: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	tmp := filepath.Join(filepath.Dir(exe), "."+filepath.Base(exe)+".new")
	if err := os.WriteFile(tmp, binary, 0o755); err != nil {
		// The usual case, and worth saying plainly: the runner runs as a user
		// who may not write into /usr/local/bin.
		return fmt.Errorf("cannot write next to %s: %w", exe, err)
	}
	if err := os.Rename(tmp, exe); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("cannot replace %s: %w", exe, err)
	}
	return nil
}

// busy says what this host is in the middle of, and the empty string when it is
// in the middle of nothing. One sentence, because it goes straight into an
// answer somebody reads: "the update stays planned — why?"
//
// Two things count. Sandboxes, because their watchers live in this process.
// And working-copy work — a start, a stop, a sync — because that is the queue
// (inOrder) through which everything passes that touches a home, and a restart
// in the middle of one abandons it silently.
func (n *Node) busy() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	if len(n.running) > 0 {
		return fmt.Sprintf("this host is carrying %d sandbox(es) — an update would leave them unwatched", len(n.running))
	}
	if len(n.turn) > 0 {
		return fmt.Sprintf("this host is working on %d working cop(ies) — a restart would abandon the write", len(n.turn))
	}
	return ""
}
