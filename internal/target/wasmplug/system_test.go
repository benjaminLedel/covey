package wasmplug

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// system compiles the fixture and puts a fake target system behind it, so the
// whole path is exercised: module → host → HTTP → back.
func system(t *testing.T, handler http.HandlerFunc) (*System, *httptest.Server) {
	t.Helper()
	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mod.Close(context.Background()) })
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	sys := NewSystem(mod)
	sys.HTTP = srv.Client()
	return sys, srv
}

func TestSystemIsATargetSystemLikeAnyOther(t *testing.T) {
	// The point of the adapter: everything downstream of target.System —
	// broker, guard rails, recording, ACCESS.md — applies unchanged.
	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())
	var sys target.System = NewSystem(mod)

	if sys.Name() != "demo" {
		t.Errorf("name = %q", sys.Name())
	}
	if _, ok := target.Probes(sys); !ok {
		t.Error("the module says it can probe — target.Probes has to agree")
	}
	if _, ok := target.WorkChecks(sys); !ok {
		t.Error("the module says it can poll — target.WorkChecks has to agree")
	}
	if _, ok := sys.(target.ScopedDocSystem); !ok {
		t.Error("a compiled plugin has to be able to narrow its doc")
	}
	// A subject the plugin named itself, so a guard rail can be written against
	// it without anybody guessing.
	if got := sys.ActionSubject("comment", nil); got != "demo:comment_external" {
		t.Errorf("subject = %q, want demo:comment_external", got)
	}
	if got := sys.ActionSubject("get_issue", nil); got != "demo:get_issue" {
		t.Errorf("subject = %q, want demo:get_issue", got)
	}
}

func TestExecuteReachesTheBrokeredSystem(t *testing.T) {
	var gotPath, gotAuth string
	sys, srv := system(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotAuth = r.URL.Path, r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"id":7,"title":"broken login"}`))
	})

	out, err := sys.Execute(context.Background(), "get_issue", json.RawMessage(`{"id":7}`),
		target.Credential{BaseURL: srv.URL, Token: "s3cret"})
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/issues/7" {
		t.Errorf("path = %q", gotPath)
	}
	// The host added the credential — the module never had it.
	if gotAuth != "Bearer s3cret" {
		t.Errorf("authorization = %q", gotAuth)
	}
	m, _ := out.(map[string]any)
	if m["title"] != "broken login" {
		t.Errorf("result = %v", out)
	}
}

func TestAPluginCannotRedirectTheToken(t *testing.T) {
	// The guarantee that makes third-party code tolerable: a plugin names a
	// path, and the host decides the host. Even a module that tried to write an
	// absolute URL would reach its own system, not a foreign one.
	sys, srv := system(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	f := sys.fetcher(target.Credential{BaseURL: srv.URL, Token: "s3cret"})
	for _, path := range []string{"https://evil.example.com/steal", "//evil.example.com/steal", "issues"} {
		resp := f(context.Background(), FetchRequest{Method: "GET", Path: path})
		if resp.Error == "" {
			t.Errorf("path %q should have been refused", path)
		}
	}
	// And it cannot set the auth header itself either.
	var seen string
	sys2, srv2 := system(t, func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	})
	f2 := sys2.fetcher(target.Credential{BaseURL: srv2.URL, Token: "real"})
	f2(context.Background(), FetchRequest{
		Method: "GET", Path: "/x", Header: map[string]string{"Authorization": "Bearer stolen"},
	})
	if seen != "Bearer real" {
		t.Errorf("authorization = %q, want the brokered one", seen)
	}
}

func TestProbeThroughTheAdapter(t *testing.T) {
	sys, srv := system(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"login":"bot@example"}`))
	})
	who, err := sys.Probe(context.Background(), target.Credential{BaseURL: srv.URL, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if who != "bot@example" {
		t.Errorf("identity = %q", who)
	}
}

func TestPollThroughTheAdapter(t *testing.T) {
	sys, srv := system(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":3,"updated_at":"a"}]`))
	})
	has, sig, err := sys.HasWorkSigned(context.Background(),
		target.Credential{BaseURL: srv.URL, Token: "t"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !has || sig != "3@a" {
		t.Errorf("poll = (%v, %q)", has, sig)
	}
}

func TestPromptDocIsCachedPerScopeSet(t *testing.T) {
	// The doc is read on every turn of every agent that has the system; asking
	// the module each time would cost an instantiation per turn.
	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())
	sys := NewSystem(mod)

	read := sys.PromptDocForScopes([]string{"read"})
	if strings.Contains(read, "comment") {
		t.Errorf("read-only doc should not offer comment: %s", read)
	}
	if again := sys.PromptDocForScopes([]string{"read"}); again != read {
		t.Error("the same scopes have to give the same doc from the cache")
	}
	write := sys.PromptDocForScopes([]string{"read", "write"})
	if !strings.Contains(write, "comment") {
		t.Errorf("with write, comment belongs in the doc: %s", write)
	}
}

func TestPluginErrorsReachTheAgentAsErrors(t *testing.T) {
	sys, srv := system(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{}`))
	})
	_, err := sys.Execute(context.Background(), "nope", nil,
		target.Credential{BaseURL: srv.URL, Token: "t"})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("err = %v", err)
	}
}

func TestDeclaredHostsAreReachableWithoutTheCredential(t *testing.T) {
	// A plugin that needs a second host — an OAuth token endpoint, a public
	// database — declares it, so an operator sees it before installing rather
	// than in a log afterwards. What the second host must never see is the
	// credential: it belongs to the system the organisation pointed the plugin
	// at, and a token that travels elsewhere is a token that leaked.
	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())
	sys := NewSystem(mod)
	sys.desc.Hosts = []string{"api.osv.dev"}

	f := sys.fetcher(target.Credential{BaseURL: "https://tracker.example", Token: "s3cret"})

	// Not declared → refused, and the message says what to do about it.
	resp := f(context.Background(), FetchRequest{Method: "GET", Path: "https://evil.example.com/steal"})
	if resp.Error == "" || !strings.Contains(resp.Error, "not declared") {
		t.Errorf("an undeclared host has to be refused: %+v", resp)
	}
	// http, even when declared, is refused: a token is not involved, but the
	// answer would still be somebody else's to write.
	sys.desc.Hosts = append(sys.desc.Hosts, "plain.example.com")
	resp = f(context.Background(), FetchRequest{Method: "GET", Path: "http://plain.example.com/x"})
	if resp.Error == "" || !strings.Contains(resp.Error, "https") {
		t.Errorf("http to a declared host has to be refused: %+v", resp)
	}
}

func TestDeclaredHostGetsNoToken(t *testing.T) {
	var gotAuth string
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Write([]byte(`{}`))
	}))
	defer foreign.Close()

	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())
	sys := NewSystem(mod)
	sys.HTTP = foreign.Client()
	// httptest serves plain http, so the https rule is relaxed for this test
	// only — what is under test here is the credential, not the scheme.
	host := strings.TrimPrefix(foreign.URL, "http://")
	sys.desc.Hosts = []string{host}
	f := sys.fetcherAllowingHTTP(target.Credential{BaseURL: "https://tracker.example", Token: "s3cret"})
	resp := f(context.Background(), FetchRequest{Method: "GET", Path: foreign.URL + "/anything"})
	if resp.Error != "" {
		t.Fatalf("declared host should be reachable: %s", resp.Error)
	}
	if gotAuth != "" {
		t.Errorf("the brokered token reached a declared host: %q", gotAuth)
	}
}

// A webhook is the capability that decided which plugins could leave the binary
// (spec/22): without it zammad and everything shaped like it stays compiled,
// because turning an inbound payload into a task is a decision and not a field
// lookup. The test walks the whole door: the host checks the signature with the
// secret, the module never sees it, and what comes back is a target.WebhookEvent
// like any compiled plugin's.
func TestWebhookIsVerifiedByTheHostAndParsedByTheModule(t *testing.T) {
	sys, _ := system(t, func(w http.ResponseWriter, r *http.Request) {})
	var s target.System = sys
	hook, ok := s.(target.Webhooker)
	if !ok {
		t.Fatal("a module that declares a webhook has to be a target.Webhooker")
	}
	if !sys.Supports(CapWebhook) {
		t.Fatal("the module declared a webhook — Supports has to agree")
	}

	body := []byte(`{"issue":{"id":42,"title":"Login broken"},"comment":{"id":7,"body":"still broken","author":"customer"}}`)
	const secret = "s3cret"

	// The wrong signature does not get in. This is the half the module must not
	// do, and cannot: it never receives the secret.
	if hook.VerifyWebhook(secret, body, http.Header{"X-Hub-Signature": []string{"sha256=deadbeef"}}) {
		t.Fatal("a wrong signature must not verify")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	good := http.Header{"X-Hub-Signature": []string{"sha256=" + hex.EncodeToString(mac.Sum(nil))}}
	if !hook.VerifyWebhook(secret, body, good) {
		t.Fatal("the correct signature has to verify")
	}

	ev, err := hook.ParseWebhook(body)
	if err != nil {
		t.Fatal(err)
	}
	if ev.CorrelationKey != "demo:issue:42" {
		t.Errorf("correlation key = %q — a blocked task hangs off it", ev.CorrelationKey)
	}
	if ev.DedupKey != "demo:comment:7" {
		t.Errorf("dedup key = %q — the target system's retry depends on it", ev.DedupKey)
	}
	if !strings.Contains(ev.TaskBody, "still broken") {
		t.Errorf("task body = %q — the person reading the backlog needs the text", ev.TaskBody)
	}
	if !ev.Wake {
		t.Error("a customer's comment is news and has to wake")
	}

	// The echo of the agent's own comment: recorded, but nobody is woken. That
	// decision is exactly what a manifest cannot express.
	echo := []byte(`{"issue":{"id":42,"title":"Login broken"},"comment":{"id":8,"body":"we are looking into it","author":"covey-agent"}}`)
	ev, err = hook.ParseWebhook(echo)
	if err != nil {
		t.Fatal(err)
	}
	if ev.Wake {
		t.Error("the agent's own echo must not wake it again")
	}
	if ev.DedupKey == "" {
		t.Error("an event that does not wake still has to be recorded for dedup")
	}
}

// A module without a webhook block is not a webhook entrance. Both locks have
// to hold: the capability report the router asks, and the verification itself.
func TestModuleWithoutWebhookIsNoEntrance(t *testing.T) {
	sys := NewSystem(&Module{desc: Description{Name: "quiet"}})
	if sys.Supports(CapWebhook) {
		t.Fatal("a module that declared no webhook must not report one")
	}
	if sys.VerifyWebhook("secret", []byte("{}"), http.Header{}) {
		t.Fatal("without a declared webhook the verification has to fail closed")
	}
	if _, err := sys.ParseWebhook([]byte("{}")); err == nil {
		t.Fatal("parsing without a declared webhook has to be an error")
	}
}

// Reading out of the agent's workspace is the second capability that decides
// whether a plugin can come from the catalogue at all (spec/22): a plugin that
// judges what a project declares has to be able to read the file that declares
// it. The confinement is the whole point, so the test spends most of its length
// trying to get out of the directory.
func TestWorkdirReadIsConfinedToTheWorkspace(t *testing.T) {
	sys, _ := system(t, func(w http.ResponseWriter, r *http.Request) {})

	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "package-lock.json"), []byte(`{"name":"app","lockfileVersion":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The thing that must stay unreachable: a file beside the workspace, the
	// shape of every secret an agent's home directory has.
	outside := filepath.Join(filepath.Dir(work), "secret.env")
	if err := os.WriteFile(outside, []byte("COVEY_MASTER_KEY=deadbeef"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(outside) })
	if err := os.Symlink(outside, filepath.Join(work, "link-out")); err != nil {
		t.Fatal(err)
	}
	ctx := target.WithWorkdir(context.Background(), work)

	read := func(path string) (any, error) {
		return sys.Execute(ctx, "read_lock", json.RawMessage(`{"path":`+strconv.Quote(path)+`}`), target.Credential{})
	}

	got, err := read("package-lock.json")
	if err != nil {
		t.Fatalf("the declared file has to be readable: %v", err)
	}
	if !strings.Contains(fmt.Sprint(got), "lockfileVersion") {
		t.Errorf("the module got %v — the file's content has to arrive", got)
	}

	// Everything below is a way out of the directory, and every one of them has
	// to end as an error rather than as content.
	for _, escape := range []string{
		"../secret.env",
		"../../etc/passwd",
		"/etc/passwd",
		"link-out",             // a symlink pointing out of the tree
		"./../" + "secret.env", // the same thing written differently
	} {
		out, err := read(escape)
		if err == nil {
			t.Errorf("%q was answered with %v — a module must not leave its workspace", escape, out)
		}
		if err != nil && strings.Contains(err.Error(), "MASTER_KEY") {
			t.Fatalf("%q leaked the file's content in the error: %v", escape, err)
		}
	}

	// A missing file is a normal answer, not a breakdown: it is how a module
	// finds out which of three lock files a project has.
	if _, err := read("composer.lock"); err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("a missing file has to say so plainly, got %v", err)
	}

	// A module that never declared a workspace does not get one, however the
	// action is invoked.
	sys.desc.Workdir = false
	if _, err := read("package-lock.json"); err == nil {
		t.Error("without a declared workdir the read has to be refused")
	}
}

// Outside a sandbox there is no workspace at all — the control plane's own
// calls (probe, poll) have none, and a module asking there is told so rather
// than handed whatever directory the process happens to sit in.
func TestNoWorkspaceOutsideTheSandbox(t *testing.T) {
	sys, _ := system(t, func(w http.ResponseWriter, r *http.Request) {})
	_, err := sys.Execute(context.Background(), "read_lock",
		json.RawMessage(`{"path":"package-lock.json"}`), target.Credential{})
	if err == nil {
		t.Fatal("without a workdir in the context the read has to fail")
	}
	if !strings.Contains(err.Error(), "workspace") {
		t.Errorf("the error should name what is missing, got %v", err)
	}
}
