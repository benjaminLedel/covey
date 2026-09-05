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
func fetchArtifact(ctx context.Context, httpc *http.Client, raw string, limit int64) ([]byte, error) {
	if strings.HasPrefix(raw, "file://") {
		body, err := os.ReadFile(strings.TrimPrefix(raw, "file://"))
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
	body, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("engines: artefact %s: %w", raw, err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("engines: artefact %s is over the %d byte cap", raw, limit)
	}
	return body, nil
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
