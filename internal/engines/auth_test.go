package engines

import (
	"strings"
	"testing"
)

// What Valid() has to refuse about an artefact behind a login, and what it has to
// let through. The rules exist because the document is fetched from somewhere
// else and acted on by a runner that cannot ask anybody: the parse is the one
// moment at which a half-written entry can still be named.
func TestReleaseAuthComesAsAReference(t *testing.T) {
	tar := Release{Version: "1.0.27", Kind: KindTarball, URL: "https://host/a.tgz",
		Integrity: "sha256:" + strings.Repeat("a", 64)}

	// The good case, first: an entry that will work must not be stopped by a rule
	// written around it.
	ok := tar
	ok.AuthHeader, ok.AuthEnv = "PRIVATE-TOKEN", "COVEY_SEVENCODE_ARTIFACT_TOKEN"
	if err := ok.Valid(); err != nil {
		t.Fatalf("a header with the variable that holds its token is what the mechanism is for: %v", err)
	}

	for name, mut := range map[string]func(r *Release){
		// A header with nothing to put in it is a header that is never sent, and
		// the wake then fails with a 401 that reads like a broken catalogue.
		"header without the variable": func(r *Release) { r.AuthHeader = "PRIVATE-TOKEN" },
		"variable without the header": func(r *Release) { r.AuthEnv = "SOME_TOKEN" },
		// A path, a URL or the secret itself where the NAME of a secret belongs.
		// Accepted here, the value would sit in a document that is meant to be
		// publishable on its own.
		"a value instead of a name": func(r *Release) { r.AuthHeader, r.AuthEnv = "PRIVATE-TOKEN", "glpt-abc123" },
		"a path instead of a name":  func(r *Release) { r.AuthHeader, r.AuthEnv = "PRIVATE-TOKEN", "/etc/sevencode/token" },
	} {
		r := tar
		mut(&r)
		if err := r.Valid(); err == nil {
			t.Errorf("%s: the document was accepted and a runner has to act on it", name)
		}
	}

	// The npm kind is installed by the package manager rather than fetched, so a
	// header on it would be read by nothing. An inert flag says the installation
	// is protected when it is not.
	npm := Release{Version: "2.1.0", Kind: KindNpm, Package: "@anthropic-ai/claude-code",
		AuthHeader: "PRIVATE-TOKEN", AuthEnv: "TOKEN"}
	if err := npm.Valid(); err == nil {
		t.Error("an auth pair on an npm release is read by nothing, so it should be refused here")
	}
}
