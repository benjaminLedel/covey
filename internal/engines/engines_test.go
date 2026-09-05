package engines

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"covey/internal/marketplace"
)

// doc is a catalogue document as a publisher would write it.
const doc = `{
  "schema": 1,
  "generated_at": "2026-01-01T00:00:00Z",
  "engines": [
    {"name": "sevencode", "versions": [
      {"version": "1.0.7", "kind": "npm", "package": "sevencode",
       "notes": "the version the adapter was read against"},
      {"version": "1.0.8", "kind": "npm", "package": "sevencode"}
    ]},
    {"name": "claude-code", "versions": [
      {"version": "2.1.0", "kind": "npm", "package": "@anthropic-ai/claude-code",
       "binary_env": "COVEY_CLAUDE_BIN"}
    ]},
    {"name": "other", "versions": [
      {"version": "9.9.9", "kind": "tarball", "url": "file:///tmp/other.tgz",
       "integrity": "sha256:0000000000000000000000000000000000000000000000000000000000000000"}
    ]}
  ]
}`

func parseDoc(t *testing.T, body string) *Catalog {
	t.Helper()
	c, err := parse([]byte(body))
	if err != nil {
		t.Fatalf("a sound document must parse: %v", err)
	}
	return c
}

func TestParseRefusesWhatCannotBeInstalled(t *testing.T) {
	for name, body := range map[string]string{
		"schema":       `{"schema": 2, "engines": []}`,
		"no version":   `{"schema": 1, "engines": [{"name":"x","versions":[{"kind":"npm","package":"x"}]}]}`,
		"no integrity": `{"schema": 1, "engines": [{"name":"x","versions":[{"version":"1","kind":"tarball","url":"https://e/x.tgz"}]}]}`,
		"no package":   `{"schema": 1, "engines": [{"name":"x","versions":[{"version":"1","kind":"npm"}]}]}`,
		"kind":         `{"schema": 1, "engines": [{"name":"x","versions":[{"version":"1","kind":"git-clone","url":"https://e/x"}]}]}`,
	} {
		if _, err := parse([]byte(body)); err == nil {
			t.Fatalf("%s: accepted, and the first wake would find out", name)
		}
	}
	if _, err := parse([]byte(`{"schema":1,"engines":[{"name":"","versions":[]}]}`)); err == nil {
		t.Fatal("an entry without a name cannot be matched to an engine")
	}
}

func TestReleaseResolution(t *testing.T) {
	cat := parseDoc(t, doc)

	// Newest last, so the last entry is what an unpinned instance gets.
	r, ok := cat.Release("sevencode", "")
	if !ok || r.Version != "1.0.8" {
		t.Fatalf("the last release wins: %+v %v", r, ok)
	}
	if r, _ := cat.Release("sevencode", "1.0.7"); r.Version != "1.0.7" {
		t.Fatal("an instance that pins a version gets that version, not the newest")
	}
	if _, ok := cat.Release("sevencode", "1.0.9"); ok {
		t.Fatal("a pinned version the catalogue does not carry is not silently the newest one")
	}
	// Nothing to say is not an error: the image carries the engine then.
	if _, ok := cat.Release("codex", ""); ok {
		t.Fatal("an engine the catalogue does not list must not resolve")
	}
	// The convention, and the case where it does not hold: the adapter for
	// claude-code reads COVEY_CLAUDE_BIN, so its entry spells it out.
	if got := r.BinaryEnvName("sevencode"); got != "COVEY_SEVENCODE_BIN" {
		t.Fatalf("convention: %s", got)
	}
	cc, _ := cat.Release("claude-code", "")
	if got := cc.BinaryEnvName("claude-code"); got != "COVEY_CLAUDE_BIN" {
		t.Fatalf("an engine whose variable is not derivable has to state it, got %s", got)
	}
	if err := cc.Valid(); err != nil {
		t.Fatalf("a sound npm entry: %v", err)
	}
}

func TestVerifyNamesBothSides(t *testing.T) {
	body := []byte("an engine")
	sum, err := sha256Hex(body), error(nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(body, "sha256:"+sum); err != nil {
		t.Fatalf("a matching digest must pass: %v", err)
	}
	err = Verify(body, "sha256:0000000000000000000000000000000000000000000000000000000000000000")
	if err == nil || !strings.Contains(err.Error(), sum) {
		t.Fatalf("a mismatch has to say what arrived, got %v", err)
	}
}

// tarball builds a .tgz with a plausible engine layout, plus one escaping entry
// if escape is not empty.
func tarball(t *testing.T, escape string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	add := func(name, body string, mode int64) {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)),
			Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	add("bin/sevencode", "#!/bin/sh\necho ok\n", 0o755)
	add("lib/node_modules/sevencode/package.json", `{"version":"1.0.8"}`, 0o644)
	if escape != "" {
		add(escape, "nope", 0o644)
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// An install that takes a minute has to say what it is doing while it does it.
// The byte figures are half of it — "starting" was the whole answer for the image
// pull too, until that learned to count — but the end is the other half: a phase
// that never ends is worse than no phase, because it claims work that is already
// finished. And a cache hit has to end too, in the same millisecond it started.
func TestEnsureWatchedTellsWhereItGotTo(t *testing.T) {
	dir := t.TempDir()
	art := tarball(t, "")
	path := filepath.Join(dir, "engine.tgz")
	if err := os.WriteFile(path, art, 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := parseDoc(t, doc).Release("other", "")
	r.URL = "file://" + path
	r.Integrity = sha256Hex(art)
	r.engine = "sevencode"
	r.Version = "1.0.8"
	store := &Store{Dir: filepath.Join(dir, "engines")}

	var seen []Progress
	if _, err := store.EnsureWatched(context.Background(), r,
		func(p Progress) { seen = append(seen, p) }); err != nil {
		t.Fatalf("install: %v", err)
	}
	if len(seen) < 2 {
		t.Fatalf("an install of %d bytes reported %d times: %+v", len(art), len(seen), seen)
	}
	if !strings.Contains(seen[0].Detail, "sevencode") || !strings.Contains(seen[0].Detail, "1.0.8") {
		t.Fatalf("the first report names what is being installed, got %q", seen[0].Detail)
	}
	if !seen[len(seen)-1].Done {
		t.Fatalf("the last report has to end the phase: %+v", seen[len(seen)-1])
	}
	var bekannt bool
	for _, p := range seen {
		if p.Bytes == int64(len(art)) && p.BytesTotal == int64(len(art)) {
			bekannt = true
		}
	}
	if !bekannt {
		t.Fatalf("no report carries the artefact's size on both sides (%d bytes): %+v", len(art), seen)
	}

	// The layer is there now. One report, ending at once — a step the display
	// shows running for a second after it finished is a step nobody believes the
	// next time.
	seen = nil
	if _, err := store.EnsureWatched(context.Background(), r,
		func(p Progress) { seen = append(seen, p) }); err != nil {
		t.Fatalf("second install: %v", err)
	}
	if len(seen) != 1 || !seen[0].Done {
		t.Fatalf("a layer already standing is one ending report, got %+v", seen)
	}
}

func TestStoreInstallsVerifiesAndMarks(t *testing.T) {
	dir := t.TempDir()
	art := tarball(t, "")
	path := filepath.Join(dir, "engine.tgz")
	if err := os.WriteFile(path, art, 0o644); err != nil {
		t.Fatal(err)
	}
	r, _ := parseDoc(t, doc).Release("other", "")
	r.URL = "file://" + path
	r.Integrity = sha256Hex(art)
	r.engine = "sevencode"
	r.Version = "1.0.8"

	store := &Store{Dir: filepath.Join(dir, "engines")}
	l, err := store.Ensure(context.Background(), r)
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if _, err := os.Stat(l.Exec); err != nil {
		t.Fatalf("the layer's binary is not where the marker says: %v", err)
	}
	if !strings.HasPrefix(l.RelExec, "bin/") {
		t.Fatalf("the marker stores the path relative to the layer, got %q", l.RelExec)
	}
	if _, err := os.Stat(filepath.Join(l.Root, markerFile)); err != nil {
		t.Fatal("a layer without a marker is a crashed install, not an engine")
	}
	again, err := store.Ensure(context.Background(), r)
	if err != nil || again.Root != l.Root {
		t.Fatalf("an installed layer is not fetched twice: %v", err)
	}
	if _, ok := store.Lookup("sevencode", "1.0.8"); !ok {
		t.Fatal("a start must not need the network for an engine that is already here")
	}

	// What a sandbox is given: the container path, not the store path.
	host, container := ContainerMount(l)
	if host != l.Root || container != "/opt/engines/sevencode/1.0.8" {
		t.Fatalf("mount %s -> %s", host, container)
	}
	env := ContainerEnv(l, r)
	if len(env) != 1 || env[0] != "COVEY_SEVENCODE_BIN=/opt/engines/sevencode/1.0.8/bin/sevencode" {
		t.Fatalf("the adapter's variable must point inside the container: %v", env)
	}

	// A digest that does not match is refused before anything is unpacked.
	bad := r
	bad.Version = "2.0.0"
	bad.Integrity = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := store.Ensure(context.Background(), bad); err == nil ||
		!strings.Contains(err.Error(), "integrity mismatch") {
		t.Fatalf("an artefact that is not what was promised: %v", err)
	}
	if _, ok := store.Lookup("sevencode", "2.0.0"); ok {
		t.Fatal("a refused install leaves no layer behind")
	}

	// And an archive that reaches out of its layer is refused at all.
	evil := tarball(t, "../escaped")
	evilPath := filepath.Join(dir, "evil.tgz")
	if err := os.WriteFile(evilPath, evil, 0o644); err != nil {
		t.Fatal(err)
	}
	ev := r
	ev.Version = "3.0.0"
	ev.URL = "file://" + evilPath
	ev.Integrity = sha256Hex(evil)
	if _, err := store.Ensure(context.Background(), ev); err == nil ||
		!strings.Contains(err.Error(), "outside the layer") {
		t.Fatalf("a traversal entry must be refused: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "escaped")); err == nil {
		t.Fatal("the escape wrote next to the store")
	}
}

func TestFileCacheSurvivesARestart(t *testing.T) {
	dir := t.TempDir()
	c := FileCacheFor(dir)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	var body []byte = []byte(doc)
	if err := c.Save(context.Background(), "https://example/cat.json", body, at); err != nil {
		t.Fatal(err)
	}
	got, when, err := c.Load(context.Background(), "https://example/cat.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) || !when.Equal(at) {
		t.Fatalf("the last good copy has to come back with its fetch time: %v", when)
	}
	if _, _, err := c.Load(context.Background(), "https://other/cat.json"); err != nil {
		t.Fatalf("an absent entry is not an error: %v", err)
	}
	// It is the marketplace's cache interface, so the same feed serves it.
	var _ marketplace.Cache = c
}

func TestSourceWithoutAURLIsSilent(t *testing.T) {
	s := NewSource("", nil, nil)
	if s.Enabled() {
		t.Fatal("no URL means the mechanism is off")
	}
	if _, _, err := s.Catalog(context.Background()); !errors.Is(err, marketplace.ErrDisabled) {
		t.Fatalf("expected ErrDisabled, got %v", err)
	}
	if _, ok := s.For(context.Background(), "sevencode", ""); ok {
		t.Fatal("nothing to say must not resolve to something")
	}
}

func TestSourceServesTheStoredCopyOffline(t *testing.T) {
	dir := t.TempDir()
	cache := FileCacheFor(dir)
	// The cache is keyed by URL, so the stored copy has to be filed under the
	// very URL this source will be pointed at.
	url := "file://" + filepath.Join(dir, "missing.json")
	if err := cache.Save(context.Background(), url, []byte(doc), time.Now()); err != nil {
		t.Fatal(err)
	}
	// The URL is unreachable by construction; the stored copy still stands, and
	// a runner whose catalogue host is down installs what it already knows.
	s := NewSource(url, cache, nil)
	cat, _, err := s.Catalog(context.Background())
	if cat == nil {
		t.Fatalf("an unreachable catalogue must not mean no engines at all: %v", err)
	}
	if r, ok := s.For(context.Background(), "sevencode", ""); !ok || r.Version != "1.0.8" {
		t.Fatalf("the stored copy should still answer: %+v %v %v", r, ok, err)
	}
}

func TestContainerEnvCannotBeContradicted(t *testing.T) {
	l := Layer{Engine: "sevencode", Version: "1.0.8", RelExec: "bin/sevencode"}
	r, _ := parseDoc(t, doc).Release("sevencode", "")
	r.Env = []string{"COVEY_SEVENCODE_BIN=/somewhere/else", "SEVENCODE_API_BASE=https://educa.example"}
	env := ContainerEnv(l, r)
	if len(env) != 2 || env[0] != "COVEY_SEVENCODE_BIN=/opt/engines/sevencode/1.0.8/bin/sevencode" {
		t.Fatalf("an entry may not bury the path the layer provides: %v", env)
	}
	if env[1] != "SEVENCODE_API_BASE=https://educa.example" {
		t.Fatalf("what the entry declares has to arrive: %v", env)
	}
}

func TestSafeSegmentKeepsNamesInOneDirectory(t *testing.T) {
	for in, want := range map[string]string{
		"claude-code": "claude-code",
		"1.0.7":       "1.0.7",
		"../../etc":   "_.._etc",
		"":            "_",
	} {
		if got := safeSegment(in); got != want {
			t.Fatalf("safeSegment(%q) = %q, want %q", in, got, want)
		}
	}
	// A hostile name must not be able to leave the store root.
	s := &Store{Dir: t.TempDir()}
	root := s.LayerRoot("../../escape", "1")
	if !strings.HasPrefix(root, s.Dir) {
		t.Fatalf("layer root escaped the store: %s", root)
	}
}

func TestMarkerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := marker{Engine: "sevencode", Version: "1.0.8", Kind: KindTarball,
		Executable: "bin/sevencode", InstalledAt: time.Now().UTC()}
	body, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, markerFile)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatal(err)
	}
	back, err := readMarker(p)
	if err != nil || back.Executable != m.Executable {
		t.Fatalf("marker round trip: %v %+v", err, back)
	}
	// Garbage is not "not installed" but an error the caller can log.
	if err := os.WriteFile(p, []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readMarker(p); err == nil {
		t.Fatal("a broken marker must not read as an engine")
	}
}
