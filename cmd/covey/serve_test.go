package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/config"
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

// The egress proxy is the sandbox's only way out in network-isolation mode,
// and it runs as its own process inside the proxy container. Without the two
// settings it needs it refuses to start rather than coming up with no
// allowlist at all — a proxy that does not know what is allowed is not a
// safeguard, it is a hole or a wall depending on which way it guesses.
func TestEgressProxyNeedsControlPlaneAndToken(t *testing.T) {
	for _, cfg := range []config.Config{
		{},
		{ControlURL: "http://127.0.0.1:1"},
		{RunnerToken: "t"},
	} {
		err := runEgressProxy(context.Background(), cfg, quiet())
		if err == nil {
			t.Errorf("the proxy started with %+v", cfg)
			continue
		}
		if !strings.Contains(err.Error(), "COVEY_CONTROL_URL") {
			t.Errorf("the refusal does not name what is missing: %v", err)
		}
	}
}

// With both, it binds and serves until it is told to stop. It deliberately
// does NOT check reachability at startup: in network mode the container joins
// the bridge only after it has started, so the control plane is unreachable
// for the first moments — the resolver retries and answers fail-closed until
// then, which is the right answer for a proxy that does not know its list yet.
func TestEgressProxyStartsWithoutTheControlPlaneAnswering(t *testing.T) {
	cfg := config.Config{
		ControlURL:      "http://127.0.0.1:1", // nothing there, on purpose
		RunnerToken:     "t",
		EgressProxyAddr: "127.0.0.1:0",
		EgressAllow:     []string{"host.docker.internal"},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runEgressProxy(ctx, cfg, quiet()) }()

	// Give it a moment to bind, then take it down again.
	time.Sleep(200 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("the proxy ended with %v — a cancelled context is a clean stop", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the proxy did not come down")
	}
}

// A serve with the home store and egress enforcement on takes a different path
// through the wiring than the default one: the blob store is opened, the
// cooperative proxy is bound inside the process, and the sweep loop starts.
func TestServeWithHomeStoreAndEgressEnforcement(t *testing.T) {
	cfg := testConfig(t)
	key, err := builtin.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MasterKeyHex = key
	cfg.ListenAddr = freePort(t)
	cfg.PublicURL = "http://" + cfg.ListenAddr
	cfg.BuiltinRunner = "off"
	cfg.HomeStore = true
	cfg.BlobStore = "builtin"
	cfg.EgressEnforce = true
	cfg.EgressIsolation = "proxy"
	cfg.EgressProxyAddr = "127.0.0.1:0"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, cfg, quiet()) }()

	waitForHTTP(t, "http://"+cfg.ListenAddr+"/api/v1/public/signup-state", 20*time.Second)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve ended with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not come down within 30 s")
	}
}

// An object store nobody can reach is a WARNING, not an abort: everything that
// is not a run works meanwhile, and a store that comes back in two minutes is
// a normal case. What must not happen is a silent fall back to the directory —
// homes would then be written where nothing looks for them.
func TestServeWithAnUnreachableObjectStore(t *testing.T) {
	cfg := testConfig(t)
	key, err := builtin.GenerateMasterKey()
	if err != nil {
		t.Fatal(err)
	}
	cfg.MasterKeyHex = key
	cfg.ListenAddr = freePort(t)
	cfg.PublicURL = "http://" + cfg.ListenAddr
	cfg.BuiltinRunner = "off"
	cfg.HomeStore = true
	cfg.BlobStore = "s3"
	cfg.S3Endpoint = "http://127.0.0.1:1"
	cfg.S3Bucket = "covey"
	cfg.S3AccessKey = "k"
	cfg.S3SecretKey = "s"
	cfg.S3Region = "eu-central-1"

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runServe(ctx, cfg, quiet()) }()

	waitForHTTP(t, "http://"+cfg.ListenAddr+"/api/v1/public/signup-state", 40*time.Second)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve ended with %v — an unreachable object store is a warning", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("serve did not come down")
	}
}
