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
		// Real hostnames stay untouched. That is the right call — the function
		// cannot guess here — but it is also the spot where a misconfigured
		// COVEY_PUBLIC_URL passes through unchecked: the sandbox then dials back
		// over the open network. config.DataPlaneWarnings warns about that at
		// startup for exactly this reason.
		"wss://covey.example.com/api/daemon/ws": "wss://covey.example.com/api/daemon/ws",
		"ws://10.0.0.5:8494/api/daemon/ws":      "ws://10.0.0.5:8494/api/daemon/ws",
	}
	for in, want := range cases {
		if got := rewriteLoopbackForDocker(in); got != want {
			t.Errorf("rewriteLoopbackForDocker(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDockerProviderStart checks the command line the provider builds —
// against a fake docker binary, without real Docker.
func TestDockerProviderStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
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
			t.Errorf("docker invocation does not contain %q:\n%s", want, got)
		}
	}
	// The agent home has to exist on the host and be mounted.
	home := filepath.Join(dir, "homes", agentID.String())
	if _, err := os.Stat(home); err != nil {
		t.Errorf("agent home %s was not created: %v", home, err)
	}
	if !strings.Contains(got, home+":"+sandboxHome) {
		t.Errorf("volume mount %s:%s is missing:\n%s", home, sandboxHome, got)
	}

	if err := sb.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}
