package orchestrator

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// SandboxProvider ist der Port zur Data Plane. Die Sandbox ist bewusst dumm
// und ersetzbar (spec/01): persistentes Home, ephemere Compute. Der MVP
// bringt den local-Provider (Subprozess) mit; E2B/Beam kommen über dasselbe
// Interface, ohne den Orchestrator zu ändern.
type SandboxProvider interface {
	// Start weckt die Sandbox: Compute hochfahren, Home mounten, Daemon starten.
	Start(ctx context.Context, spec SandboxSpec) (Sandbox, error)
}

type SandboxSpec struct {
	AgentID uuid.UUID
	HomeDir string
	Env     map[string]string // COVEY_WS_URL, COVEY_DAEMON_TOKEN, …
}

type Sandbox interface {
	// Stop fährt die Compute-Instanz herunter; das Home bleibt bestehen.
	Stop(ctx context.Context) error
}

// LocalProvider startet coveyd als Subprozess — die einfachste ehrliche
// Data Plane für Entwicklung und Tests. Isolation ist hier Prozess-Ebene;
// echte Isolation (MicroVM/Container) liefert ein späterer Provider.
type LocalProvider struct {
	CoveydPath string
	DataDir    string
}

func (p *LocalProvider) Start(ctx context.Context, spec SandboxSpec) (Sandbox, error) {
	home := spec.HomeDir
	if home == "" {
		home = filepath.Join(p.DataDir, "homes", spec.AgentID.String())
	}
	// Absolut auflösen: HOME/cwd der Sandbox müssen absolut sein, sonst löst die
	// Runtime ~/.claude.json relativ zum cwd auf (der selbst das Home ist).
	if abs, err := filepath.Abs(home); err == nil {
		home = abs
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, err
	}
	cmd := exec.Command(p.CoveydPath)
	cmd.Dir = home
	// HOME ist das Agenten-Home, nicht das des Host-Users: sonst liest eine
	// Claude-Code-Sandbox die persönliche Anmeldung aus ~/.claude.json und
	// kollidiert mit dem gebrokerten Key ("Invalid API key · Fix external API
	// key"). Geerbte Anthropic-Credentials werden herausgefiltert, damit
	// ausschließlich das zur Laufzeit gebrokerte Credential zählt (spec/04).
	cmd.Env = append(sandboxBaseEnv(home), "COVEY_HOME="+home)
	for k, v := range spec.Env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("coveyd starten: %w", err)
	}
	sb := &localSandbox{cmd: cmd, done: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(sb.done)
	}()
	return sb, nil
}

// sandboxBaseEnv leitet die Prozess-Umgebung vom Host ab, ersetzt aber HOME
// durch das Agenten-Home und entfernt geerbte Anthropic-Credentials. So bleibt
// die Runtime-Auth allein Sache des Brokers, nicht der Host-Umgebung.
func sandboxBaseEnv(home string) []string {
	drop := map[string]bool{
		"HOME":                    true, // wird durch das Agenten-Home ersetzt
		"ANTHROPIC_API_KEY":       true,
		"ANTHROPIC_AUTH_TOKEN":    true,
		"CLAUDE_CODE_OAUTH_TOKEN": true,
	}
	out := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if drop[name] {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "HOME="+home)
}

type localSandbox struct {
	cmd  *exec.Cmd
	done chan struct{}
}

func (s *localSandbox) Stop(ctx context.Context) error {
	select {
	case <-s.done:
		return nil // schon beendet
	default:
	}
	// Erst freundlich (SIGTERM = graceful shutdown), dann hart.
	_ = s.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-s.done:
		return nil
	case <-time.After(5 * time.Second):
	case <-ctx.Done():
	}
	_ = s.cmd.Process.Kill()
	<-s.done
	return nil
}
