package orchestrator

import (
	"context"

	"github.com/google/uuid"
)

// SandboxProvider ist der Port zur Data Plane. Die Sandbox ist bewusst dumm
// und ersetzbar (spec/01): persistentes Home, ephemere Compute. Ausgeliefert
// ist der docker-Provider (sandbox_docker.go, echte Container-Isolation);
// E2B/Beam kommen über dasselbe Interface, ohne den Orchestrator zu ändern.
type SandboxProvider interface {
	// Start weckt die Sandbox: Compute hochfahren, Home mounten, Daemon starten.
	Start(ctx context.Context, spec SandboxSpec) (Sandbox, error)
}

type SandboxSpec struct {
	AgentID uuid.UUID
	HomeDir string
	Env     map[string]string // COVEY_WS_URL, COVEY_DAEMON_TOKEN, …
	// EgressToken ist das per-Sandbox-Token, mit dem sich die Sandbox am
	// Egress-Proxy als dieser Agent ausweist (Proxy-Authorization). Leer =
	// kein Egress-Enforcement für diese Sandbox.
	EgressToken string
}

type Sandbox interface {
	// Stop fährt die Compute-Instanz herunter; das Home bleibt bestehen.
	Stop(ctx context.Context) error
}
