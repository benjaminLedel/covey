package engines

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// One artefact behind a login, three hosts in front of it: the one whose
// organisation holds the secret, the one that only has a variable of its own,
// and the one with neither. What separates them is what the start carries, and
// the refusal has to name both places it looked — a message that names one
// sends an operator to the wrong machine (#289).
func TestSecretOfTheOrganisationOpensTheArtefact(t *testing.T) {
	var seen string
	body := []byte("#!/bin/sh\necho one\n")
	art := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get("Authorization")
		if seen == "" {
			http.Error(w, "401", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write(body)
	}))
	defer art.Close()

	sum := sha256Hex(body)
	base := func() Release {
		return Release{engine: "sevencode", Version: "1.0.27", Kind: KindFile,
			URL: art.URL + "/cli/latest", Integrity: "sha256:" + sum,
			AuthHeader: "Authorization", AuthSecret: "sevencode_api_token",
			AuthEnv: "COVEY_SEVENCODE_DOWNLOAD_TOKEN"}
	}

	// The brokered value wins, and the host's own export does not shadow it: the
	// secret is what somebody configured, the variable is the fallback.
	t.Setenv("COVEY_SEVENCODE_DOWNLOAD_TOKEN", "Bearer von-der-host")
	dir := t.TempDir()
	store := &Store{Dir: filepath.Join(dir, "engines")}
	layer, err := store.Ensure(t.Context(), base(), Auth{Secret: "Bearer sc-organisation"})
	if err != nil {
		t.Fatalf("the agent's secret opens the artefact: %v", err)
	}
	if seen != "Bearer sc-organisation" {
		t.Fatalf("the header carries the brokered value, got %q", seen)
	}
	if got, err := os.ReadFile(layer.Exec); err != nil || string(got) != string(body) {
		t.Fatal("the bytes behind the login are the ones that landed")
	}

	// No secret, but the host holds a token of its own — a mirror with nothing in
	// covey behind it. That path stays open.
	seen = ""
	store2 := &Store{Dir: filepath.Join(t.TempDir(), "engines")}
	if _, err := store2.Ensure(t.Context(), base(), Auth{}); err != nil {
		t.Fatalf("the host variable is the fallback it is documented as: %v", err)
	}
	if seen != "Bearer von-der-host" {
		t.Fatalf("the fallback is used when there is nothing else, got %q", seen)
	}

	// Neither, and the refusal has to end the search: both names, because the two
	// are set on different machines and the reader is on neither.
	t.Setenv("COVEY_SEVENCODE_DOWNLOAD_TOKEN", "")
	store3 := &Store{Dir: filepath.Join(t.TempDir(), "engines")}
	_, err = store3.Ensure(t.Context(), base(), Auth{})
	if err == nil {
		t.Fatal("an artefact known to need a login must not be fetched anonymously")
	}
	for _, want := range []string{"sevencode_api_token", "COVEY_SEVENCODE_DOWNLOAD_TOKEN", "Authorization"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal has to name %s, got: %v", want, err)
		}
	}
	// Not one line more than that: the document is public, the value is not.
	if strings.Contains(strings.ToLower(err.Error()), "sc-organ") {
		t.Errorf("a refusal that quotes the token turns a log line into a credential: %v", err)
	}
}

// What a secret field holds is the token the vendor handed out — `sc_…`, `glpat…`
// — and `Authorization` is the one header that insists on a scheme in front of
// it. Sent verbatim that answers 401, and the wake blames the catalogue while
// the state is one missing word (#289). So the scheme is completed for that
// header, and only when the value brings none of its own.
func TestATokenWithoutASchemeIsNotSentBare(t *testing.T) {
	cases := []struct {
		name, header, stored, want string
	}{
		{"bare token", "Authorization", "sc_KPD1kg", "Bearer sc_KPD1kg"},
		{"scheme already there", "Authorization", "Bearer sc_KPD1kg", "Bearer sc_KPD1kg"},
		{"another scheme, kept", "Authorization", "Token abc.123", "Token abc.123"},
		{"basic, kept", "Authorization", "Basic Zm9vOmJhcg==", "Basic Zm9vOmJhcg=="},
		// A registry's own header carries no scheme by design — adding one there
		// would be the same mistake in the other direction.
		{"registry header untouched", "PRIVATE-TOKEN", "glpat-abc", "glpat-abc"},
		{"api-key header untouched", "X-Api-Key", "abc def", "abc def"},
	}
	for _, c := range cases {
		if got := authHeaderValue(c.header, c.stored); got != c.want {
			t.Errorf("%s: %q in front of %q, want %q", c.name, got, c.header, c.want)
		}
	}
}
