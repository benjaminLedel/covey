package marketplace

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Feed is one JSON document behind one URL, fetched with a cache — the
// mechanism the catalogue is built on, without the knowledge of what is in it.
//
// It exists because the catalogue turned out to be a shape rather than a
// feature: the plugins are one such document (spec/22), the workplaces are
// another (spec/16 — which image an agent works in). Both want the same four
// properties, and each of them is a lesson somebody paid for once:
//
//   - ONE plain GET of ONE file, so that GitHub raw, GitLab raw, an S3 bucket,
//     an internal nginx and a file:// path are all the same case.
//   - The last good copy survives a restart (Cache), because a store page that
//     is EMPTY while a foreign server is down looks like a fault here.
//   - Stale is served immediately and refreshed behind the page: nobody opening
//     a page waits on a server somewhere on the internet.
//   - A failed refresh is reported ALONGSIDE the copy, never instead of it, so
//     the page can say "from 11:04, last refresh failed" rather than showing
//     old data as though it were current.
//
// What it deliberately does not do is decide anything. It fetches and hands the
// document over; installing, resolving and using are the caller's, and nothing
// here ever acts on its own.
type Feed[T any] struct {
	URL   string
	HTTP  *http.Client
	Store Cache
	Log   *slog.Logger
	// Limit caps the document; 0 = maxCatalog.
	Limit int64
	// TTL is how long a fetched copy is served without asking again; 0 = cacheTTL.
	TTL time.Duration
	// Parse turns the bytes into the document. It is also the validation: a
	// document this build cannot read must fail here, not halfway through use.
	Parse func([]byte) (*T, error)
	// Name prefixes log lines, so two feeds are distinguishable in a log.
	Name string

	mu         sync.Mutex
	cached     *T
	fetched    time.Time
	refreshing bool
	loaded     bool
	lastErr    error
	// ownFetch: has THIS process fetched, or is it serving what a previous one
	// left behind? The stored copy stays usable — it is what keeps a start
	// without network from having nothing at all — but it does not count as
	// fresh. A restart therefore fetches, always.
	//
	// That is the lever an operator already has. Without it a corrected
	// catalogue took up to the TTL to arrive and nothing could say "now": no
	// endpoint refreshed the feed, restarting did not, and the only remaining
	// knob was an environment variable per profile on the host (#117).
	ownFetch bool
}

// Enabled: is a URL configured at all?
func (f *Feed[T]) Enabled() bool { return f != nil && strings.TrimSpace(f.URL) != "" }

func (f *Feed[T]) limit() int64 {
	if f.Limit > 0 {
		return f.Limit
	}
	return maxCatalog
}

func (f *Feed[T]) ttl() time.Duration {
	if f.TTL > 0 {
		return f.TTL
	}
	return cacheTTL
}

func (f *Feed[T]) name() string {
	if f.Name != "" {
		return f.Name
	}
	return "marketplace"
}

// Get returns the document, from the cache when it is fresh enough.
func (f *Feed[T]) Get(ctx context.Context) (*T, time.Time, error) {
	if !f.Enabled() {
		return nil, time.Time{}, ErrDisabled
	}
	f.loadStored(ctx)

	f.mu.Lock()
	cached, fetched, lastErr := f.cached, f.fetched, f.lastErr
	fresh := cached != nil && f.ownFetch && time.Since(fetched) < f.ttl()
	f.mu.Unlock()

	if fresh {
		return cached, fetched, nil
	}
	if cached != nil {
		f.refreshInBackground()
		return cached, fetched, lastErr
	}

	// Nothing at all: this one has to wait.
	doc, body, err := f.fetch(ctx)
	if err != nil {
		f.mu.Lock()
		f.lastErr = err
		f.mu.Unlock()
		return nil, time.Time{}, err
	}
	return f.adopt(ctx, doc, body), f.fetched, nil
}

func (f *Feed[T]) fetch(ctx context.Context) (*T, []byte, error) {
	body, err := fetchBytes(ctx, f.HTTP, f.URL, f.limit())
	if err != nil {
		return nil, nil, err
	}
	doc, err := f.Parse(body)
	if err != nil {
		return nil, nil, err
	}
	return doc, body, nil
}

// adopt takes a freshly fetched document into the cache (memory and store).
func (f *Feed[T]) adopt(ctx context.Context, doc *T, body []byte) *T {
	now := time.Now()
	f.mu.Lock()
	f.cached, f.fetched, f.lastErr = doc, now, nil
	f.ownFetch = true
	f.mu.Unlock()
	if f.Store != nil {
		// Deliberately not ctx: the request that triggered this may be done by
		// now, and losing the cache write because the browser disconnected
		// would be a silly way to keep waking a foreign host.
		saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := f.Store.Save(saveCtx, f.URL, body, now); err != nil && f.Log != nil {
			f.Log.Warn(f.name()+": document not cached", "err", err)
		}
	}
	return doc
}

// loadStored fills the memory cache from the persistent one, once per process.
func (f *Feed[T]) loadStored(ctx context.Context) {
	f.mu.Lock()
	if f.loaded || f.Store == nil {
		f.loaded = true
		f.mu.Unlock()
		return
	}
	f.loaded = true
	f.mu.Unlock()

	body, at, err := f.Store.Load(ctx, f.URL)
	if err != nil || len(body) == 0 {
		if err != nil && f.Log != nil {
			f.Log.Warn(f.name()+": cached document not readable", "err", err)
		}
		return
	}
	doc, err := f.Parse(body)
	if err != nil {
		// A cached document this build can no longer read (an older schema,
		// say) is not an error to report — it is simply not usable, and the
		// next fetch replaces it.
		return
	}
	f.mu.Lock()
	if f.cached == nil {
		// With its original timestamp, so the age shown is the age of the
		// document and not of this process. It stays stale by definition
		// (ownFetch is false) — the first Get hands it out and refreshes
		// behind it.
		f.cached, f.fetched = doc, at
	}
	f.mu.Unlock()
}

// refreshInBackground refreshes without holding anybody up. At most one refresh
// runs at a time — a page opened by five people produces one request to the
// foreign host, not five.
func (f *Feed[T]) refreshInBackground() {
	f.mu.Lock()
	if f.refreshing {
		f.mu.Unlock()
		return
	}
	f.refreshing = true
	f.mu.Unlock()

	go func() {
		defer func() {
			f.mu.Lock()
			f.refreshing = false
			f.mu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		doc, body, err := f.fetch(ctx)
		if err != nil {
			f.mu.Lock()
			f.lastErr = err
			f.mu.Unlock()
			if f.Log != nil {
				f.Log.Warn(f.name()+": refresh failed — serving the last good copy",
					"url", f.URL, "err", err)
			}
			return
		}
		f.adopt(ctx, doc, body)
	}()
}

// fetchBytes reads one document. file:// is supported on purpose: an air-gapped
// installation serves its catalogue from a mounted path, and that must not be a
// second code path.
func fetchBytes(ctx context.Context, httpc *http.Client, raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("marketplace: %q is not a URL: %w", raw, err)
	}
	switch u.Scheme {
	case "file":
		path := u.Path
		if u.Host != "" { // file://./relative — tolerate it rather than read nothing
			path = filepath.Join(u.Host, u.Path)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("marketplace: %w", err)
		}
		if int64(len(body)) > limit {
			return nil, fmt.Errorf("marketplace: %s is larger than %d bytes", raw, limit)
		}
		return body, nil
	case "http", "https":
	default:
		return nil, fmt.Errorf("marketplace: scheme %q is not supported (http, https, file)", u.Scheme)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if httpc == nil {
		httpc = &http.Client{Timeout: fetchTimeout}
	}
	resp, err := httpc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("marketplace: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("marketplace: GET %s: HTTP %d", raw, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("marketplace: %w", err)
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("marketplace: %s is larger than %d bytes", raw, limit)
	}
	return body, nil
}
