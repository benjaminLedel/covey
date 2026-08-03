package orchestrator

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"covey/internal/sandboxfs"
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

// FileAccess ist der optionale Zweit-Port eines SandboxProviders: der Weg zum
// persistenten Home eines Agenten. Er hängt am Provider, weil nur der weiß, wo
// das Home liegt — beim docker-Provider ein Verzeichnis auf dem Host, bei einem
// künftigen Remote-Provider etwas anderes. Wer ihn nicht implementiert, hat
// keinen Dateibrowser; das Feature schaltet sich dann ab, statt zu raten.
//
// Der Zugriff geht bewusst *nicht* durch das Daemon-Protokoll: Das Home ist
// auch dann da, wenn die Sandbox schläft — und schlafend ist der Normalzustand.
type FileAccess interface {
	// AgentHome liefert das Home eines Agenten. Es muss nicht existieren: ein
	// nie geweckter Agent hat noch keines.
	AgentHome(agentID uuid.UUID) (Home, error)
}

// Home beschreibt das persistente Home eines Agenten auf dem Host.
type Home struct {
	// Path ist das Verzeichnis, das in der Sandbox als /home/agent erscheint.
	Path string
	// UID/GID ist der Besitzer innerhalb der Sandbox; -1 = nicht setzen.
	UID, GID int
}

// ErrNoFileAccess: der konfigurierte Provider kennt kein erreichbares Home.
var ErrNoFileAccess = errors.New("dateizugriff: der sandbox-provider hat kein erreichbares home")

// AgentFiles öffnet das Home eines Agenten als Dateibaum (spec/02: der
// Arbeitsplatz). Fehlt dem Provider der FileAccess-Port, kommt ErrNoFileAccess.
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
