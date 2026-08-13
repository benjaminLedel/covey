package target

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// capManifest declares everything the engine learned to do in data: a probe, a
// poll with a sub-scope, a scope vocabulary and per-action doc lines.
const capManifest = `{
  "name": "tracker",
  "label": "Tracker",
  "auth": {"header": "X-API-Key", "format": "{token}"},
  "webhook": {"id_field": "issue.id"},
  "scopes": ["read", "comment"],
  "probe": {"path": "/me", "identity_field": "user.login"},
  "poll": {
    "": {"path": "/issues?state=open", "items_field": "items", "signature_field": "updated_at"},
    "review": {"path": "/reviews?assigned=me", "items_field": "items", "signature_field": "id"}
  },
  "actions": {
    "get_issue": {"method": "GET", "path": "/issues/{id}", "scope": "read", "doc": "read one issue"},
    "comment":   {"method": "POST", "path": "/issues/{id}/comments", "scope": "comment", "doc": "write a comment"},
    "whoami":    {"method": "GET", "path": "/me", "doc": "who am I"}
  }
}`

func capSystem(t *testing.T, body func(path string) string) (*ManifestSystem, *httptest.Server) {
	t.Helper()
	m, err := ParseManifest([]byte(capManifest))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-API-Key"); got != "tok" {
			t.Errorf("auth header = %q, want tok", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(body(r.URL.RequestURI())))
	}))
	t.Cleanup(srv.Close)
	sys := NewManifestSystem(m)
	sys.HTTP = srv.Client()
	return sys, srv
}

func TestManifestCapabilitiesAreDeclaredNotAssumed(t *testing.T) {
	full, err := ParseManifest([]byte(capManifest))
	if err != nil {
		t.Fatal(err)
	}
	bare, err := ParseManifest([]byte(demoManifest))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		m    Manifest
		cap  string
		want bool
	}{
		{"probe declared", full, CapProbe, true},
		{"poll declared", full, CapPoll, true},
		{"probe absent", bare, CapProbe, false},
		{"poll absent", bare, CapPoll, false},
	} {
		sys := NewManifestSystem(tc.m)
		if got := sys.Supports(tc.cap); got != tc.want {
			t.Errorf("%s: Supports(%q) = %v, want %v", tc.name, tc.cap, got, tc.want)
		}
	}

	// The whole point of the capability report: the method set alone would say
	// yes for every manifest, and the store would grow a probe button that can
	// only fail.
	var bareSys System = NewManifestSystem(bare)
	if _, ok := bareSys.(Prober); !ok {
		t.Fatal("the manifest engine should carry the Prober method either way")
	}
	if _, ok := Probes(NewManifestSystem(bare)); ok {
		t.Error("Probes() must say no for a manifest without a probe block")
	}
	if _, ok := Probes(NewManifestSystem(full)); !ok {
		t.Error("Probes() must say yes for a manifest with a probe block")
	}
	if _, ok := WorkChecks(NewManifestSystem(bare)); ok {
		t.Error("WorkChecks() must say no for a manifest without a poll block")
	}
	if _, ok := WorkChecks(NewManifestSystem(full)); !ok {
		t.Error("WorkChecks() must say yes for a manifest with a poll block")
	}
}

func TestManifestProbe(t *testing.T) {
	sys, srv := capSystem(t, func(string) string { return `{"user":{"login":"bot@example"}}` })
	who, err := sys.Probe(context.Background(), Credential{BaseURL: srv.URL, Token: "tok"})
	if err != nil {
		t.Fatal(err)
	}
	if who != "bot@example" {
		t.Errorf("identity = %q, want bot@example", who)
	}

	// The call worked, the field did not match: that is the plugin's problem,
	// not the credential's — blaming the credential would send an operator
	// looking in the wrong place.
	sys2, srv2 := capSystem(t, func(string) string { return `{"user":{}}` })
	who, err = sys2.Probe(context.Background(), Credential{BaseURL: srv2.URL, Token: "tok"})
	if err != nil || who != "ok" {
		t.Errorf("probe without the field = (%q, %v), want (ok, nil)", who, err)
	}
}

func TestManifestProbeReportsHTTPFailure(t *testing.T) {
	m, err := ParseManifest([]byte(capManifest))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"bad token"}`, http.StatusUnauthorized)
	}))
	defer srv.Close()
	sys := NewManifestSystem(m)
	sys.HTTP = srv.Client()
	if _, err := sys.Probe(context.Background(), Credential{BaseURL: srv.URL, Token: "tok"}); err == nil {
		t.Fatal("a 401 must reach the operator as an error")
	} else if !strings.Contains(err.Error(), "401") {
		t.Errorf("error should name the status: %v", err)
	}
}

func TestManifestPoll(t *testing.T) {
	sys, srv := capSystem(t, func(uri string) string {
		switch {
		case strings.HasPrefix(uri, "/issues"):
			return `{"items":[{"updated_at":"b"},{"updated_at":"a"}]}`
		case strings.HasPrefix(uri, "/reviews"):
			return `{"items":[]}`
		}
		return `{}`
	})
	cred := Credential{BaseURL: srv.URL, Token: "tok"}

	has, sig, err := sys.HasWorkSigned(context.Background(), cred, "")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("two open items should count as work")
	}
	// Sorted: the order the target system happens to answer in must not by
	// itself look like news.
	if sig != "a,b" {
		t.Errorf("signature = %q, want a,b", sig)
	}

	has, _, err = sys.HasWorkSigned(context.Background(), cred, "review")
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("an empty list is not work")
	}
}

func TestManifestPollUnknownKindFallsBackToTheGeneralCheck(t *testing.T) {
	// Contract of KindWorkChecker: an unknown sub-scope must not report LESS
	// than the plain check — otherwise a typo in HEARTBEAT.md silently switches
	// an agent off.
	sys, srv := capSystem(t, func(uri string) string {
		if strings.HasPrefix(uri, "/issues") {
			return `{"items":[{"updated_at":"a"}]}`
		}
		return `{"items":[]}`
	})
	has, _, err := sys.HasWorkSigned(context.Background(), Credential{BaseURL: srv.URL, Token: "tok"}, "typo")
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("unknown kind should fall back to the general poll, not report no work")
	}
}

func TestManifestPollWithoutABlockNeverStarves(t *testing.T) {
	m, err := ParseManifest([]byte(demoManifest))
	if err != nil {
		t.Fatal(err)
	}
	has, _, err := NewManifestSystem(m).HasWorkSigned(context.Background(), Credential{}, "")
	if err != nil || !has {
		t.Errorf("a manifest without poll must answer fail-open: (%v, %v)", has, err)
	}
}

func TestManifestPromptDocNarrowsToScopes(t *testing.T) {
	m, err := ParseManifest([]byte(capManifest))
	if err != nil {
		t.Fatal(err)
	}
	sys := NewManifestSystem(m)

	full := sys.PromptDoc()
	for _, want := range []string{"get_issue", "comment", "whoami"} {
		if !strings.Contains(full, want) {
			t.Errorf("full doc is missing %q:\n%s", want, full)
		}
	}

	readOnly := sys.PromptDocForScopes([]string{"read"})
	if !strings.Contains(readOnly, "get_issue") {
		t.Errorf("read scope should keep get_issue:\n%s", readOnly)
	}
	if strings.Contains(readOnly, "write a comment") {
		t.Errorf("read scope should drop the comment action:\n%s", readOnly)
	}
	// An action without a scope belongs to everybody — dropping it would take a
	// capability away that nobody restricted.
	if !strings.Contains(readOnly, "whoami") {
		t.Errorf("an unscoped action must survive narrowing:\n%s", readOnly)
	}

	// Fail-open: no scopes recorded means the full doc, per the
	// ScopedDocSystem contract.
	if sys.PromptDocForScopes(nil) != full {
		t.Error("an empty scope list must yield the full doc")
	}
}

func TestManifestFreeTextDocIsNotNarrowed(t *testing.T) {
	// demoManifest carries prompt_doc as free text and no per-action lines:
	// there is no way to tell which sentence belongs to which action, so the
	// doc stays whole rather than being cut at a guess.
	m, err := ParseManifest([]byte(demoManifest))
	if err != nil {
		t.Fatal(err)
	}
	sys := NewManifestSystem(m)
	if got := sys.PromptDocForScopes([]string{"read"}); got != m.PromptDoc {
		t.Errorf("free text should stay whole, got:\n%s", got)
	}
}

func TestParseManifestValidatesTheNewBlocks(t *testing.T) {
	base := `"name":"x1","webhook":{"id_field":"id"},"actions":{"a":{"method":"GET","path":"/x"%s}}`
	cases := map[string]string{
		"scope not declared":  `{` + strings.Replace(base, "%s", `,"scope":"read"`, 1) + `}`,
		"empty scope entry":   `{` + strings.Replace(base, "%s", "", 1) + `,"scopes":["read",""]}`,
		"scope listed twice":  `{` + strings.Replace(base, "%s", "", 1) + `,"scopes":["read","read"]}`,
		"probe path relative": `{` + strings.Replace(base, "%s", "", 1) + `,"probe":{"path":"me"}}`,
		"poll path relative":  `{` + strings.Replace(base, "%s", "", 1) + `,"poll":{"":{"path":"issues"}}}`,
	}
	for name, raw := range cases {
		if _, err := ParseManifest([]byte(raw)); err == nil {
			t.Errorf("%s: error expected", name)
		}
	}
	ok := `{` + strings.Replace(base, "%s", `,"scope":"read"`, 1) + `,"scopes":["read"],"probe":{"path":"/me"},"poll":{"":{"path":"/issues"}}}`
	if _, err := ParseManifest([]byte(ok)); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}
