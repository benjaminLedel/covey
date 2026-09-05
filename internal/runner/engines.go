package runner

import (
	"context"
	"fmt"
	"os"
	"strings"

	"covey/internal/engines"
)

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
	r, ok := p.Engines.For(ctx, spec.Engine, "")
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
