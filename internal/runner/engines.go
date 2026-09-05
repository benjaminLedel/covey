package runner

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"covey/internal/engines"
)

// catalogueBudget is how long a sandbox start waits for the catalogue DOCUMENT.
// It is short and deliberate: the fetch sits on the wake path, and an
// installation that has not published a catalogue (or whose catalogue host has
// just gone quiet) would otherwise pay the feed's fetch timeout on every single
// start — there is no negative caching, because a document that failed to arrive
// once is exactly the document that should be tried again next time.
//
// The artefact download that follows is deliberately NOT bounded this way: an
// engine archive is hundreds of megabytes, and cutting that off in the middle
// would read like a network fault.
const catalogueBudget = 5 * time.Second

// catalogueRelease is the catalogue's answer about one engine, under that
// budget. Silence means "the image carries the engine", not "the engine is
// broken" — see the four quiet cases in engineLayer.
func (p *Docker) catalogueRelease(ctx context.Context, engine string) (engines.Release, bool) {
	ctx, cancel := context.WithTimeout(ctx, catalogueBudget)
	defer cancel()
	return p.Engines.For(ctx, engine, "")
}

// The engine layer: the runtime binary a sandbox is given, installed on this
// host rather than baked into the image (spec/26).
//
// The whole reason this sits here is a multiplication: an image per engine per
// workplace does not scale, and an installation that wants an engine the project
// does not publish could not have it without building images. A catalogue entry
// and a runner-side layer replace both.
//
// Three cases return nothing and no error, because nothing is to say: the start
// names no engine, the instance configured no catalogue, or the catalogue knows
// nothing about this engine. All three mean "the image carries it" — the state
// of the world before this file existed, and still the right one for a workplace
// that ships its engine.
//
// The fourth case is the one that fails the start: the catalogue DOES name this
// engine and the layer cannot be produced. Falling back to whatever binary the
// image happens to hold is the drift this mechanism exists to close — a run
// recorded against version 1.0.7 that ran on a 0.9 in the image is a wrong
// record, not a degraded one. So the reason goes back over the protocol and the
// task fails with it.
func (p *Docker) engineLayer(ctx context.Context, spec StartSandbox) (env, mount []string, err error) {
	if spec.Engine == "" || p.Engines == nil || !p.Engines.Enabled() || p.EngineStore == nil {
		return nil, nil, nil
	}
	r, ok := p.catalogueRelease(ctx, spec.Engine)
	if !ok {
		return nil, nil, nil
	}
	// An operator who names the binary on this host outranks the catalogue, in
	// the same spirit as COVEY_SANDBOX_IMAGE_<PROFILE> outranking the workplace
	// catalogue: the explicit local word wins, and an installation debugging a
	// engine build must not have to edit a published document to do it.
	if v := strings.TrimSpace(os.Getenv(r.BinaryEnvName(spec.Engine))); v != "" {
		return nil, nil, nil
	}
	layer, err := p.EngineStore.Ensure(ctx, r)
	if err != nil {
		return nil, nil, fmt.Errorf("engine %s: %w", spec.Engine, err)
	}
	host, container := engines.ContainerMount(layer)
	return engines.ContainerEnv(layer, r),
		[]string{"-v", host + ":" + container + ":ro"}, nil
}
