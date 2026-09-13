package doctor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/config"
	"covey/internal/daemon"
	"covey/internal/engines"
)

// The failure this is about: an agent on an engine whose CLI is in no image and
// in no catalogue, nothing said so, and the answer arrived half an hour later as
// `executable file not found in $PATH` (#221). Everything needed to say it
// earlier was known — so it is said here.
func TestAnEngineWithoutACLIAnywhereIsNamedBeforeTheRun(t *testing.T) {
	o := EngineOrigins(context.Background(), config.Config{}, nil, []string{"sevencode"})["sevencode"]
	if o.Settled() {
		t.Fatalf("without a catalogue and without the variable nothing is settled: %+v", o)
	}
	if !strings.Contains(o.Detail(), "sevencode") {
		t.Errorf("the detail has to name the binary somebody then searches for: %q", o.Detail())
	}
	warn := o.AssignmentWarning()
	// The point of the warning is that it carries the sentence the failed task
	// would produce — that is what somebody pastes into a search field.
	if !strings.Contains(warn, "executable file not found") {
		t.Errorf("the warning does not name the failure it prevents: %q", warn)
	}
	if !strings.Contains(warn, daemon.CLI("sevencode").Env) {
		t.Errorf("the warning does not name the variable that would fix it: %q", warn)
	}
}

// An operator who names the path has answered the question, and being told
// about it again is how a check becomes furniture.
func TestAPathOnTheHostSettlesIt(t *testing.T) {
	t.Setenv(daemon.CLI("sevencode").Env, "/opt/seven/bin/sevencode")
	o := EngineOrigins(context.Background(), config.Config{}, nil, []string{"sevencode"})["sevencode"]
	if !o.Settled() || o.AssignmentWarning() != "" {
		t.Fatalf("a named path has to settle it: %+v", o)
	}
	if !strings.Contains(o.Detail(), "/opt/seven/bin/sevencode") {
		t.Errorf("the detail has to name the path: %q", o.Detail())
	}
}

// The catalogue is the other answer, and the one an installation is meant to
// use: the runner installs the layer before the sandbox starts (spec/26).
func TestTheCatalogueSettlesItAndNamesTheVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(engines.Catalog{
			Schema: engines.CatalogSchema,
			Engines: []engines.Entry{{Name: "sevencode", Versions: []engines.Release{
				{Version: "0.0.2", Kind: engines.KindNpm, Package: "sevencode"},
			}}},
		})
	}))
	defer srv.Close()

	o := EngineOrigins(context.Background(), config.Config{EngineCatalogURL: srv.URL}, nil,
		[]string{"sevencode"})["sevencode"]
	if !o.Settled() {
		t.Fatalf("a catalogue release has to settle it: %+v", o)
	}
	if o.Version != "0.0.2" {
		t.Errorf("version = %q, expected the release the catalogue names", o.Version)
	}
	if o.AssignmentWarning() != "" {
		t.Errorf("nothing to warn about, and yet: %q", o.AssignmentWarning())
	}
}

// An engine that needs no binary must not be asked about one — otherwise every
// demo installation carries a permanent warning about the mock.
func TestAnEngineWithoutACLIIsNotAsked(t *testing.T) {
	o := EngineOrigins(context.Background(), config.Config{}, nil, []string{"mock"})["mock"]
	if !o.Settled() || o.AssignmentWarning() != "" {
		t.Fatalf("the mock needs no CLI: %+v", o)
	}
}

// An engine this build does not register is a different failure: no adapter,
// not a missing file. It blocks, and the remedy is another build.
func TestAnUnknownEngineSaysSoRatherThanNamingABinary(t *testing.T) {
	o := EngineOrigins(context.Background(), config.Config{}, nil, []string{"not-an-engine"})["not-an-engine"]
	if o.Known {
		t.Fatal("a name nothing registers must not come back as known")
	}
	if !strings.Contains(o.AssignmentWarning(), "not-an-engine") {
		t.Errorf("the warning has to name the engine: %q", o.AssignmentWarning())
	}
}

// Every engine this build registers has to say what it needs in the sandbox —
// the declaration is what makes the question answerable at all, and an engine
// added later must not slip past it silently. Empty is a valid answer (the
// mock); a binary without the variable that overrides it is not, because that
// variable is what the runner sets when it installs the engine (spec/26).
func TestEveryRegisteredEngineDeclaresItsCLI(t *testing.T) {
	for _, d := range daemon.Runtimes() {
		if d.CLI.Name == "" {
			continue
		}
		if d.CLI.Env == "" {
			t.Errorf("engine %s names the binary %q and no variable to override it", d.Name, d.CLI.Name)
		}
	}
}
