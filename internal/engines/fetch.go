package engines

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fetchArtifact reads one artefact behind one URL, capped.
//
// file:// is supported for the same reason the other catalogues support it: an
// air-gapped installation serves its engines from a mounted path, and that must
// not become a second code path — a second code path is a second set of
// behaviour, and the digest check below would be the first thing to fall out of
// it.
func fetchArtifact(ctx context.Context, httpc *http.Client, raw string, limit int64, watch func(Progress)) ([]byte, error) {
	if strings.HasPrefix(raw, "file://") {
		path := strings.TrimPrefix(raw, "file://")
		// Opened rather than ReadFile, and for one reason: the reading below can
		// say how far it has got. An air-gapped installation fetches its engines
		// from a mounted path, and a hundred and fifty megabytes off that path is
		// not instant either — a step that reports nothing looks like a hang
		// whoever wrote it.
		f, err := os.Open(path)
		if err != nil {
			return nil, fmt.Errorf("engines: artefact: %w", err)
		}
		defer f.Close()
		var total int64
		if st, err := f.Stat(); err == nil {
			total = st.Size()
		}
		body, err := io.ReadAll(io.LimitReader(counted(f, total, watch), limit+1))
		if err != nil {
			return nil, fmt.Errorf("engines: artefact: %w", err)
		}
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("engines: artefact is %d bytes, over the %d byte cap", len(body), limit)
		}
		return body, nil
	}
	if httpc == nil {
		// An engine archive is hundreds of megabytes. The timeout of a document
		// client would cut the fetch off in the middle of the download, and the
		// error would read like a network fault.
		httpc = &http.Client{Timeout: 30 * time.Minute}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, fmt.Errorf("engines: artefact request: %w", err)
	}
	res, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("engines: artefact %s is not reachable: %w", raw, err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("engines: artefact %s answers %s", raw, res.Status)
	}
	// One byte past the cap: reading the limit exactly cannot tell a file that
	// is exactly the cap from one that is longer.
	body, err := io.ReadAll(io.LimitReader(counted(res.Body, res.ContentLength, watch), limit+1))
	if err != nil {
		return nil, fmt.Errorf("engines: artefact %s: %w", raw, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("engines: artefact %s is over the %d byte cap", raw, limit)
	}
	return body, nil
}

// Progress is how far an install has got. Bytes are read so far, BytesTotal is
// what the artefact announced — 0 when it announced nothing, which is the same
// statement the runner's own phases make: an unknown end is not a zero end.
//
// Done is the last word of one install: from here the figures are a result and
// no longer a snapshot.
type Progress struct {
	Detail     string
	Bytes      int64
	BytesTotal int64
	Done       bool
}

// progressEvery is how often a download says something. It is the runner's own
// figure by intent: a step that reports sixty times a second is a step nobody
// reads, and the control plane drops anything younger than this anyway.
const progressEvery = 500 * time.Millisecond

// counted wraps a reader and reports the bytes going through it, at most once
// per progressEvery. Total 0 is carried through to the watch, so a server that
// sends no Content-Length shows a running figure without a false 0 %.
func counted(r io.Reader, total int64, watch func(Progress)) io.Reader {
	if watch == nil {
		return r
	}
	return &countReader{r: r, total: total, watch: watch, next: time.Now().Add(progressEvery)}
}

type countReader struct {
	r     io.Reader
	total int64
	got   int64
	watch func(Progress)
	next  time.Time
}

func (c *countReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.got += int64(n)
	// The first report comes whatever the clock says — a step that stays silent
	// until a timer fires looks stuck in exactly the window somebody is watching.
	if err != nil || time.Now().After(c.next) {
		c.next = time.Now().Add(progressEvery)
		c.watch(Progress{Bytes: c.got, BytesTotal: c.total})
	}
	return n, err
}

// FileCache keeps the last good copy of the catalogue on the runner's disk.
//
// The other catalogues live in Postgres because the control plane reads them and
// the control plane has a database. A runner deliberately has none — that is the
// point of spec/16 — so the cache has to be a file. Losing the last good copy on
// a restart would mean that a runner whose catalogue host is down can start
// nothing new, which is the failure the cache exists to prevent.
//
// The fetch time is the file's mtime: no sidecar, no metadata to fall out of
// step with the body it describes.
type FileCache struct {
	Dir string
}

// Load returns the stored copy of one URL and when it was fetched. No entry is
// (nil, zero, nil) — an absent cache is not an error.
func (c *FileCache) Load(ctx context.Context, url string) ([]byte, time.Time, error) {
	if c == nil || c.Dir == "" {
		return nil, time.Time{}, nil
	}
	path := c.path(url)
	st, err := os.Stat(path)
	if err != nil {
		return nil, time.Time{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, time.Time{}, nil
	}
	return body, st.ModTime(), nil
}

// Save stores the copy. A failure here is not fatal and is not reported as one:
// the copy in memory still stands, and the next start will try again.
func (c *FileCache) Save(ctx context.Context, url string, body []byte, at time.Time) error {
	if c == nil || c.Dir == "" {
		return nil
	}
	if err := os.MkdirAll(c.Dir, 0o755); err != nil {
		return err
	}
	tmp := c.path(url) + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if !at.IsZero() {
		_ = os.Chtimes(tmp, at, at)
	}
	return os.Rename(tmp, c.path(url))
}

// FileCacheFor is the cache beside a runner's or a control plane's data
// directory: one directory per installation, named after what it holds.
func FileCacheFor(dir string) *FileCache {
	return &FileCache{Dir: filepath.Join(dir, "engine-catalog")}
}

func (c *FileCache) path(url string) string {
	sum := sha256.Sum256([]byte(url))
	return filepath.Join(c.Dir, hex.EncodeToString(sum[:])+".json")
}
