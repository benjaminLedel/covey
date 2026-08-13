package marketplace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

const artifact = `{"name":"redmine","webhook":{"id_field":"issue.id"},` +
	`"actions":{"get":{"method":"GET","path":"/issues/{id}.json"}}}`

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// serve puts a catalogue and an artefact behind a test server. artifactBody is
// what /redmine.json answers with — a test can hand back something other than
// what the catalogue pins, which is the case the whole design is about.
func serve(t *testing.T, artifactBody string, pinned string) (*Client, *httptest.Server, *int32) {
	t.Helper()
	var hits int32
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cat := Catalog{Schema: 1, GeneratedAt: "2026-08-13T09:00:00Z", Plugins: []Entry{{
		Name: "redmine", Label: "Redmine", Kind: "custom", Category: "ticketing",
		Publisher: "example", Homepage: "https://example.invalid", License: "MIT",
		Versions: []Version{
			{Version: "1.1.0", URL: srv.URL + "/redmine.json", SHA256: pinned},
			{Version: "1.0.0", URL: srv.URL + "/old.json", SHA256: digestOf("{}")},
		},
	}, {
		Name: "zammad", Label: "Zammad", Kind: "builtin", Category: "ticketing",
		Publisher: "covey", Homepage: "https://example.invalid", License: "AGPL-3.0-only",
		BuiltinSince: "0.1.0",
	}}}
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		json.NewEncoder(w).Encode(cat)
	})
	mux.HandleFunc("/redmine.json", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(artifactBody))
	})

	c := New(srv.URL + "/catalog.json")
	c.HTTP = srv.Client()
	return c, srv, &hits
}

func TestCatalogAndEntry(t *testing.T) {
	c, _, hits := serve(t, artifact, digestOf(artifact))
	cat, fetched, err := c.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Plugins) != 2 {
		t.Fatalf("plugins = %d, want 2", len(cat.Plugins))
	}
	if fetched.IsZero() {
		t.Error("the fetch time belongs in the answer — a store page has to be able to say how old this is")
	}

	// Second call comes from the cache: rendering a store page must not hit a
	// foreign host every time.
	if _, _, err := c.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(hits); got != 1 {
		t.Errorf("catalogue fetched %d times, want 1 (cache)", got)
	}

	e, err := c.Entry(context.Background(), "redmine")
	if err != nil {
		t.Fatal(err)
	}
	v, ok := e.Latest()
	if !ok || v.Version != "1.1.0" {
		t.Errorf("Latest = %q, want 1.1.0 (the catalogue lists newest first)", v.Version)
	}
	if _, ok := e.Find("1.0.0"); !ok {
		t.Error("an older version has to remain installable — it is the way back when a new one misbehaves")
	}
	if _, err := c.Entry(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown plugin: err = %v, want ErrNotFound", err)
	}
}

func TestArtifactVerifiesTheDigest(t *testing.T) {
	c, _, _ := serve(t, artifact, digestOf(artifact))
	e, _ := c.Entry(context.Background(), "redmine")
	v, _ := e.Latest()
	body, err := c.Artifact(context.Background(), v)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != artifact {
		t.Error("the artefact should be handed over unchanged")
	}
}

func TestArtifactRefusesAChangedArtefact(t *testing.T) {
	// The case the whole arrangement exists for: the third-party repository
	// serves something else than what the entry pins. It must fail loudly, not
	// silently become something else.
	c, _, _ := serve(t, `{"name":"redmine","actions":{"evil":{"method":"GET","path":"/x"}}}`, digestOf(artifact))
	e, _ := c.Entry(context.Background(), "redmine")
	v, _ := e.Latest()
	_, err := c.Artifact(context.Background(), v)
	if !errors.Is(err, ErrDigest) {
		t.Fatalf("err = %v, want ErrDigest", err)
	}
	if !strings.Contains(err.Error(), v.SHA256) {
		t.Errorf("the error should name both digests so it can be checked by hand: %v", err)
	}
}

func TestArtifactRefusesAnUnusableDigest(t *testing.T) {
	c, _, _ := serve(t, artifact, "abc")
	_, err := c.Artifact(context.Background(), Version{URL: "http://example.invalid", SHA256: "abc"})
	if !errors.Is(err, ErrDigest) {
		t.Fatalf("err = %v, want ErrDigest", err)
	}
	if c == nil {
		t.Fatal("unreachable")
	}
}

func TestUnknownSchemaIsRefusedNotGuessedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"schema":99,"plugins":[]}`)
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.HTTP = srv.Client()
	_, _, err := c.Catalog(context.Background())
	if err == nil || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("err = %v, want a refusal naming the schema version", err)
	}
}

func TestStaleCatalogueSurvivesAFailedRefresh(t *testing.T) {
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			http.Error(w, "down", http.StatusInternalServerError)
			return
		}
		fmt.Fprint(w, `{"schema":1,"plugins":[{"name":"redmine","kind":"custom"}]}`)
	}))
	defer srv.Close()
	c := New(srv.URL)
	c.HTTP = srv.Client()
	if _, _, err := c.Catalog(context.Background()); err != nil {
		t.Fatal(err)
	}

	// Force the cache to expire, then break the server.
	c.mu.Lock()
	c.fetched = c.fetched.Add(-2 * cacheTTL)
	c.mu.Unlock()
	fail.Store(true)

	cat, _, err := c.Catalog(context.Background())
	if cat == nil || len(cat.Plugins) != 1 {
		t.Fatal("an unreachable catalogue must not empty the store page")
	}
	if err == nil {
		t.Error("…and it must not look healthy either — the error belongs alongside the stale copy")
	}
}

func TestDisabledWithoutAURL(t *testing.T) {
	c := New("")
	if c.Enabled() {
		t.Fatal("an empty URL means the marketplace is off")
	}
	if _, _, err := c.Catalog(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
	if _, err := c.Artifact(context.Background(), Version{}); !errors.Is(err, ErrDisabled) {
		t.Errorf("err = %v, want ErrDisabled", err)
	}
}

func TestFileURLForTheAirGappedCase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "catalog.json")
	if err := os.WriteFile(path, []byte(`{"schema":1,"plugins":[{"name":"local","kind":"custom"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c := New("file://" + path)
	cat, _, err := c.Catalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Plugins) != 1 || cat.Plugins[0].Name != "local" {
		t.Errorf("a mirrored catalogue on disk should read like any other: %+v", cat)
	}
}

func TestUnsupportedSchemeIsRefused(t *testing.T) {
	c := New("ftp://example.invalid/catalog.json")
	if _, _, err := c.Catalog(context.Background()); err == nil || !strings.Contains(err.Error(), "scheme") {
		t.Fatalf("err = %v, want a refusal naming the scheme", err)
	}
}
