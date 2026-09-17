package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/engines"
)

// An artefact behind a login is opened by the agent's secret, which the control
// plane resolves for this one start and hands over with the request (#289). What
// the host must not end up with is the credential itself: not in its environment,
// not on the layer it mounts, not in what it keeps about that layer.
func TestEngineLayerOffersTheAgentsSecretToTheDownload(t *testing.T) {
	const token = "Bearer sc-die-agent"
	body := []byte("#!/bin/sh\necho engine\n")
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	sum := sha256.Sum256(body)
	cat := `{"schema":1,"engines":[{"name":"sevencode","versions":[{"version":"1.0.27",
		"kind":"file","binary":"sevencode","url":"` + srv.URL + `/cli/latest",
		"integrity":"sha256:` + hex.EncodeToString(sum[:]) + `",
		"auth_header":"Authorization","auth_secret":"sevencode_api_token"}]}]}`
	dir := t.TempDir()
	catPath := filepath.Join(dir, "engines.json")
	if err := os.WriteFile(catPath, []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	storeDir := filepath.Join(dir, "engines")
	p := &Docker{
		DataDir:     dir,
		Engines:     engines.NewSource("file://"+catPath, nil, nil),
		EngineStore: &engines.Store{Dir: storeDir},
	}

	env, mount, err := p.engineLayer(t.Context(), StartSandbox{Engine: "sevencode", EngineAuth: token})
	if err != nil {
		t.Fatalf("the layer has to come up with the secret that travels with the start: %v", err)
	}
	if len(env) == 0 || len(mount) == 0 {
		t.Fatalf("an installed engine has to reach the container: %v %v", env, mount)
	}
	if seen != token {
		t.Fatalf("the download was offered what the entry asks for, got %q", seen)
	}

	// Nothing of the credential is left behind on this host. Walked rather than
	// guessed at, because where a layer keeps its own bookkeeping is the store's
	// affair and not an assumption this test may make.
	err = filepath.WalkDir(storeDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		if strings.Contains(string(b), token) {
			t.Errorf("%s holds the credential that was only borrowed for the download", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// An engine whose secret the agent does not have fails the start, and the reason
// names both places a token could come from — the organisation's secret and the
// host's own variable. One of the two is what an operator can set, and a message
// that names only one of them sends the search to the wrong machine.
func TestEngineLayerWithoutAnyTokenNamesBothPlaces(t *testing.T) {
	body := []byte("#!/bin/sh\n")
	sum := sha256.Sum256(body)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an artefact known to need a token must not be fetched anonymously")
	}))
	t.Cleanup(srv.Close)

	cat := `{"schema":1,"engines":[{"name":"sevencode","versions":[{"version":"1.0.27",
		"kind":"file","binary":"sevencode","url":"` + srv.URL + `/cli/latest",
		"integrity":"sha256:` + hex.EncodeToString(sum[:]) + `",
		"auth_header":"Authorization","auth_secret":"sevencode_api_token",
		"auth_env":"COVEY_TEST_ENGINE_TOKEN"}]}]}`
	dir := t.TempDir()
	catPath := filepath.Join(dir, "engines.json")
	if err := os.WriteFile(catPath, []byte(cat), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &Docker{
		DataDir:     dir,
		Engines:     engines.NewSource("file://"+catPath, nil, nil),
		EngineStore: &engines.Store{Dir: filepath.Join(dir, "engines")},
	}

	_, _, err := p.engineLayer(t.Context(), StartSandbox{Engine: "sevencode"})
	if err == nil {
		t.Fatal("a login that cannot be answered has to fail the start, not ask anonymously")
	}
	for _, want := range []string{"sevencode_api_token", "COVEY_TEST_ENGINE_TOKEN"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the message has to name %s, or the search ends at the wrong place: %v", want, err)
		}
	}
}
