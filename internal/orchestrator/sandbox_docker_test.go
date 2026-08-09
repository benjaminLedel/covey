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

// TestDockerProviderCheck: the self-check has to name the two ways a fresh
// installation fails — and stay silent when nothing is in the way. Both
// answers matter equally: a check that cries wolf gets ignored, and one that
// says nothing lets the first wake do the explaining.
func TestDockerProviderCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	// fakeDocker writes a script that fails for exactly the given first
	// argument ("version", "image") and succeeds otherwise.
	fakeDocker := func(t *testing.T, failOn string) string {
		t.Helper()
		path := filepath.Join(t.TempDir(), "docker")
		script := "#!/bin/sh\nif [ \"$1\" = \"" + failOn + "\" ]; then\n" +
			"  echo 'Cannot connect to the Docker daemon' >&2\n  exit 1\nfi\necho ok\n"
		if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		return path
	}

	// No daemon: everything else is beside the point, so only that is said.
	p := &DockerProvider{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "version")}
	problems := p.Check(context.Background())
	if len(problems) != 1 || !strings.Contains(problems[0], "docker.sock") {
		t.Fatalf("a missing daemon has to name the socket: %v", problems)
	}

	// Daemon there, image missing: the message has to carry the build command,
	// otherwise the reader is left looking for it.
	p = &DockerProvider{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "image")}
	problems = p.Check(context.Background())
	if len(problems) != 1 || !strings.Contains(problems[0], "Dockerfile.sandbox") {
		t.Fatalf("a missing image has to name how to build it: %v", problems)
	}

	// Everything in place: nothing to report.
	p = &DockerProvider{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "nothing")}
	if problems = p.Check(context.Background()); len(problems) != 0 {
		t.Fatalf("a working data plane has to stay silent: %v", problems)
	}
}
