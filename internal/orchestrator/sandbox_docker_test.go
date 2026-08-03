package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestRewriteLoopbackForDocker(t *testing.T) {
	cases := map[string]string{
		"ws://localhost:8494/api/daemon/ws": "ws://host.docker.internal:8494/api/daemon/ws",
		"ws://127.0.0.1:8494/api/daemon/ws": "ws://host.docker.internal:8494/api/daemon/ws",
		"ws://[::1]:8494/api/daemon/ws":     "ws://host.docker.internal:8494/api/daemon/ws",
		"ws://localhost/api/daemon/ws":      "ws://host.docker.internal/api/daemon/ws",
		// Echte Hostnamen bleiben unangetastet. Das ist die richtige Wahl —
		// raten kann die Funktion hier nichts —, aber es ist auch die Stelle,
		// an der eine falsch gesetzte COVEY_PUBLIC_URL ungebremst durchgeht:
		// Die Sandbox wählt dann über das offene Netz zurück. Beim Start warnt
		// deshalb config.DataPlaneWarnings davor.
		"wss://covey.example.com/api/daemon/ws": "wss://covey.example.com/api/daemon/ws",
		"ws://10.0.0.5:8494/api/daemon/ws":      "ws://10.0.0.5:8494/api/daemon/ws",
	}
	for in, want := range cases {
		if got := rewriteLoopbackForDocker(in); got != want {
			t.Errorf("rewriteLoopbackForDocker(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDockerProviderStart prüft die Kommandozeile, die der Provider baut —
// gegen ein Fake-docker-Binary, ohne echtes Docker.
func TestDockerProviderStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Fake-Binary ist ein Shell-Skript")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + argsFile + "\necho containerid\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	agentID := uuid.New()
	p := &DockerProvider{Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake}
	sb, err := p.Start(context.Background(), SandboxSpec{
		AgentID: agentID,
		Env: map[string]string{
			"COVEY_WS_URL":       "ws://localhost:8494/api/daemon/ws",
			"COVEY_DAEMON_TOKEN": "tok",
			"COVEY_AGENT_ID":     agentID.String(),
		},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"covey-sandbox-" + agentID.String(),
		"COVEY_WS_URL=ws://host.docker.internal:8494/api/daemon/ws",
		"COVEY_DAEMON_TOKEN=tok",
		"HOME=" + sandboxHome,
		"host.docker.internal:host-gateway",
		"covey-sandbox:test",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("docker-Aufruf enthält %q nicht:\n%s", want, got)
		}
	}
	// Das Agenten-Home muss auf dem Host existieren und gemountet werden.
	home := filepath.Join(dir, "homes", agentID.String())
	if _, err := os.Stat(home); err != nil {
		t.Errorf("Agenten-Home %s wurde nicht angelegt: %v", home, err)
	}
	if !strings.Contains(got, home+":"+sandboxHome) {
		t.Errorf("Volume-Mount %s:%s fehlt:\n%s", home, sandboxHome, got)
	}

	if err := sb.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
