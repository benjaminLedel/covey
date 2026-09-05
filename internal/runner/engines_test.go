package runner

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
