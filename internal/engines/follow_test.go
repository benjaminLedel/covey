package engines

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// publisher is a vendor that serves its CLI behind `latest` and announces the
// current release beside it — the shape SevenCode has, and the one `follow` is
// for. Down makes both endpoints answer 503; lie makes the announcement name a
// digest the bytes do not have.
type publisher struct {
	mu      sync.Mutex
	version string
	body    []byte
	down    bool
	lie     bool
	asked   int
	token   string
}

func (p *publisher) release(version string, body []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.version, p.body = version, body
}

func (p *publisher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		http.Error(w, "down", http.StatusServiceUnavailable)
		return
	}
	if p.token != "" && r.Header.Get("Authorization") != "Bearer "+p.token {
		http.Error(w, "no", http.StatusUnauthorized)
		return
	}
	switch r.URL.Path {
	case "/cli/version":
		p.asked++
		sum := sha256Hex(p.body)
		if p.lie {
			sum = sha256Hex([]byte("something else"))
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"version": p.version, "sha256": sum, "notes": "ignored"})
	case "/cli/latest":
		_, _ = w.Write(p.body)
	default:
		http.NotFound(w, r)
	}
}

func cli(v string) []byte {
	return []byte("#!/usr/bin/env node\nconsole.log(" + fmt.Sprintf("%q", v) + ")\n")
}

// followed release as the catalogue would carry it: its own version is the floor.
func followRelease(base string, floor []byte) Release {
	return Release{engine: "tool", Version: "1.0.0", Kind: KindFile, URL: base + "/cli/latest",
		Integrity: "sha256:" + sha256Hex(floor), Binary: "bin/tool",
		AuthHeader: "Authorization", AuthEnv: "COVEY_TEST_FOLLOW_TOKEN",
		Follow: &Follow{URL: base + "/cli/version"}}
}

func installed(t *testing.T, l Layer, want []byte) {
	t.Helper()
	got, err := os.ReadFile(l.Exec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("the layer holds %q, expected %q", got, want)
	}
}

func TestFollowInstallsWhatThePublisherAnnounces(t *testing.T) {
	pub := &publisher{token: "tok"}
	pub.release("1.0.2", cli("1.0.2"))
	srv := httptest.NewServer(pub)
	defer srv.Close()
	store := &Store{Dir: t.TempDir()}

	l, err := store.Ensure(context.Background(), followRelease(srv.URL, cli("1.0.0")), Auth{Secret: "tok"})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if l.Version != "1.0.2" {
		t.Fatalf("installed %s; the publisher announces 1.0.2", l.Version)
	}
	installed(t, l, cli("1.0.2"))
	// The run is recorded against the version it ran, so the layer's own release
	// says it too — ContainerEnv and the mount path are derived from it.
	if l.Release.Version != "1.0.2" || !strings.HasSuffix(ContainerRoot(l.Engine, l.Version), "/tool/1.0.2") {
		t.Fatalf("the layer describes itself as %s at %s", l.Release.Version, ContainerRoot(l.Engine, l.Version))
	}
}

// Asking on every wake would be a question per start to somebody else's server.
func TestFollowAsksOncePerFreshnessWindow(t *testing.T) {
	pub := &publisher{}
	pub.release("1.0.2", cli("1.0.2"))
	srv := httptest.NewServer(pub)
	defer srv.Close()
	store := &Store{Dir: t.TempDir()}
	rel := followRelease(srv.URL, cli("1.0.0"))
	t.Setenv("COVEY_TEST_FOLLOW_TOKEN", "any")

	for i := 0; i < 3; i++ {
		if _, err := store.Ensure(context.Background(), rel, Auth{}); err != nil {
			t.Fatal(err)
		}
	}
	if pub.asked != 1 {
		t.Fatalf("asked %d times within the window, expected once", pub.asked)
	}
}

func TestFollowPicksUpANewRelease(t *testing.T) {
	pub := &publisher{}
	pub.release("1.0.2", cli("1.0.2"))
	srv := httptest.NewServer(pub)
	defer srv.Close()
	store := &Store{Dir: t.TempDir()}
	rel := followRelease(srv.URL, cli("1.0.0"))
	t.Setenv("COVEY_TEST_FOLLOW_TOKEN", "any")
	if _, err := store.Ensure(context.Background(), rel, Auth{}); err != nil {
		t.Fatal(err)
	}

	pub.release("1.0.3", cli("1.0.3"))
	age(t, store, rel)
	l, err := store.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatal(err)
	}
	if l.Version != "1.0.3" {
		t.Fatalf("still on %s after the publisher released 1.0.3", l.Version)
	}
	installed(t, l, cli("1.0.3"))
}

// The address is `latest`: between the announcement this host holds and the
// download, the publisher may have moved on. One fresh question settles it.
func TestFollowReasksWhenLatestMovedUnderTheAnnouncement(t *testing.T) {
	pub := &publisher{}
	pub.release("1.0.2", cli("1.0.2"))
	srv := httptest.NewServer(pub)
	defer srv.Close()
	store := &Store{Dir: t.TempDir()}
	rel := followRelease(srv.URL, cli("1.0.0"))
	t.Setenv("COVEY_TEST_FOLLOW_TOKEN", "any")

	// An announcement still fresh, of a release no longer behind `latest`.
	store.saveFollowed(rel, followed{Announced: &pin{Version: "1.0.1", Integrity: "sha256:" + sha256Hex(cli("1.0.1"))},
		AskedAt: time.Now().UTC()})
	l, err := store.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if l.Version != "1.0.2" {
		t.Fatalf("installed %s, expected the release behind latest", l.Version)
	}
}

// A publisher outage must not stop an agent that has started before.
func TestFollowSurvivesASilentPublisher(t *testing.T) {
	pub := &publisher{}
	pub.release("1.0.2", cli("1.0.2"))
	srv := httptest.NewServer(pub)
	defer srv.Close()
	store := &Store{Dir: t.TempDir()}
	rel := followRelease(srv.URL, cli("1.0.0"))
	t.Setenv("COVEY_TEST_FOLLOW_TOKEN", "any")
	if _, err := store.Ensure(context.Background(), rel, Auth{}); err != nil {
		t.Fatal(err)
	}

	pub.down = true
	age(t, store, rel)
	// A restart in between: nothing in memory, only the disk.
	again := &Store{Dir: store.Dir}
	l, err := again.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("a silent publisher stopped a host that has the engine: %v", err)
	}
	if l.Version != "1.0.2" {
		t.Fatalf("running %s, expected the release this host has", l.Version)
	}
}

// A new release that cannot be verified leaves the last good one running.
func TestFollowKeepsTheLastGoodReleaseWhenTheNewOneFails(t *testing.T) {
	pub := &publisher{}
	pub.release("1.0.2", cli("1.0.2"))
	srv := httptest.NewServer(pub)
	defer srv.Close()
	store := &Store{Dir: t.TempDir()}
	rel := followRelease(srv.URL, cli("1.0.0"))
	t.Setenv("COVEY_TEST_FOLLOW_TOKEN", "any")
	if _, err := store.Ensure(context.Background(), rel, Auth{}); err != nil {
		t.Fatal(err)
	}

	pub.release("1.0.3", cli("1.0.3"))
	pub.lie = true
	age(t, store, rel)
	l, err := store.Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if l.Version != "1.0.2" {
		t.Fatalf("running %s; the unverifiable 1.0.3 must not replace 1.0.2", l.Version)
	}
	installed(t, l, cli("1.0.2"))
	if _, ok := store.Lookup("tool", "1.0.3"); ok {
		t.Fatal("bytes that did not match the announced digest were installed")
	}
}

// A host that has never heard from the publisher has the entry's own version.
func TestFollowFallsBackToTheFloor(t *testing.T) {
	floor := cli("1.0.0")
	dir := t.TempDir()
	path := filepath.Join(dir, "cli")
	if err := os.WriteFile(path, floor, 0o644); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(&publisher{down: true})
	defer srv.Close()
	rel := followRelease(srv.URL, floor)
	rel.URL = "file://" + path
	t.Setenv("COVEY_TEST_FOLLOW_TOKEN", "any")

	l, err := (&Store{Dir: filepath.Join(dir, "engines")}).Ensure(context.Background(), rel, Auth{})
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if l.Version != "1.0.0" {
		t.Fatalf("installed %s, expected the entry's own version", l.Version)
	}
}

func TestFollowRefusesAnAnnouncementItCannotInstall(t *testing.T) {
	for name, doc := range map[string]string{
		"a version that is a path": `{"version":"../../etc","sha256":"` + sha256Hex(cli("x")) + `"}`,
		"no version":               `{"sha256":"` + sha256Hex(cli("x")) + `"}`,
		"a digest that is not one": `{"version":"1.0.9","sha256":"abc"}`,
		"not JSON":                 `<html>moved</html>`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(doc))
			}))
			defer srv.Close()
			rel := followRelease(srv.URL, cli("1.0.0"))
			if _, err := (&Store{}).announcement(context.Background(), rel, Auth{Secret: "t"}); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

func TestFollowIsRefusedWhereNoDigestIsChecked(t *testing.T) {
	r := Release{engine: "tool", Version: "1", Kind: KindNpm, Package: "tool", Follow: &Follow{URL: "https://example.org/v"}}
	if err := r.Valid(); err == nil {
		t.Fatal("an npm release cannot follow: the package manager fetches, not this process")
	}
	r = Release{engine: "tool", Version: "1", Kind: KindFile, URL: "https://example.org/f",
		Integrity: "sha256:" + sha256Hex(cli("1")), Follow: &Follow{URL: "file:///etc/passwd"}}
	if err := r.Valid(); err == nil {
		t.Fatal("follow has to name an address to ask")
	}
}

// age makes the host's announcement older than the freshness window.
func age(t *testing.T, s *Store, r Release) {
	t.Helper()
	rec := s.loadFollowed(r)
	rec.AskedAt = time.Now().Add(-2 * followFresh)
	s.saveFollowed(r, rec)
}
