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
	HomeDir string
	Env     map[string]string // COVEY_WS_URL, COVEY_DAEMON_TOKEN, …
	// EgressToken is the per-sandbox token the sandbox uses to identify itself
	// as this agent to the egress proxy (Proxy-Authorization). Empty = no
	// egress enforcement for this sandbox.
	EgressToken string
	// EnableDocker grants this sandbox a real Docker daemon (a Docker-in-Docker
	// sidecar, not the host socket — the sandbox never gets host-level
	// container control). Opt-in per agent via ACCESS.md (system: dev, scope
	// incl. "docker"); the caller resolves that and sets this field, the
	// provider does not know about ACCESS.md itself.
	EnableDocker bool
}

type Sandbox interface {
	// Stop shuts the compute instance down; the home stays.
	Stop(ctx context.Context) error
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
	// AgentHome returns an agent's home. It need not exist: an agent that was
	// never woken does not have one yet.
	AgentHome(agentID uuid.UUID) (Home, error)
}

// Home describes an agent's persistent home on the host.
type Home struct {
	// Path is the directory that appears as /home/agent inside the sandbox.
	Path string
	// UID/GID is the owner inside the sandbox; -1 = do not set.
	UID, GID int
}

// ErrNoFileAccess: the configured provider knows no reachable home.
var ErrNoFileAccess = errors.New("file access: the sandbox provider has no reachable home")

// AgentFiles opens an agent's home as a file tree (spec/02: the workplace). If
// the provider lacks the FileAccess port, ErrNoFileAccess is returned.
func (o *Orchestrator) AgentFiles(agentID uuid.UUID) (*sandboxfs.FS, error) {
	fa, ok := o.Provider.(FileAccess)
	if !ok {
		return nil, ErrNoFileAccess
	}
	home, err := fa.AgentHome(agentID)
	if err != nil {
		return nil, err
	}
	return sandboxfs.New(home.Path, home.UID, home.GID)
}
