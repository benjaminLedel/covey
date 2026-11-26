package wasmplug

import (
	"context"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// TestDescriptionDeclaresItsCredentials is #777: a module could not say that it
// needs no secret, and the broker refused every action of a plugin whose
// sources are all public. The field maps onto the descriptor flags a compiled
// plugin sets, and anything it does not recognise keeps the broker closed.
func TestDescriptionDeclaresItsCredentials(t *testing.T) {
	cases := []struct {
		value                   string
		none, optional, baseURL bool
	}{
		{"", false, false, false},
		{CredentialsNone, true, false, true},
		{CredentialsOptional, false, true, true},
		// A typo, or a value from a newer protocol, is the default.
		{"nope", false, false, false},
		{"None", false, false, false},
	}
	for _, c := range cases {
		d := Description{Name: "public", Label: "Public", Scopes: []string{"read"}, Credentials: c.value}.Descriptor()
		if d.NoCredentials != c.none || d.CredentialsOptional != c.optional || d.BaseURLOptional != c.baseURL {
			t.Errorf("credentials %q: none=%v optional=%v base_url_optional=%v, want %v %v %v",
				c.value, d.NoCredentials, d.CredentialsOptional, d.BaseURLOptional, c.none, c.optional, c.baseURL)
		}
		if d.Name != "public" || d.Label != "Public" || d.Kind != "wasm" || len(d.Scopes) != 1 {
			t.Errorf("credentials %q: descriptor lost the description: %+v", c.value, d)
		}
	}
}

// TestStoredDescriptionCarriesTheDeclaration: the broker reads the flags from
// the row written at install time, so the field has to survive the round trip
// without the module being compiled again.
func TestStoredDescriptionCarriesTheDeclaration(t *testing.T) {
	raw, _, err := Pack(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	d, ok := StoredDescription(raw)
	if !ok {
		t.Fatal("stored description unreadable")
	}
	if d.Credentials != CredentialsRequired {
		t.Errorf("the demo module declares nothing, stored %q", d.Credentials)
	}
}

// TestRelativeFetchWithoutBaseURLNamesTheSecret: a plugin granted without a
// secret has no base URL. A relative request then must not go to the bare
// path; it fails with the name of the secret that would fix it.
func TestRelativeFetchWithoutBaseURLNamesTheSecret(t *testing.T) {
	mod, err := Compile(context.Background(), demoModule(t))
	if err != nil {
		t.Fatal(err)
	}
	defer mod.Close(context.Background())
	sys := NewSystem(mod)

	resp := sys.fetcher(target.Credential{})(context.Background(), FetchRequest{Method: "GET", Path: "/issues/7"})
	if resp.Error == "" || !strings.Contains(resp.Error, "demo_url") {
		t.Errorf("a relative fetch without a base URL has to name demo_url: %+v", resp)
	}
}
