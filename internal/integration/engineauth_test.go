package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"covey/internal/engines"
	"covey/internal/orchestrator"
	"covey/internal/runner"
	"covey/internal/sandbox"

	"github.com/google/uuid"
)

// The artefact of an engine behind a login is opened by the agent's secret, not
// by the host (#289). In the field that promise broke quietly: the secret stood
// in the store, assigned to the agent, and every start still refused with "the
// secret it names is not set for this agent" — for hours, without a line about
// the actual cause (#293). This drives the production path end to end —
// orchestrator → pool → protocol → node → engines store → fetch — and asserts
// the bytes leaving the process: the artefact request carries the agent's
// assigned secret, with no host variable standing anywhere near it.
func TestEngineArtefactCarriesTheAssignedSecret(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	dockerBin := fakeDocker(t, dir)

	// The artefact behind a strict login: only the bearer this test assigns
	// through the store opens it — every other word (anonymous, guessed, a
	// host variable) is a 401, so the assertion cannot pass by accident.
	bundle := []byte("#!/bin/sh\necho sevencode-test\n")
	sum := sha256.Sum256(bundle)
	var mu sync.Mutex
	var seen []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /cli", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Header.Get("Authorization"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer sc_assigned_secret" {
			http.Error(w, `{"error":"invalid_api_key"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(bundle)
	})
	artefactSrv := httptest.NewServer(mux)
	t.Cleanup(artefactSrv.Close)

	catalog := fmt.Sprintf(`{"schema":1,"engines":[{"name":"sevencode","label":"SevenCode",
		"versions":[{"version":"1.0.27","kind":"file","url":"%s/cli",
		"integrity":"sha256:%s","binary":"bin/sevencode",
		"auth_header":"Authorization","auth_secret":"COVEY_SEVENCODE_DOWNLOAD_TOKEN"}]}]}`,
		artefactSrv.URL, hex.EncodeToString(sum[:]))
	catalogSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(catalog))
	}))
	t.Cleanup(catalogSrv.Close)

	// The catalogue path, not the documented host-binary skip: no COVEY_SEVENCODE_BIN.
	_ = os.Unsetenv("COVEY_SEVENCODE_BIN")

	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		readyTimeout: 90 * time.Second,
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
			pool.StartTimeout = 30 * time.Second
			return pool
		},
		// The control-plane half of the catalogue — the same document, read by
		// the side that decides which secret opens it.
		afterOrch: func(o *orchestrator.Orchestrator) {
			o.Engines = engines.NewSource(catalogSrv.URL, engines.FileCacheFor(dir), slog.Default())
		},
	})
	ctx := context.Background()

	// The engine's runtime key first: the seat the platform's hook builds must
	// find the credential it attaches already standing in the store.
	if err := s.secrets.Put(ctx, s.orgID, "sevencode_api_token", "sc_runtime_key"); err != nil {
		t.Fatal(err)
	}
	rt, err := s.runtimes.EnsureDefault(ctx, s.orgID, "sevencode")
	if err != nil {
		t.Fatal(err)
	}

	agent, err := s.registry.Create(ctx, s.orgID, "engine-auth", "Engine-Auth", "sevencode", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Engine-Auth\n\n## Role\nTest.",
		"ACCESS.md": "- system: zammad scope: read,write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if err := s.runtimes.Assign(ctx, s.orgID, agent.ID, rt.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "sevencode_api_token", agent.ID); err != nil {
		t.Fatal(err)
	}

	// The download secret — the case #293 is about: a store secret with the
	// catalogue's literal name, assigned to this agent, nothing on any host.
	if err := s.secrets.Put(ctx, s.orgID, "COVEY_SEVENCODE_DOWNLOAD_TOKEN", "sc_assigned_secret"); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "COVEY_SEVENCODE_DOWNLOAD_TOKEN", agent.ID); err != nil {
		t.Fatal(err)
	}

	// The built-in runner speaks the real protocol; its Docker carries both
	// halves of the catalogue exactly as cmd/covey wires them for production.
	runnerID := uuid.New()
	node := runner.NewNode(runnerID, s.orgID, &runner.Docker{
		RunnerID:    runnerID,
		Image:       "covey-sandbox:test",
		DataDir:     dir,
		DockerBin:   dockerBin,
		Engines:     engines.NewSource(catalogSrv.URL, engines.FileCacheFor(dir), slog.Default()),
		EngineStore: &engines.Store{Dir: filepath.Join(dir, "engines"), Log: slog.Default()},
	}, slog.Default())
	t.Cleanup(node.Close)
	if err := pool.AttachLocal(ctx, node); err != nil {
		t.Fatalf("built-in runner: %v", err)
	}

	if _, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Engine artefact wake", "", "manual", 3); err != nil {
		t.Fatal(err)
	}

	// The download happens while the start is prepared — before the (faked)
	// container ever runs. The assertion is the header the endpoint sees.
	waitFor(t, "the artefact was requested", 40*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(seen) > 0
	})
	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
	for _, h := range got {
		if h == "Bearer sc_assigned_secret" {
			return // #289 holds: the request carries the agent's assigned secret
		}
	}
	t.Fatalf("the artefact request did not carry the assigned secret — headers seen: %v", got)
}

// The same case one reading further: a named secret that does not arrive has to
// say so. The field case behind #293 refused two hours while the secret looked
// assigned — the download's own sentence names the places it looked, never what
// this side found, so the search could not end anywhere. One lifecycle entry
// makes the recording answer: which secret, which engine, store error or empty.
func TestTheMissingEngineSecretNamesItself(t *testing.T) {
	catalog := `{"schema":1,"engines":[{"name":"sevencode","label":"SevenCode",
		"versions":[{"version":"1.0.27","kind":"file","url":"file:///dev/null",
		"integrity":"sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855","binary":"bin/sevencode",
		"auth_header":"Authorization","auth_secret":"COVEY_SEVENCODE_DOWNLOAD_TOKEN"}]}]}`
	catalogSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(catalog))
	}))
	t.Cleanup(catalogSrv.Close)

	s := newStackWith(t, stackOpts{
		afterOrch: func(o *orchestrator.Orchestrator) {
			o.Engines = engines.NewSource(catalogSrv.URL, engines.FileCacheFor(t.TempDir()), slog.Default())
		},
	})
	ctx := context.Background()
	if err := s.secrets.Put(ctx, s.orgID, "sevencode_api_token", "sc_runtime_key"); err != nil {
		t.Fatal(err)
	}
	rt, err := s.runtimes.EnsureDefault(ctx, s.orgID, "sevencode")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.registry.Create(ctx, s.orgID, "names-itself", "Names-Itself", "sevencode", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Names-Itself\n\n## Role\nTest.",
		"ACCESS.md": "- system: zammad scope: read,write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	if err := s.runtimes.Assign(ctx, s.orgID, agent.ID, rt.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.secrets.Assign(ctx, s.orgID, "sevencode_api_token", agent.ID); err != nil {
		t.Fatal(err)
	}
	// The download secret deliberately does NOT exist — the wake still asks
	// for it, and the asking is what has to cost a line.

	_, err = s.backlog.Create(ctx, s.orgID, agent.ID, "Names itself", "", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, "the refusal names itself", 20*time.Second, func() bool {
		events, err := s.obs.Events(ctx, agent.ID, nil, 0, 200)
		if err != nil {
			return false
		}
		for _, e := range events {
			if e.Kind != "lifecycle" {
				continue
			}
			var p struct {
				Status string `json:"status"`
				Secret string `json:"secret"`
				Reason string `json:"reason"`
			}
			if json.Unmarshal(e.Payload, &p) != nil || p.Status != "engine_auth_unavailable" {
				continue
			}
			if p.Secret != "COVEY_SEVENCODE_DOWNLOAD_TOKEN" {
				t.Fatalf("the entry has to name the secret the catalogue asks for, got %q", p.Secret)
			}
			return true
		}
		return false
	})
}
