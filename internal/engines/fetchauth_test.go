package engines

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFetchSendsTheHeaderTheEntryNames(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if v := r.Header.Get("PRIVATE-TOKEN"); v != "" {
			got = v
		}
		w.Write([]byte("payload"))
	}))
	defer srv.Close()

	release := func() Release {
		return Release{Version: "1.0.27", Kind: KindTarball, URL: srv.URL + "/a.tgz",
			Integrity:  "sha256:" + strings.Repeat("a", 64),
			AuthHeader: "PRIVATE-TOKEN", AuthEnv: "COVEY_TEST_ARTIFACT_TOKEN"}
	}

	t.Setenv("COVEY_TEST_ARTIFACT_TOKEN", "glpt-abc")
	body, err := fetchArtifact(t.Context(), srv.Client(), release(), 1<<20, nil, Auth{})
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(body) != "payload" {
		t.Errorf("body: %q", body)
	}
	if got != "glpt-abc" {
		t.Errorf("the header went out as %q, want the value of the named variable", got)
	}

	// Without the variable: refused before the request rather than answered 401,
	// and the reason names the variable so the state ends by setting it.
	fresh := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			t.Error("an artefact known to need a token must not be fetched anonymously")
		}))
	}
	srv2 := fresh()
	defer srv2.Close()
	r2 := release()
	r2.URL = srv2.URL + "/a.tgz"
	// A different variable, never set: what is under test is the missing one, and
	// `t.Setenv` holds for the whole test rather than for the block around it.
	r2.AuthEnv = "COVEY_TEST_MISSING_TOKEN"
	_, err = fetchArtifact(t.Context(), srv2.Client(), r2, 1<<20, nil, Auth{})
	if err == nil || !strings.Contains(err.Error(), "COVEY_TEST_MISSING_TOKEN") {
		t.Errorf("the reason should name the variable that is missing: %v", err)
	}
}
