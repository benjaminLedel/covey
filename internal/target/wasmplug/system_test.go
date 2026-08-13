package wasmplug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/target"
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
