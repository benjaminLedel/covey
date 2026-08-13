package integration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"covey/internal/marketplace"
	"covey/internal/target"
)

// buildDemoWasm compiles the fixture plugin the way a publisher would: ordinary
// Go, GOOS=wasip1. If the toolchain cannot do it the test skips rather than
// pretending.
func buildDemoWasm(t *testing.T) []byte {
	t.Helper()
	out := filepath.Join(t.TempDir(), "demo.wasm")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = "../target/wasmplug/testdata/demoplugin"
	cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Skipf("cannot build the wasm fixture: %v\n%s", err, msg)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestWasmPluginFromCatalogue: a plugin that is real code — not a manifest —
// travels the same road as everything else. Catalogue, digest, install,
// activation, and then it is a target system like any other (spec/22).
func TestWasmPluginFromCatalogue(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	module := buildDemoWasm(t)
	sum := sha256.Sum256(module)
	digest := hex.EncodeToString(sum[:])

	// The fake target system the plugin will talk to.
	var seenAuth, seenPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuth, seenPath = r.Header.Get("Authorization"), r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":7,"title":"broken login"}`)
	}))
	defer upstream.Close()

	// A catalogue serving the module.
	mux := http.NewServeMux()
	index := httptest.NewServer(mux)
	defer index.Close()
	mux.HandleFunc("/demo.wasm", func(w http.ResponseWriter, r *http.Request) { w.Write(module) })
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(marketplace.Catalog{Schema: 1, Plugins: []marketplace.Entry{{
			Name: "demo", Label: "Demo (wasm)", Description: "compiled plugin",
			Category: "dev", Kind: "wasm", Publisher: "example",
			Homepage: "https://example.invalid", License: "MIT",
			Versions: []marketplace.Version{{Version: "1.0.0", URL: index.URL + "/demo.wasm", SHA256: digest}},
		}}})
	})
	s.srv.Marketplace = marketplace.New(index.URL + "/catalog.json")

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/marketplace/demo/install", nil, http.StatusOK)

	// It landed as a plugin, described by the module itself — the label and
	// the scopes were never typed by anybody, the code said them.
	var plugins []map[string]any
	resp := admin.do(http.MethodGet, "/api/v1/targets", nil)
	json.NewDecoder(resp.Body).Decode(&plugins)
	resp.Body.Close()
	var found map[string]any
	for _, p := range plugins {
		if p["name"] == "demo" {
			found = p
		}
	}
	if found == nil {
		t.Fatalf("the wasm plugin is not in the store: %v", plugins)
	}
	if found["kind"] != "wasm" || found["enabled"] != false {
		t.Errorf("plugin = %v, want kind=wasm and disabled on arrival", found)
	}
	if found["label"] != "Demo (wasm)" {
		t.Errorf("label = %v — it should come from the module's own description", found["label"])
	}

	// Activate it and give it a credential, as an organization would.
	admin.expect(http.MethodPatch, "/api/v1/targets/demo", map[string]any{"enabled": true}, http.StatusOK)
	s.secrets.Put(ctx, s.orgID, "demo_token", "s3cret")
	s.secrets.Put(ctx, s.orgID, "demo_url", upstream.URL)

	sys, err := s.targets.System(ctx, s.orgID, "demo")
	if err != nil {
		t.Fatal(err)
	}

	// Compiled code can do what a manifest cannot: work out an answer without
	// calling anything.
	out, err := sys.Execute(ctx, "shout", json.RawMessage(`{"text":"quiet"}`), target.Credential{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "QUIET" {
		t.Errorf("local computation = %v, want QUIET", out)
	}

	// And when it does call, the host adds the base URL and the token — the
	// module never sees either.
	out, err = sys.Execute(ctx, "get_issue", json.RawMessage(`{"id":7}`),
		target.Credential{BaseURL: upstream.URL, Token: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if seenPath != "/issues/7" || seenAuth != "Bearer s3cret" {
		t.Errorf("upstream saw path=%q auth=%q", seenPath, seenAuth)
	}
	if m, _ := out.(map[string]any); m["title"] != "broken login" {
		t.Errorf("result = %v", out)
	}

	// The capabilities the module declared are the ones the platform believes.
	if _, ok := target.Probes(sys); !ok {
		t.Error("the module declares a probe — the platform has to offer it")
	}
	if got := sys.ActionSubject("comment", nil); got != "demo:comment_external" {
		t.Errorf("guard-rail subject = %q — the plugin names its own", got)
	}
}

// TestWasmPluginRefusesARubbishModule: what does not compile never gets stored.
// Finding out at the first action of the first agent would be hours later and
// in the wrong place.
func TestWasmPluginRefusesARubbishModule(t *testing.T) {
	s := newStack(t)
	rubbish := []byte("this is not a wasm module")
	sum := sha256.Sum256(rubbish)

	mux := http.NewServeMux()
	index := httptest.NewServer(mux)
	defer index.Close()
	mux.HandleFunc("/broken.wasm", func(w http.ResponseWriter, r *http.Request) { w.Write(rubbish) })
	mux.HandleFunc("/catalog.json", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(marketplace.Catalog{Schema: 1, Plugins: []marketplace.Entry{{
			Name: "broken", Label: "Broken", Kind: "wasm", Category: "dev",
			Publisher: "example", Homepage: "https://example.invalid", License: "MIT",
			Versions: []marketplace.Version{{
				Version: "1.0.0", URL: index.URL + "/broken.wasm",
				SHA256: hex.EncodeToString(sum[:]),
			}},
		}}})
	})
	s.srv.Marketplace = marketplace.New(index.URL + "/catalog.json")

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/marketplace/broken/install", nil, http.StatusBadRequest)

	var n int
	if err := s.pool.QueryRow(context.Background(),
		`SELECT count(*) FROM target_plugins WHERE org_id=$1 AND name='broken'`, s.orgID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("a module that does not compile must not be stored")
	}
}
