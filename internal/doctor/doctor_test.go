package doctor

import (
	"testing"

	"covey/internal/config"
	"covey/internal/sandbox"
)

// Which image a profile name means is decided in three steps: environment,
// catalogue, compiled default (spec/16). The doctor knew the first and the
// third, because the map it read had been built with a nil catalogue — and so
// it reported three missing local build products on an installation that runs
// the published images and needs none of them (#118).
//
// This holds the order the wake uses, from the doctor's side.
func TestCatalogueBeatsTheCompiledDefault(t *testing.T) {
	cfg := config.Config{}
	katalog := map[string]string{
		"base": "ghcr.io/benjaminledel/covey-sandbox@sha256:aaa",
	}

	ohne := sandbox.Resolve(cfg.SandboxImageEnv, nil)
	if ohne["base"] == katalog["base"] {
		t.Fatal("without a catalogue the compiled default has to stand — otherwise this test proves nothing")
	}
	// And that default is precisely the name that only exists on a machine
	// which built it: the answer the doctor used to give.
	if sandbox.Pullable(ohne["base"]) {
		t.Errorf("the compiled default %q looks pullable; the case this is about is that it is not", ohne["base"])
	}

	mit := sandbox.Resolve(cfg.SandboxImageEnv, katalog)
	if mit["base"] != katalog["base"] {
		t.Errorf("the catalogue did not win: %q", mit["base"])
	}
	// Which turns "missing, build it" into "the runner fetches it on the first
	// wake" — a fact instead of an errand.
	if !sandbox.Pullable(mit["base"]) {
		t.Errorf("a published reference has to be fetchable: %q", mit["base"])
	}

	// The environment still beats both: an instance that pins its own image
	// means it.
	eigen := map[string]string{"base": "registry.example.com/eigenes:1"}
	if got := sandbox.Resolve(eigen, katalog)["base"]; got != eigen["base"] {
		t.Errorf("the environment has to beat the catalogue: %q", got)
	}
}
