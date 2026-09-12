package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/secrets/builtin"
)

// The one thing no unit test says: does the binary actually come up?
//
// `covey serve` is where forty parts are wired together — identity, secrets,
// sealbox, migrations, the orchestrator, the runner pool, the HTTP server, six
// background loops. Each of them is tested somewhere; the assembly was tested
// nowhere, and an assembly is exactly what breaks when a constructor grows an
// argument or a loop starts before the thing it reads from exists.
//
// So: start it, ask it who it is, shut it down.
func TestServeStartsAnswersAndStopsCleanly(t *testing.T) {
	cfg := testConfig(t)
	key, err := builtin.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MasterKeyHex = key
	cfg.ListenAddr = freePort(t)
	cfg.PublicURL = "http://" + cfg.ListenAddr
	// No sandbox runs here: this is about the control plane coming up, and the
	// data plane needs a Docker the test machine need not have.
	cfg.BuiltinRunner = "off"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, cfg, quiet()) }()

	base := "http://" + cfg.ListenAddr
	waitForHTTP(t, base+"/api/v1/public/signup-state", 20*time.Second)

	// The public signup state is what the login page asks before anybody has
	// a session — the first request a browser makes against a fresh instance.
	resp, err := http.Get(base + "/api/v1/public/signup-state")
	if err != nil {
		t.Fatal(err)
	}
	var state map[string]any
	json.NewDecoder(resp.Body).Decode(&state)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the signup state answered HTTP %d", resp.StatusCode)
	}
	if state["mode"] == nil {
		t.Errorf("the instance does not say whether it takes anybody in: %v", state)
	}

	// The admin UI is compiled into the binary. A serve that answers the API
	// and not the interface is an installation nobody can use.
	ui, err := http.Get(base + "/")
	if err != nil {
		t.Fatal(err)
	}
	ui.Body.Close()
	if ui.StatusCode != http.StatusOK {
		t.Errorf("the interface answered HTTP %d", ui.StatusCode)
	}

	// And what needs a session says so, rather than answering. Version too:
	// which build is running is not something an unauthenticated caller gets.
	me, err := http.Get(base + "/api/v1/version")
	if err != nil {
		t.Fatal(err)
	}
	me.Body.Close()
	if me.StatusCode != http.StatusUnauthorized {
		t.Errorf("an endpoint behind the session answered HTTP %d without one", me.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve ended with %v — a cancelled context is a graceful shutdown", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not come down within 30 s")
	}
}

// Without a master key nothing starts, and the message names the command that
// makes one — every secret in the installation hangs off it.
func TestServeRefusesWithoutAMasterKey(t *testing.T) {
	cfg := testConfig(t)
	cfg.MasterKeyHex = ""
	err := runServe(context.Background(), cfg, quiet())
	if err == nil {
		t.Fatal("serve started without a master key")
	}
	if !strings.Contains(err.Error(), "genkey") {
		t.Errorf("the message does not name the way to one: %v", err)
	}
}

// freePort asks the operating system for one and hands it back — the test
// cannot use :0, because ListenAndServe binds it and never says which.
func freePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	return addr
}

func waitForHTTP(t *testing.T, url string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("%s did not answer within %s", url, within)
}
