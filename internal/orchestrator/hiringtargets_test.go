package orchestrator

import (
	"reflect"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// TestCredentialStateSaysWhatANewColleagueCouldUse is #642: list_targets
// reported a switched-on system as if it were usable, and the HR agent drafted
// a colleague around a mailbox nobody had stored a key for.
func TestCredentialStateSaysWhatANewColleagueCouldUse(t *testing.T) {
	target.Register(target.Descriptor{Name: "lokal-642", Label: "Lokal", NoCredentials: true})
	target.Register(target.Descriptor{Name: "fest-642", Label: "Fester Endpunkt", BaseURLOptional: true})
	target.Register(target.Descriptor{Name: "frei-642", Label: "Öffentlich", CredentialsOptional: true})

	cases := []struct {
		name, kind string
		stored     []string
		want       string
		missing    []string
	}{
		{"lokal-642", "builtin", nil, "not needed", nil},
		{"post-642", "custom", nil, "missing", []string{"post-642_token", "post-642_url"}},
		{"post-642", "custom", []string{"post-642_token"}, "missing", []string{"post-642_url"}},
		{"post-642", "custom", []string{"post-642_token", "post-642_url"}, "in place", nil},
		{"fest-642", "builtin", []string{"fest-642_token"}, "in place", nil},
		{"fest-642", "builtin", nil, "missing", []string{"fest-642_token"}},
		{"frei-642", "builtin", nil, "not needed", nil},
		{"frei-642", "builtin", []string{"frei-642_token"}, "in place", nil},
		{"server-642", "mcp", nil, "not needed", nil},
	}
	for _, c := range cases {
		stored := map[string]bool{}
		for _, k := range c.stored {
			stored[k] = true
		}
		got, missing := credentialState(c.name, c.kind, stored)
		if got != c.want || !reflect.DeepEqual(missing, c.missing) {
			t.Errorf("%s (%s) with %v: %q %v, want %q %v", c.name, c.kind, c.stored, got, missing, c.want, c.missing)
		}
	}
}
