package doctor

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/config"
	"covey/internal/daemon"
	"covey/internal/engines"
	"covey/internal/marketplace"
)

// Where an engine's CLI comes from — asked before a run rather than after one.
//
// The failure this answers looked like this: an agent was put on an engine whose
// CLI was in no image and in no catalogue, nothing said so at the assignment,
// and the first task ended with `exec: "sevencode": executable file not found in
// $PATH` (#221). Everything needed to say it earlier was already known: the
// agent's engine, the descriptor's CLI declaration, the catalogue, the
// operator's own variable.
//
// What this CANNOT say is whether a workplace image happens to carry the binary.
// Looking inside an image means starting a container, and the doctor reads. So
// the honest answer in that case is "the image has to carry it", with the error
// text it would produce if it does not — that is the sentence somebody searches
// for afterwards.
type EngineOrigin struct {
	Engine string
	// Known: this build registers the engine. An unknown one is not a missing
	// binary but a missing adapter, and the two lead somewhere different.
	Known bool
	// Binary is the command the run calls. Empty = the engine needs none (the
	// mock), and then nothing here is in question.
	Binary string
	// Env is the variable that names another path for it.
	Env string
	// Path is set when THIS process's environment names one. It is the highest
	// precedence (spec/26) and it is also the narrowest answer: a remote runner
	// has its own environment, which nothing here can read.
	Path string
	// Version and Kind are the catalogue's answer, when it has one.
	Version string
	Kind    string
}

// Settled reports whether the CLI's origin is known rather than assumed: the
// engine needs none, an operator named a path, or the catalogue names a release.
// False means the workplace image has to carry it, and nobody here can check.
func (o EngineOrigin) Settled() bool {
	return !o.Known || o.Binary == "" || o.Path != "" || o.Version != ""
}

// Detail is the one line that says where the binary comes from.
func (o EngineOrigin) Detail() string {
	switch {
	case !o.Known:
		return "this build does not know the engine"
	case o.Binary == "":
		return "needs no CLI in the sandbox"
	case o.Path != "":
		return o.Env + " names " + o.Path + " on this host"
	case o.Version != "":
		return "catalogue release " + o.Version + " (" + o.Kind + ") — the runner installs it before the sandbox starts"
	default:
		return "neither the engine catalogue nor " + o.Env + " names it, so the workplace image has to carry `" + o.Binary + "`"
	}
}

// EngineOrigins answers for several engines at once, sharing one catalogue
// fetch. An installation without a catalogue URL asks nothing over the network.
func EngineOrigins(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, list []string) map[string]EngineOrigin {
	var source *engines.Source
	if strings.TrimSpace(cfg.EngineCatalogURL) != "" {
		var cache marketplace.Cache
		if pool != nil {
			cache = marketplace.NewPgCache(pool)
		}
		source = engines.NewSource(cfg.EngineCatalogURL, cache, nil)
	}
	out := make(map[string]EngineOrigin, len(list))
	for _, engine := range list {
		o := EngineOrigin{Engine: engine}
		desc, known := daemon.Describe(engine)
		o.Known = known
		if !known {
			out[engine] = o
			continue
		}
		o.Binary, o.Env = desc.CLI.Name, desc.CLI.Env
		if o.Binary == "" {
			out[engine] = o
			continue
		}
		if o.Env != "" {
			o.Path = strings.TrimSpace(os.Getenv(o.Env))
		}
		if o.Path == "" && source.Enabled() {
			if r, ok := source.For(ctx, engine, ""); ok {
				o.Version, o.Kind = r.Version, r.Kind
			}
		}
		out[engine] = o
	}
	return out
}

// LookupEngineOrigin answers for one engine — the shape the assignment needs,
// which looks at one agent.
func LookupEngineOrigin(ctx context.Context, cfg config.Config, pool *pgxpool.Pool, engine string) EngineOrigin {
	return EngineOrigins(ctx, cfg, pool, []string{engine})[engine]
}

// AssignmentWarning is what to show somebody who has just put an agent on an
// engine, and empty when there is nothing to say. A warning, never a refusal:
// an image can catch up, and a platform that refuses the assignment forces the
// order to be right rather than the outcome.
func (o EngineOrigin) AssignmentWarning() string {
	switch {
	case !o.Known:
		return "this installation does not know the engine " + o.Engine + " — its runs cannot start until a build that carries it is deployed"
	case o.Settled():
		return ""
	default:
		return "the engine " + o.Engine + " needs the CLI `" + o.Binary + "` in the sandbox. " +
			"Neither the engine catalogue nor " + o.Env + " names it here, so the workplace image has to carry it — " +
			"otherwise the first task fails with `executable file not found in $PATH`."
	}
}

// checkEngines is the doctor's side of the same question, over every engine an
// agent is set to.
func (d *doctor) checkEngines(ctx context.Context, pool *pgxpool.Pool) {
	inUse, err := agents.NewRegistry(pool).EnginesInUse(ctx)
	if err != nil {
		d.problem("engines", "not readable: "+err.Error(), "", false)
		return
	}
	if len(inUse) == 0 {
		return
	}
	names := make([]string, 0, len(inUse))
	for engine := range inUse {
		names = append(names, engine)
	}
	sort.Strings(names)
	origins := EngineOrigins(ctx, d.cfg, pool, names)

	for _, engine := range names {
		o := origins[engine]
		what := "engine " + engine
		count := fmt.Sprintf("%d agent(s)", inUse[engine])
		switch {
		case !o.Known:
			// Blocking: this is not a binary that may turn up, it is an engine
			// this build has no adapter for. Every run of those agents fails.
			d.problem(what, count+", and this build does not know it",
				"deploy a covey that carries the engine, or move the agents to one that is registered", true)
		case o.Settled():
			d.ok(what, count+" — "+o.Detail())
		default:
			// Not blocking: the image may well carry it, and saying "blocking"
			// about something unverifiable is how a check becomes furniture.
			d.problem(what, count+" — "+o.Detail(),
				"point COVEY_ENGINE_CATALOG_URL at a catalogue that lists "+engine+
					", set "+o.Env+" to its path, or use a workplace whose image carries it. "+
					"Without one of the three the first task fails with `executable file not found in $PATH`.", false)
		}
	}
}
