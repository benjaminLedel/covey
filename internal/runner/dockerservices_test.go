package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/sandbox"
)

// fakeDocker writes every invocation to a file and answers the way the real one
// would on a host that has nothing yet: `network inspect` fails (the segment
// does not exist), everything else succeeds. `rules` may fail specific calls.
func fakeDocker(t *testing.T, dir string, failOn string) (bin, argsFile string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	argsFile = filepath.Join(dir, "args")
	bin = filepath.Join(dir, "docker")
	fail := ""
	if failOn != "" {
		fail = "case \"$*\" in *" + failOn + "*) echo 'Unable to find image' >&2; exit 1;; esac\n"
	}
	script := "#!/bin/sh\n" +
		"printf '%s\\n' \"$*\" >> " + argsFile + "\n" +
		fail +
		// A host that does not have the segment yet — otherwise `network
		// create` would never be reached and the test would prove nothing.
		"case \"$1$2\" in networkinspect) exit 1;; esac\n" +
		"echo containerid\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argsFile
}

func TestDockerStartsServicesBesideTheSandbox(t *testing.T) {
	dir := t.TempDir()
	fake, argsFile := fakeDocker(t, dir, "")

	agentID := uuid.New()
	p := &Docker{Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake}
	container, _, err := p.Start(context.Background(), StartSandbox{
		AgentID: agentID,
		Services: []sandbox.Service{
			{Name: "db", Image: "postgres:16", Env: map[string]string{"POSTGRES_PASSWORD": "test"}},
			{Name: "cache", Image: "redis:7"},
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
	network := servicesNetworkFor(agentID)

	for _, want := range []string{
		// The segment belongs to the sandbox and carries no way out.
		"network create --internal",
		network,
		// Each service under the name the agent reaches it by.
		"--network-alias db",
		"--network-alias cache",
		"postgres:16",
		"redis:7",
		"POSTGRES_PASSWORD=test",
		// And the sandbox joined to it — after its own run, so that its
		// default route is decided without them.
		"network connect " + network + " " + container,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the docker invocations do not contain %q:\n%s", want, got)
		}
	}

	// The order is the substance here: a daemon that reports ready has to be
	// reporting a complete workplace, so the services run BEFORE the sandbox.
	svc := strings.Index(got, "postgres:16")
	box := strings.Index(got, "covey-sandbox:test")
	if svc < 0 || box < 0 || svc > box {
		t.Errorf("the service did not start before the sandbox (service at %d, sandbox at %d):\n%s", svc, box, got)
	}
}

// A sandbox without services must not create a network, and must not touch
// anything of the kind. That is the normal case, and the one that would notice
// a cost nobody asked for.
func TestDockerWithoutServicesTouchesNoNetwork(t *testing.T) {
	dir := t.TempDir()
	fake, argsFile := fakeDocker(t, dir, "")

	agentID := uuid.New()
	p := &Docker{Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake}
	if _, _, err := p.Start(context.Background(), StartSandbox{AgentID: agentID}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	raw, _ := os.ReadFile(argsFile)
	if got := string(raw); strings.Contains(got, "network create") || strings.Contains(got, "network connect") {
		t.Errorf("a sandbox without services built a network:\n%s", got)
	}
}

// A service whose image cannot be fetched ends the start, and takes what
// already came up with it. Half a set of services is the state in which an
// agent reports the wrong defect.
func TestDockerServiceFailureEndsTheStart(t *testing.T) {
	dir := t.TempDir()
	fake, argsFile := fakeDocker(t, dir, "redis:7")

	agentID := uuid.New()
	p := &Docker{Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake}
	_, _, err := p.Start(context.Background(), StartSandbox{
		AgentID: agentID,
		Services: []sandbox.Service{
			{Name: "db", Image: "postgres:16"},
			{Name: "cache", Image: "redis:7"},
		},
	})
	if err == nil {
		t.Fatal("a service that could not be fetched did not end the start")
	}
	if !strings.Contains(err.Error(), "cache") || !strings.Contains(err.Error(), "redis:7") {
		t.Errorf("the error names neither the service nor its image: %v", err)
	}
	raw, _ := os.ReadFile(argsFile)
	got := string(raw)
	if strings.Contains(got, "covey-sandbox:test") {
		t.Errorf("the sandbox started although its services had not:\n%s", got)
	}
	if !strings.Contains(got, "ps -aq --filter label="+serviceLabel+"="+agentID.String()) {
		t.Errorf("what had already come up was not torn down:\n%s", got)
	}
}

// Stop and the watcher both end the services — the sandbox's life is theirs.
func TestDockerStopRemovesTheServices(t *testing.T) {
	dir := t.TempDir()
	fake, argsFile := fakeDocker(t, dir, "")

	agentID := uuid.New()
	p := &Docker{Image: "covey-sandbox:test", DataDir: dir, DockerBin: fake}
	if err := p.Stop(context.Background(), containerName(agentID.String())); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	raw, _ := os.ReadFile(argsFile)
	got := string(raw)
	if !strings.Contains(got, "label="+serviceLabel+"="+agentID.String()) {
		t.Errorf("Stop did not look for the services:\n%s", got)
	}
	if !strings.Contains(got, "network rm "+servicesNetworkFor(agentID)) {
		t.Errorf("Stop left the segment behind:\n%s", got)
	}
}

func TestAgentIDFromContainer(t *testing.T) {
	agentID := uuid.New()
	got, ok := agentIDFromContainer(containerName(agentID.String()))
	if !ok || got != agentID {
		t.Errorf("agentIDFromContainer round trip: %v, %v", got, ok)
	}
	if _, ok := agentIDFromContainer("some-other-container"); ok {
		t.Error("a foreign container name was read as an agent")
	}
	if _, ok := agentIDFromContainer("covey-sandbox-not-a-uuid"); ok {
		t.Error("a name that is not a UUID was read as an agent")
	}
}
