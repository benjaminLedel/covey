package wasmplug

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

var (
	buildOnce sync.Once
	wasmPath  string
	buildErr  error
)

// demoModule compiles testdata/demoplugin with the real Go toolchain — the
// fixture is not a hand-written wasm blob but a plugin written the way a
// publisher would write one, which is the only way this test proves anything.
func demoModule(t *testing.T) []byte {
	t.Helper()
	buildOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wasmplug")
		if err != nil {
			buildErr = err
			return
		}
		wasmPath = filepath.Join(dir, "demo.wasm")
		cmd := exec.Command("go", "build", "-o", wasmPath, ".")
		cmd.Dir = "testdata/demoplugin"
		cmd.Env = append(os.Environ(), "GOOS=wasip1", "GOARCH=wasm")
		if out, err := cmd.CombinedOutput(); err != nil {
			buildErr = err
			t.Logf("go build: %s", out)
		}
	})
	if buildErr != nil {
		t.Skipf("cannot build the wasm fixture: %v", buildErr)
	}
	b, err := os.ReadFile(wasmPath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func compile(t *testing.T) *Module {
	t.Helper()
	m, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { m.Close(context.Background()) })
	return m
}

func TestModuleDescribesItself(t *testing.T) {
	m := compile(t)
	d := m.Describe()
	if d.Name != "demo" || d.Label != "Demo (wasm)" {
		t.Errorf("description = %+v", d)
	}
	if !d.Probe || !d.Poll {
		t.Error("the module says it can probe and poll — that has to arrive")
	}
	// The two capabilities a module cannot take for itself: an operator has to
	// be able to read both off the description before installing.
	if d.Webhook == nil || d.Webhook.Signature != "hmac-sha256" {
		t.Errorf("the declared webhook has to arrive: %+v", d.Webhook)
	}
	if !d.Workdir {
		t.Error("the module declares that it reads the checkout — that has to arrive")
	}
	if len(d.Actions) != 4 {
		t.Fatalf("actions = %d, want 4", len(d.Actions))
	}
	for _, a := range d.Actions {
		if a.Name == "comment" && a.Subject != "comment_external" {
			t.Errorf("a plugin has to be able to name its own guard-rail subject: %+v", a)
		}
	}
}

func TestExecuteWithoutAnyRequest(t *testing.T) {
	// The case a manifest cannot do at all: computation, no call.
	m := compile(t)
	out, err := m.Invoke(context.Background(), Invocation{
		Op: "execute", Action: "shout", Params: json.RawMessage(`{"text":"quiet"}`),
	}, Host{})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	json.Unmarshal(out.Result, &got)
	if got != "QUIET" {
		t.Errorf("result = %q, want QUIET", got)
	}
}

func TestFetchGoesThroughTheHost(t *testing.T) {
	m := compile(t)
	var seen FetchRequest
	fetch := func(_ context.Context, req FetchRequest) FetchResponse {
		seen = req
		return FetchResponse{Status: 200, Body: json.RawMessage(`{"id":7,"title":"broken login"}`)}
	}
	out, err := m.Invoke(context.Background(), Invocation{
		Op: "execute", Action: "get_issue", Params: json.RawMessage(`{"id":7}`),
	}, Host{Fetch: fetch})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Method != "GET" || seen.Path != "/issues/7" {
		t.Errorf("request = %+v", seen)
	}
	if !strings.Contains(string(out.Result), "broken login") {
		t.Errorf("result = %s", out.Result)
	}
}

func TestTheModuleNeverGetsACredential(t *testing.T) {
	// Structural, not a convention: FetchRequest carries no host, no scheme and
	// no auth header, and the module has no socket of its own. It therefore
	// cannot send a token anywhere, because it never has one.
	m := compile(t)
	var seen FetchRequest
	_, err := m.Invoke(context.Background(), Invocation{Op: "probe"}, Host{Fetch: func(_ context.Context, req FetchRequest) FetchResponse {
		seen = req
		return FetchResponse{Status: 200, Body: json.RawMessage(`{"login":"bot@example"}`)}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(seen.Path, "://") {
		t.Errorf("a plugin must not be able to name a host: %q", seen.Path)
	}
	for k := range seen.Header {
		if strings.EqualFold(k, "authorization") {
			t.Error("a plugin must not be able to set the authorization header")
		}
	}
}

func TestProbeReportsTheIdentity(t *testing.T) {
	m := compile(t)
	out, err := m.Invoke(context.Background(), Invocation{Op: "probe"}, Host{Fetch: func(context.Context, FetchRequest) FetchResponse {
		return FetchResponse{Status: 200, Body: json.RawMessage(`{"login":"bot@example"}`)}
	}})
	if err != nil {
		t.Fatal(err)
	}
	var who string
	json.Unmarshal(out.Result, &who)
	if who != "bot@example" {
		t.Errorf("identity = %q", who)
	}
}

func TestProbeFailureIsThePluginsOwn(t *testing.T) {
	// A plugin that decides the credential does not work says so through the
	// protocol; that is not a runtime failure and must not read like one.
	m := compile(t)
	out, err := m.Invoke(context.Background(), Invocation{Op: "probe"}, Host{Fetch: func(context.Context, FetchRequest) FetchResponse {
		return FetchResponse{Status: 401}
	}})
	if err != nil {
		t.Fatalf("a refused credential is the plugin's answer, not a crash: %v", err)
	}
	if !strings.Contains(out.Error, "probe failed") {
		t.Errorf("error = %q", out.Error)
	}
}

func TestPollReturnsWorkAndSignature(t *testing.T) {
	m := compile(t)
	out, err := m.Invoke(context.Background(), Invocation{Op: "poll"}, Host{Fetch: func(context.Context, FetchRequest) FetchResponse {
		return FetchResponse{Status: 200, Body: json.RawMessage(
			`[{"id":3,"updated_at":"a"},{"id":9,"updated_at":"b"}]`)}
	}})
	if err != nil {
		t.Fatal(err)
	}
	var pr PollResult
	json.Unmarshal(out.Result, &pr)
	if !pr.HasWork || pr.Signature != "3@a,9@b" {
		t.Errorf("poll = %+v", pr)
	}
}

func TestPromptDocNarrowsToScopes(t *testing.T) {
	m := compile(t)
	read, err := m.Invoke(context.Background(), Invocation{Op: "prompt_doc", Scopes: []string{"read"}}, Host{})
	if err != nil {
		t.Fatal(err)
	}
	write, err := m.Invoke(context.Background(), Invocation{Op: "prompt_doc", Scopes: []string{"read", "write"}}, Host{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(read.Result), "comment") {
		t.Errorf("read-only doc should not offer comment: %s", read.Result)
	}
	if !strings.Contains(string(write.Result), "comment") {
		t.Errorf("with write, comment belongs in the doc: %s", write.Result)
	}
}

func TestInstancesDoNotShareState(t *testing.T) {
	// Every invocation gets a fresh instance: one agent's action must not carry
	// anything into another's.
	m := compile(t)
	for i := 0; i < 3; i++ {
		out, err := m.Invoke(context.Background(), Invocation{
			Op: "execute", Action: "shout", Params: json.RawMessage(`{"text":"a"}`),
		}, Host{})
		if err != nil {
			t.Fatal(err)
		}
		var got string
		json.Unmarshal(out.Result, &got)
		if got != "A" {
			t.Fatalf("run %d: %q", i, got)
		}
	}
}

func TestUnknownActionIsThePluginsError(t *testing.T) {
	m := compile(t)
	out, err := m.Invoke(context.Background(), Invocation{Op: "execute", Action: "nope"}, Host{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Error, "unknown action") {
		t.Errorf("error = %q", out.Error)
	}
}

func TestRubbishIsNotAModule(t *testing.T) {
	if _, err := Compile(context.Background(), []byte("this is not wasm")); err == nil {
		t.Fatal("anything that is not a module has to be refused at compile time")
	}
}
