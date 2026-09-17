package engines

import (
	"os"
	"strings"
	"testing"
)

// The published address is where the document has to stand, not a suggestion
// beside it: a default that points at nothing puts a fetch that fails on every
// wake of every installation. Derived from the source address, so a fork that
// publishes its own engines carries its own — the same rule the workplace
// catalogue has.
func TestDefaultCatalogURLIsThePublishedPath(t *testing.T) {
	want := "https://raw.githubusercontent.com/benjaminLedel/covey/catalog/engine-catalog.json"
	if got := DefaultCatalogURL(); got != want {
		t.Fatalf("DefaultCatalogURL() = %q, expected %q", got, want)
	}
}

// The document this repository ships is read by the parse below and by nothing
// else — no code path here consumes the file. The test is therefore the review:
// it runs the same parse a runner runs over the bytes that will be published. A
// release that cannot be installed (a tarball without a digest, an entry without
// a version) is refused here, at the change, and not at the first wake of an
// agent — where the reader is an operator who cannot tell whether the fault lies
// with the platform or with the document.
func TestShippedCatalogueIsInstallable(t *testing.T) {
	body, err := os.ReadFile("engine-catalog.json")
	if err != nil {
		t.Fatalf("the document that is published has to stand in the repository: %v", err)
	}
	cat := parseDoc(t, string(body))
	if len(cat.Engines) == 0 {
		t.Fatal("a catalogue with no entry switches no engine on")
	}
	for _, e := range cat.Engines {
		for _, r := range e.Versions {
			if err := r.Valid(); err != nil {
				t.Errorf("%s %s: %v", e.Name, r.Version, err)
			}
		}
		// What an instance gets without pinning is the entry's last release, so
		// the claims below are about the release that is actually served.
		last := e.Versions[len(e.Versions)-1]
		if last.BinaryEnv == "" {
			t.Errorf("%s %s: no binary_env — the adapter would not find its CLI",
				e.Name, last.Version)
		}
		if len(last.Requires) == 0 {
			t.Errorf("%s %s: no requires — a host that does not meet it then exits 127 "+
				"instead of saying what was missing", e.Name, last.Version)
		}
		// Behind a login only as far as a variable NAME. A document served over a
		// public URL carries no secret; the digest is what vouches for the bytes.
		if (last.AuthHeader == "") != (last.AuthEnv == "") {
			t.Errorf("%s %s: auth_header and auth_env come in pairs — one alone names "+
				"either nothing or a value", e.Name, last.Version)
		}
		for _, kv := range last.Env {
			k, v, found := strings.Cut(kv, "=")
			if !found || k == "" || v == "" {
				t.Errorf("%s %s: env entry %q is no KEY=value with both sides filled",
					e.Name, last.Version, kv)
			}
		}
	}
}
