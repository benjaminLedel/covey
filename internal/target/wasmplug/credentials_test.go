package wasmplug

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// A wasm plugin says in its description whether it needs the brokered secrets;
// the control plane reads that through the same descriptor fields a compiled
// plugin sets. A value the host does not know must read as the default, so a
// typo cannot open the broker.
func TestDescriptionCredentialsMapOntoTheDescriptor(t *testing.T) {
	cases := []struct {
		credentials               string
		none, optional, urlOption bool
	}{
		{CredentialsRequired, false, false, false},
		{CredentialsOptional, false, true, true},
		{CredentialsNone, true, false, true},
		{"None", false, false, false},
		{"maybe", false, false, false},
	}
	for _, c := range cases {
		d := Description{Name: "demo", Credentials: c.credentials}.Descriptor()
		if d.NoCredentials != c.none || d.CredentialsOptional != c.optional || d.BaseURLOptional != c.urlOption {
			t.Errorf("credentials %q: NoCredentials=%v CredentialsOptional=%v BaseURLOptional=%v, want %v %v %v",
				c.credentials, d.NoCredentials, d.CredentialsOptional, d.BaseURLOptional, c.none, c.optional, c.urlOption)
		}
		if d.Name != "demo" || d.Kind != "wasm" || d.Category != target.CategoryOther {
			t.Errorf("credentials %q: descriptor = %+v", c.credentials, d)
		}
	}
}

// The declaration has to survive storage: the broker reads the stored
// description, not a freshly compiled module.
func TestStoredDescriptionKeepsCredentials(t *testing.T) {
	raw, err := json.Marshal(Stored{Wasm: "", Describe: Description{Name: "demo", Credentials: CredentialsNone}})
	if err != nil {
		t.Fatal(err)
	}
	d, ok := StoredDescription(raw)
	if !ok || d.Credentials != CredentialsNone {
		t.Fatalf("stored description = %+v, %v", d, ok)
	}
	// And a description from before the field existed reads as the default.
	d, ok = StoredDescription([]byte(`{"wasm":"","describe":{"name":"demo"}}`))
	if !ok || d.Credentials != CredentialsRequired || d.Descriptor().NoCredentials {
		t.Fatalf("an old stored description has to stay required: %+v", d)
	}
}

// A plugin granted without <name>_url has no brokered system. A relative fetch
// then has to say that, rather than request the bare path and come back with
// an error about a missing scheme; an absolute URL on a declared host goes out
// as before.
func TestRelativeFetchWithoutBaseURLSaysSo(t *testing.T) {
	var hits int
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Write([]byte(`{"ok":true}`))
	}))
	defer foreign.Close()

	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())
	sys := NewSystem(mod)
	sys.HTTP = foreign.Client()
	sys.desc.Hosts = []string{strings.TrimPrefix(foreign.URL, "http://")}

	f := sys.fetcherAllowingHTTP(target.Credential{})
	resp := f(context.Background(), FetchRequest{Method: "GET", Path: "/issues/7"})
	if !strings.Contains(resp.Error, "no base URL") || !strings.Contains(resp.Error, "demo_url") {
		t.Errorf("a relative fetch without a base URL has to say so: %+v", resp)
	}
	if hits != 0 {
		t.Error("a relative fetch without a base URL must not reach anything")
	}

	resp = f(context.Background(), FetchRequest{Method: "GET", Path: foreign.URL + "/public"})
	if resp.Error != "" || resp.Status != http.StatusOK || hits != 1 {
		t.Errorf("a declared host has to stay reachable without a base URL: %+v (hits %d)", resp, hits)
	}
}
