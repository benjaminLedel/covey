package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/engines"
)

// The four answers engineLayer can give, and which of them is which: three
// cases where the catalogue has nothing to say (and the image's own engine
// stands, as it did before this file existed) and one where the catalogue named
// this engine, so the layer has to arrive or the start fails with a reason.
//
// The npm kind is not exercised here — it would want a registry. What is
// exercised is the decision and the wiring, with a tarball off disk.
func TestEngineLayerOnlySpeaksWhenTheCatalogueNamesTheEngine(t *testing.T) {
	dir := t.TempDir()
	art := engineTarball(t)
	artPath := filepath.Join(dir, "sevencode.tgz")
	if err := os.WriteFile(artPath, art, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(art)
	catPath := filepath.Join(dir, "engines.json")
	body := []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[` +
		`{"version":"1.0.8","kind":"tarball","url":"file://` + artPath +
		`","integrity":"sha256:` + hex.EncodeToString(sum[:]) + `"}]},` +
		`{"name":"other","versions":[{"version":"1.0.0","kind":"npm","package":"other"}]}]}`)
	if err := os.WriteFile(catPath, body, 0o644); err != nil {
		t.Fatal(err)
	}

	newDocker := func(url string) *Docker {
		return &Docker{
			DataDir:     dir,
			Engines:     engines.NewSource(url, nil, nil),
			EngineStore: &engines.Store{Dir: filepath.Join(dir, "engines")},
		}
	}
	const url = "file://"

	env, mount, err := newDocker("").engineLayer(context.Background(),
		StartSandbox{Engine: "sevencode"})
	if err != nil || env != nil || mount != nil {
		t.Fatalf("no catalogue URL must mean no opinion: %v %v %v", env, mount, err)
	}

	env, mount, err = newDocker("file://"+catPath).engineLayer(context.Background(),
		StartSandbox{})
	if err != nil || env != nil || mount != nil {
		t.Fatalf("a start that names no engine is not this file's business: %v %v %v", env, mount, err)
	}

	env, mount, err = newDocker("file://"+catPath).engineLayer(context.Background(),
		StartSandbox{Engine: "codex"})
	if err != nil || env != nil || mount != nil {
		t.Fatalf("an engine the catalogue does not list must stay silent, not fail: %v %v %v", env, mount, err)
	}

	// The case that acts: a tarball on disk, installed on this host and mounted
	// read-only at the fixed container path.
	p := newDocker("file://" + catPath)
	env, mount, err = p.engineLayer(context.Background(), StartSandbox{Engine: "sevencode"})
	if err != nil {
		t.Fatalf("a catalogue that names the engine has to deliver it: %v", err)
	}
	if len(env) != 1 || env[0] != "COVEY_SEVENCODE_BIN=/opt/engines/sevencode/1.0.8/bin/sevencode" {
		t.Fatalf("the run is told where its engine is: %v", env)
	}
	if len(mount) != 2 || mount[0] != "-v" {
		t.Fatalf("expected a bind mount, got %v", mount)
	}
	parts := strings.Split(mount[1], ":")
	if len(parts) != 3 {
		t.Fatalf("a bind mount is host:container:options, got %q", mount[1])
	}
	host, container := parts[0], parts[1]
	if !strings.HasSuffix(host, filepath.Join("sevencode", "1.0.8")) ||
		container != "/opt/engines/sevencode/1.0.8" || parts[2] != "ro" {
		t.Fatalf("one layer, read-only, at the fixed path: %q", mount[1])
	}
	if _, err := os.Stat(filepath.Join(host, "bin", "sevencode")); err != nil {
		t.Fatalf("the host side of the mount has to be the installed layer: %v", err)
	}

	// An operator who names the binary on this host outranks the catalogue, and
	// nothing is installed for that engine.
	t.Setenv("COVEY_SEVENCODE_BIN", "/usr/local/bin/sevencode")
	env, mount, err = p.engineLayer(context.Background(), StartSandbox{Engine: "sevencode"})
	if err != nil || env != nil || mount != nil {
		t.Fatalf("an explicit local path is left alone: %v %v %v", env, mount, err)
	}
}

// A catalogue entry that promises an artefact which is not there fails the
// start — with the reason, not a fallback onto whatever the image carries.
func TestEngineLayerFailsLoudlyWhenThePromisedLayerIsMissing(t *testing.T) {
	dir := t.TempDir()
	catPath := filepath.Join(dir, "engines.json")
	body := []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[` +
		`{"version":"9.9.9","kind":"tarball","url":"file://` + filepath.Join(dir, "gone.tgz") +
		`","integrity":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}]}`)
	if err := os.WriteFile(catPath, body, 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Docker{
		DataDir:     dir,
		Engines:     engines.NewSource("file://"+catPath, nil, nil),
		EngineStore: &engines.Store{Dir: filepath.Join(dir, "engines")},
	}
	if _, _, err := p.engineLayer(context.Background(), StartSandbox{Engine: "sevencode"}); err == nil ||
		!strings.Contains(err.Error(), "sevencode") {
		t.Fatalf("the start must fail, and say which engine: %v", err)
	}
}

// What the unit tests above decide is one thing; what actually reaches the
// container is another, and that is the seam the whole mechanism exists for.
// So: the same fake docker binary docker_test.go uses, and the two questions a
// start has to answer — is the layer mounted and named in the invocation, and
// does a catalogue that cannot deliver keep the container from being created at
// all (rather than starting on whatever binary the image carries).
func TestDockerStartCarriesTheEngineLayer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	art := engineTarball(t)
	artPath := filepath.Join(dir, "sevencode.tgz")
	if err := os.WriteFile(artPath, art, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(art)
	catPath := filepath.Join(dir, "engines.json")
	write := func(path string, body []byte) {
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(catPath, []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[`+
		`{"version":"1.0.8","kind":"tarball","url":"file://`+artPath+
		`","integrity":"sha256:`+hex.EncodeToString(sum[:])+`"}]}]}`))

	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "docker")
	write(fake, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+argsFile+"\necho containerid\n"))
	if err := os.Chmod(fake, 0o755); err != nil {
		t.Fatal(err)
	}

	agentID := uuid.New()
	p := &Docker{
		Image:     "covey-sandbox:test",
		DataDir:   dir,
		DockerBin: fake,
		Engines:   engines.NewSource("file://"+catPath, nil, nil),
	}
	// The store lives where the control plane puts it: under the data dir.
	p.EngineStore = &engines.Store{Dir: filepath.Join(dir, "engines")}

	if _, _, err := p.Start(context.Background(), StartSandbox{AgentID: agentID, Engine: "sevencode"}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"COVEY_SEVENCODE_BIN=/opt/engines/sevencode/1.0.8/bin/sevencode",
		"/opt/engines/sevencode/1.0.8:ro",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the docker invocation does not carry %q:\n%s", want, got)
		}
	}
	// The host side of that mount has to be the installed layer, not a path that
	// will only appear later: the container starts against what is on disk.
	host := filepath.Join(dir, "engines", "sevencode", "1.0.8")
	if _, err := os.Stat(filepath.Join(host, "bin", "sevencode")); err != nil {
		t.Errorf("the layer was not installed before the container was started: %v", err)
	}
	if !strings.Contains(got, host+":/opt/engines/sevencode/1.0.8:ro") {
		t.Errorf("the mount is not the installed layer:\n%s", got)
	}

	// And the case that must not produce a container: the catalogue names this
	// engine, the artefact behind it is gone. A fresh source — a live Feed serves
	// its document for the TTL, so a file rewritten underneath it changes nothing
	// until it is refetched (the known property behind #117).
	brokenPath := filepath.Join(dir, "broken.json")
	write(brokenPath, []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[`+
		`{"version":"9.9.9","kind":"tarball","url":"file://`+filepath.Join(dir, "gone.tgz")+
		`","integrity":"sha256:0000000000000000000000000000000000000000000000000000000000000000"}]}]}`))
	p.Engines = engines.NewSource("file://"+brokenPath, nil, nil)
	other := uuid.New()
	startErr := func() error {
		_, _, err := p.Start(context.Background(), StartSandbox{AgentID: other, Engine: "sevencode"})
		return err
	}()
	if startErr == nil {
		t.Fatal("a sandbox started on an engine the catalogue could not deliver")
	}
	after, rerr := os.ReadFile(argsFile)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(after[len(raw):]), "\nrun\n") {
		t.Errorf("docker run was invoked although the engine is missing:\n%s", string(after[len(raw):]))
	}
	if !strings.Contains(startErr.Error(), "sevencode") {
		t.Errorf("the failure has to name the engine, got %v", startErr)
	}
}

// A catalogue that does not answer must not hold up a wake. Without the budget
// on the document fetch, an installation that enabled the mechanism and lost the
// host would pay the feed's fetch timeout on every start — and there is no
// negative caching to skip the attempt, because a document that failed once is
// exactly the one worth trying again.
func TestEngineLayerDoesNotWaitOnAnUnreachableCatalogue(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(10 * time.Second):
		}
	}))
	defer slow.Close()
	p := &Docker{
		DataDir:     t.TempDir(),
		Engines:     engines.NewSource(slow.URL, nil, nil),
		EngineStore: &engines.Store{Dir: filepath.Join(t.TempDir(), "engines")},
	}
	start := time.Now()
	env, mount, err := p.engineLayer(context.Background(), StartSandbox{Engine: "sevencode"})
	if took := time.Since(start); took > 8*time.Second {
		t.Fatalf("the start waited %v on a catalogue that never answered", took)
	}
	if err != nil || env != nil || mount != nil {
		t.Fatalf("an unreachable catalogue is silence, not a failed start: %v %v %v", env, mount, err)
	}
}

// engineTarball is a .tgz with the layout a self-contained engine ships in:
// bin/<engine> at the root of the archive.
func engineTarball(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	body := []byte("#!/bin/sh\necho sevencode\n")
	if err := tw.WriteHeader(&tar.Header{Name: "bin/sevencode", Mode: 0o755,
		Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	tw.Close()
	gz.Close()
	return out.Bytes()
}
