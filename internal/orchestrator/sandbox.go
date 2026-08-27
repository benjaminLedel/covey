package orchestrator

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"covey/internal/sandboxfs"
)

// SandboxProvider is the port to the data plane. The sandbox is deliberately
// dumb and replaceable (spec/01): persistent home, ephemeral compute. Shipped
// is the docker provider (sandbox_docker.go, real container isolation);
// E2B/Beam plug into the same interface without changing the orchestrator.
type SandboxProvider interface {
	// Start wakes the sandbox: bring up compute, mount the home, start the daemon.
	Start(ctx context.Context, spec SandboxSpec) (Sandbox, error)
}

type SandboxSpec struct {
	AgentID uuid.UUID
	// OrgID is the agent's organisation. It decides which runner carries this
	// sandbox — and with it which egress segment it hangs in: a runner serves
	// exactly one organisation (spec/16), so two tenants never share an
	// internal network.
	OrgID uuid.UUID
	// Image is the agent's workplace: a profile name (`base`, `dev`), an image
	// reference of its own, or empty for the provider's default. The provider
	// resolves it — only it knows what a profile is called in its world.
	Image string
	// RunnerTags are the capabilities the agent needs of its host. The provider
	// decides what to do with them — for the docker provider on one machine,
	// nothing.
	RunnerTags []string
	HomeDir    string
	Env        map[string]string // COVEY_WS_URL, COVEY_DAEMON_TOKEN, …
	// EgressToken is the per-sandbox token the sandbox uses to identify itself
	// as this agent to the egress proxy (Proxy-Authorization). Empty = no
	// egress enforcement for this sandbox.
	EgressToken string
}

type Sandbox interface {
	// Stop shuts the compute instance down; the home stays.
	Stop(ctx context.Context) error
}

// HomeSyncer is the optional half of a sandbox whose home lives in a central
// store: it writes the home away WITHOUT the compute going down.
//
// It exists because of the warm sandbox. For an agent that falls asleep
// properly, stopping the compute is what carries the home into the store — the
// scan then runs on a home nothing writes into any more. A warm agent never
// stops, so without this its work stays in one container volume on one host
// until something tears that sandbox down, and every way of losing the
// container in between (a hard restart, a prune, an OOM kill) loses the run
// with it. `spec/16-runner.md` decides the other way round: after every job the
// home goes into the store.
//
// Optional, because not every provider has a store to speak of — the mock in
// the tests has none.
type HomeSyncer interface {
	SyncHome(ctx context.Context) error
}

// Discardable is the optional half of a sandbox that can be taken down WITHOUT
// writing its home away.
//
// It exists for the start that never became a run: the container died at once,
// or the daemon never connected. The home is then byte for byte what was
// materialised into it a moment earlier, and syncing it back is a full scan of
// a home that may be gigabytes — measured on a production instance: eleven
// minutes to materialise 8.3 GB, the container gone in under a second, and then
// half an hour of scanning before the failure was even recorded. The agent
// spent forty-five minutes per attempt to arrive exactly where it started.
//
// Optional, because a provider without a store has nothing to skip.
type Discardable interface {
	Discard(ctx context.Context) error
}

// Placed is the optional half of a sandbox that knows which host it landed on.
// Optional because not every provider has hosts to speak of — the mock in the
// tests has none — and because the answer is worth a line in the recording
// rather than a change to everything that starts a sandbox.
//
// It exists for the question an operator asks in front of a run that behaved
// oddly: WHERE did this run? With one machine it is not a question; with a
// second one it is the first one.
type Placed interface {
	// Runner names the host: its id, and the label it carries in the runner
	// view at this moment.
	Runner() (uuid.UUID, string)
}

// DataPlaneChecker is the optional self-check of a SandboxProvider: can it
// start a sandbox at all, asked without starting one.
//
// Optional because the answer is provider-specific and some providers have no
// meaningful way to give it in advance. Whoever implements it lets the platform
// say at startup — and in the interface — what an agent would otherwise run
// into on its first wake. An empty result means nothing is in the way; each
// message names its own remedy.
type DataPlaneChecker interface {
	Check(ctx context.Context) []string
}

// FileAccess is the optional second port of a SandboxProvider: the route to an
// agent's persistent home. It hangs off the provider because only the provider
// knows where the home lives — a directory on the host for the docker provider,
// something else for a future remote provider. Whoever does not implement it
// has no file browser; the feature then switches itself off instead of guessing.
//
// Access deliberately does *not* go through the daemon protocol: the home is
// there even while the sandbox sleeps — and asleep is the normal state.
type FileAccess interface {
	// AgentFiles opens an agent's home as a file tree. It need not exist: an
	// agent that was never woken does not have one yet, and the listing is then
	// empty rather than an error.
	//
	// A tree and no longer a path: with a runner on another host the home is
	// not on this machine, and when that host is offline the last snapshot is
	// what can still be read. The caller sees one interface.
	AgentFiles(agentID uuid.UUID) (sandboxfs.Tree, error)
}

// ErrNoFileAccess: the configured provider knows no reachable home.
var ErrNoFileAccess = errors.New("file access: the sandbox provider has no reachable home")

// AgentFiles opens an agent's home as a file tree (spec/02: the workplace). If
// the provider lacks the FileAccess port, ErrNoFileAccess is returned.
func (o *Orchestrator) AgentFiles(agentID uuid.UUID) (sandboxfs.Tree, error) {
	fa, ok := o.Provider.(FileAccess)
	if !ok {
		return nil, ErrNoFileAccess
	}
	return fa.AgentFiles(agentID)
}
