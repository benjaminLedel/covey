package runner

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

// TestDockerStart checks the command line the provider builds —
// against a fake docker binary, without real Docker.
func TestDockerStart(t *testing.T) {
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
	p := &Docker{Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake}
	container, err := p.Start(context.Background(), StartSandbox{
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

	if err := p.Stop(context.Background(), container); err != nil {
		t.Errorf("Stop: %v", err)
	}
}

// TestDockerCheck: the self-check has to name the two ways a fresh
// installation fails — and stay silent when nothing is in the way. Both
// answers matter equally: a check that cries wolf gets ignored, and one that
// says nothing lets the first wake do the explaining.
func TestDockerCheck(t *testing.T) {
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
	p := &Docker{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "version")}
	problems := p.Check(context.Background(), nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "docker.sock") {
		t.Fatalf("a missing daemon has to name the socket: %v", problems)
	}

	// Daemon there, image missing: the message has to carry the build command,
	// otherwise the reader is left looking for it.
	p = &Docker{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "image")}
	problems = p.Check(context.Background(), nil)
	if len(problems) != 1 || !strings.Contains(problems[0], "make sandbox-image") {
		t.Fatalf("a missing image has to name how to build it: %v", problems)
	}

	// The images to ask about come from the control plane: it knows the
	// agents, a runner knows only images. Asking about every configured profile
	// instead would warn every fresh installation about a dev image nobody
	// wants.
	p = &Docker{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "image")}
	problems = p.Check(context.Background(), []string{"covey-sandbox:test", "covey-sandbox-dev:test"})
	if len(problems) != 2 {
		t.Fatalf("both named images have to be reported: %v", problems)
	}
	joined := strings.Join(problems, "\n")
	if !strings.Contains(joined, "make sandbox-image-dev") || !strings.Contains(joined, "covey-sandbox-dev:test") {
		t.Errorf("the dev image needs its own hint and its own name:\n%s", joined)
	}

	// Everything in place: nothing to report.
	p = &Docker{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "nothing")}
	if problems = p.Check(context.Background(), nil); len(problems) != 0 {
		t.Fatalf("a working data plane has to stay silent: %v", problems)
	}
}

// The internal network and the proxy container carry the runner they belong to
// in their name. That is not cosmetics: as instance-wide singletons the
// sandboxes of every organisation hung off the same segment, and `--internal`
// cuts the way out, not the way sideways. Two tenants' agents could reach each
// other directly, past every allowlist.
func TestEgressSegmentIsPerRunner(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	if egressNetworkFor(a) == egressNetworkFor(b) {
		t.Error("two runners must not share an internal network")
	}
	if egressProxyNameFor(a) == egressProxyNameFor(b) {
		t.Error("two runners must not share a proxy container")
	}
	// Names have to be usable: docker allows [a-zA-Z0-9][a-zA-Z0-9_.-]*, and a
	// name that is merely long is one nobody can read in a terminal.
	for _, name := range []string{egressNetworkFor(a), egressProxyNameFor(a)} {
		if len(name) > 40 {
			t.Errorf("name too long for a terminal: %q", name)
		}
		if strings.ContainsAny(name, " /:") {
			t.Errorf("name not usable as a docker name: %q", name)
		}
	}
}

// The per-sandbox token identifies the agent to the proxy. If it ever stopped
// travelling, the proxy would answer 407 fail-closed — correct, and completely
// opaque from the agent's side.
func TestEgressProxyURLCarriesThePerSandboxToken(t *testing.T) {
	agent := uuid.New().String()
	got := egressURLWithCreds("http://covey-egress:8888", agent, "geheim")
	if !strings.Contains(got, agent+":geheim@") {
		t.Errorf("the token has to be in the proxy URL: %s", got)
	}
	// Without a token the URL stays untouched — the proxy then answers 407 by
	// itself, which is the right answer and not one to fake here.
	if got := egressURLWithCreds("http://covey-egress:8888", agent, ""); got != "http://covey-egress:8888" {
		t.Errorf("without a token the URL has to stay unchanged: %s", got)
	}
}

// In hard isolation mode the sandbox has no way out but the proxy — and the
// control plane connection runs through it too. If NO_PROXY ever covered the
// control plane there, the daemon link would bypass the allowlist; if the proxy
// variables were missing, nothing would reach the outside at all.
func TestNetworkIsolationRoutesEverythingThroughTheProxy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args")
	fake := filepath.Join(dir, "docker")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" >> " + argsFile + "\necho ok\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	runnerID := uuid.New()
	p := &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake,
		EgressIsolation: "network", EgressProxyImage: "covey-egress:test",
		EgressRunnerToken: "runner-token",
		EgressProxyEnv:    map[string]string{"COVEY_CONTROL_URL": "https://covey.example"},
	}
	agentID := uuid.New()
	if _, err := p.Start(context.Background(), StartSandbox{
		AgentID: agentID, EgressToken: "sandbox-token",
		Env: map[string]string{"COVEY_WS_URL": "wss://covey.example/api/daemon/ws"},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	raw, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	got := string(raw)
	for _, want := range []string{
		egressNetworkFor(runnerID),                // the sandbox's own segment
		egressProxyNameFor(runnerID),              // and its own proxy
		"HTTPS_PROXY=http://" + agentID.String(),  // through the proxy, as this agent
		"COVEY_RUNNER_TOKEN=runner-token",         // the proxy authenticates as the runner
		"COVEY_CONTROL_URL=https://covey.example", // …against the control plane
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing from the docker invocation: %q\n%s", want, got)
		}
	}
	// The control plane must NOT be in NO_PROXY here: in hard mode the daemon
	// link runs through the proxy by CONNECT, and an exception would be a way
	// past the allowlist.
	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "NO_PROXY=") && strings.Contains(line, "host.docker.internal") {
			t.Errorf("in hard mode the control plane must not bypass the proxy: %q", line)
		}
	}
	// And the database URL is gone for good — the proxy is an enforcement
	// point, not a database client.
	if strings.Contains(got, "COVEY_DATABASE_URL") {
		t.Error("the egress proxy must not be given the database URL")
	}
}
