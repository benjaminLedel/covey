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
	if len(problems) != 1 || !strings.Contains(problems[0], "make sandbox-image") {
		t.Fatalf("a missing image has to name how to build it: %v", problems)
	}

	// Since the image hangs off the agent, the check follows the agents and no
	// longer the config: it asks about the images that are actually in use, and
	// names how many agents are waiting on the missing one. Asking about every
	// configured profile instead would warn every fresh installation about a
	// dev image nobody wants.
	p = &DockerProvider{
		Image:     "covey-sandbox:test",
		Profiles:  map[string]string{"base": "covey-sandbox:test", "dev": "covey-sandbox-dev:test"},
		DockerBin: fakeDocker(t, "image"),
		AgentImages: func(context.Context) (map[string]int, error) {
			return map[string]int{"dev": 3}, nil // nobody on base
		},
	}
	problems = p.Check(context.Background())
	if len(problems) != 1 {
		t.Fatalf("only the image in use may be reported: %v", problems)
	}
	if !strings.Contains(problems[0], "covey-sandbox-dev:test") || !strings.Contains(problems[0], "3 agent(s)") {
		t.Errorf("the message has to name the image and how many wait on it: %q", problems[0])
	}
	if !strings.Contains(problems[0], "make sandbox-image-dev") {
		t.Errorf("the build hint has to name the right target, got %q", problems[0])
	}

	// Everything in place: nothing to report.
	p = &DockerProvider{Image: "covey-sandbox:test", DockerBin: fakeDocker(t, "nothing")}
	if problems = p.Check(context.Background()); len(problems) != 0 {
		t.Fatalf("a working data plane has to stay silent: %v", problems)
	}
}

// The image hangs off the agent (spec/16). One column carries both a profile
// name and an image of an organisation's own, so the resolution is the whole
// rule — and if it is wrong, an agent silently works in a foreign workplace:
// either without the toolchain it needs, or with one it should not have.
func TestImageForResolvesProfilesAndOwnImages(t *testing.T) {
	p := &DockerProvider{
		Image: "covey-sandbox:latest",
		Profiles: map[string]string{
			"base": "covey-sandbox:latest",
			"dev":  "covey-sandbox-dev:latest",
		},
	}
	faelle := []struct {
		name, want, expected string
	}{
		{"nothing named → the instance default", "", "covey-sandbox:latest"},
		{"profile base", "base", "covey-sandbox:latest"},
		{"profile dev", "dev", "covey-sandbox-dev:latest"},
		// The third row of the profile table: an organisation builds its own
		// image and names it. Without this the field would need a second one
		// beside it that says how to read the first.
		{"own image", "registry.example.com/team/sandbox:2026-08", "registry.example.com/team/sandbox:2026-08"},
		// Whitespace comes from a text field in the interface, not from a
		// malicious client — and a trailing blank must not produce an image
		// name nobody can find.
		{"trimmed", "  dev  ", "covey-sandbox-dev:latest"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			if got := p.imageFor(f.want); got != f.expected {
				t.Errorf("imageFor(%q) = %q, expected %q", f.want, got, f.expected)
			}
		})
	}

	// A profile without a configured image must not swallow the value: an
	// instance without COVEY_SANDBOX_IMAGE_DEV would otherwise start every dev
	// agent in an empty image name.
	leer := &DockerProvider{Image: "covey-sandbox:latest", Profiles: map[string]string{"dev": ""}}
	if got := leer.imageFor("dev"); got != "dev" {
		t.Errorf("an unconfigured profile stays the literal value, got %q", got)
	}
}
