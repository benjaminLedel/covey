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
	info, err := os.Stat(home)
	if err != nil {
		t.Errorf("agent home %s was not created: %v", home, err)
	}
	if !strings.Contains(got, home+":"+sandboxHome) {
		t.Errorf("volume mount %s:%s is missing:\n%s", home, sandboxHome, got)
	}
	// Regression: the chown to sandboxUID is best-effort (only succeeds as
	// root) and was observed to silently not take effect on Docker Desktop for
	// Mac — the bind mount kept the caller's own uid. A 0o700 directory then
	// leaves the sandbox user with no traverse permission on its own home; Go's
	// os/exec chdir()s into it when starting the runtime (cmd.Dir), and that
	// EACCES surfaces as "fork/exec <runtime>: permission denied" — reading
	// like the runtime binary itself is broken, not like a directory the agent
	// cannot enter. The mode has to grant "other" traverse (x) regardless of
	// which uid ends up owning it.
	if info != nil && info.Mode().Perm()&0o001 == 0 {
		t.Errorf("agent home %s is not traversable by \"other\" (mode %o) — "+
			"a failed chown to the sandbox uid would then lock that user out of its own home",
			home, info.Mode().Perm())
	}

	if err := sb.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestDockerProviderStartWithDocker checks the Docker-in-Docker wiring for an
// agent with EnableDocker set: a sidecar comes up, the sandbox is attached to
// its private network and told where to find it, and Stop tears both down.
func TestDockerProviderStartWithDocker(t *testing.T) {
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
		AgentID:      agentID,
		EnableDocker: true,
		Env:          map[string]string{"COVEY_AGENT_ID": agentID.String()},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	dindContainer := dindContainerName(agentID.String())
	dindNet := dindNetworkName(agentID.String())
	for _, want := range []string{
		"--privileged",
		dindContainer,
		dindNet,
		"--network-alias\ndind",
		DindImage,
		"DOCKER_HOST=tcp://dind:2375",
		"network\nconnect\n" + dindNet,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("docker invocations do not contain %q:\n%s", want, got)
		}
	}
	// The sidecar must get the SAME home mount as the sandbox — otherwise a
	// bind mount in a compose file the agent runs resolves against the
	// sidecar's own, empty filesystem instead of the checkout the agent
	// actually put there (the real failure this closes: a compose service
	// couldn't find its bind-mounted script). Both the sandbox's own `run`
	// and the sidecar's `run` must carry it, so the mount line has to appear
	// twice, not once.
	homeMount := "-v\n" + filepath.Join(dir, "homes", agentID.String()) + ":" + sandboxHome
	if n := strings.Count(got, homeMount); n != 2 {
		t.Errorf("home mount %q must appear twice (sandbox + sidecar), appeared %d times:\n%s", homeMount, n, got)
	}

	if err := os.WriteFile(argsFile, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sb.Stop(context.Background()); err != nil {
		t.Errorf("Stop: %v", err)
	}
	stopArgs, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{dindContainer, dindNet} {
		if !strings.Contains(string(stopArgs), want) {
			t.Errorf("Stop did not tear down %q:\n%s", want, string(stopArgs))
		}
	}
}

// TestDockerProviderStartWithDockerHardIsolation proves the two network
// boundaries around the unauthenticated privileged daemon: its API network is
// internal and per-agent, and its proxy route is also per-agent. The daemon
// must never join the shared sandbox egress network where another agent could
// reach port 2375.
func TestDockerProviderStartWithDockerHardIsolation(t *testing.T) {
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
	p := &DockerProvider{
		Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake,
		EgressIsolation: "network", EgressProxyImage: "covey-egress:test",
	}
	sb, err := p.Start(context.Background(), SandboxSpec{AgentID: agentID, EnableDocker: true})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = sb.Stop(context.Background()) })

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	dindContainer := dindContainerName(agentID.String())
	dindNet := dindNetworkName(agentID.String())
	proxyNet := dindProxyNetworkName(agentID.String())
	for _, want := range []string{
		"network\ncreate\n--internal\n" + dindNet,
		"network\ncreate\n--internal\n" + proxyNet,
		"network\nconnect\n--alias\n" + egressProxyAlias + "\n" + proxyNet + "\n" + egressProxyName,
		"network\nconnect\n" + proxyNet + "\n" + dindContainer,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("hard-isolation wiring does not contain %q:\n%s", want, got)
		}
	}
	if forbidden := "network\nconnect\n" + egressNetwork + "\n" + dindContainer; strings.Contains(got, forbidden) {
		t.Fatalf("privileged daemon joined the shared sandbox network:\n%s", got)
	}
}

// TestDockerProviderReconcileDindOrphans protects startup cleanup after a
// control-plane crash, when normal Sandbox.Stop never ran.
func TestDockerProviderReconcileDindOrphans(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s\n' "$@" >> ` + argsFile + `
if [ "$1" = "ps" ]; then
  printf '%s\n' covey-dind-agent-a covey-dind-agent-b
fi
if [ "$1" = "network" ] && [ "$2" = "ls" ]; then
  printf '%s\n' covey-dind-net-agent-a covey-dind-proxy-net-agent-a
fi
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	p := &DockerProvider{DockerBin: fake}
	if err := p.Reconcile(context.Background()); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		"rm\n-f\ncovey-dind-agent-a",
		"rm\n-f\ncovey-dind-agent-b",
		"network\nrm\ncovey-dind-net-agent-a",
		"network\nrm\ncovey-dind-proxy-net-agent-a",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("startup cleanup does not contain %q:\n%s", want, got)
		}
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
