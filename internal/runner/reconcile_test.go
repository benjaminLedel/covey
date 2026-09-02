package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/orchestrator"
)

// leftRunning tells the fake docker that a sandbox container for this agent is
// up — what a runner that restarted finds on its host.
func leftRunning(t *testing.T, dir string, agentID uuid.UUID) {
	t.Helper()
	line := containerName(agentID.String()) + "\t" + agentID.String() + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A runner that starts finds the containers its predecessor left and takes
// them under watch: they count, they are named in the handshake, and their
// death is still reported (#155).
func TestARestartedRunnerAdoptsWhatItLeftRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	leftRunning(t, dir, agentID)
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	t.Cleanup(node.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	control, nodeEnd := NewInProc()
	go func() { _ = node.Run(ctx, nodeEnd) }()
	msg, err := control.Receive(ctx)
	if err != nil || msg.Type != TypeRegistered {
		t.Fatal(err)
	}
	reg, _ := decode[Registered](msg)
	if len(reg.Running) != 1 || reg.Running[0] != agentID {
		t.Fatalf("the handshake has to name what runs here, got %v", reg.Running)
	}
	if node.capacity().Sandboxes != 1 {
		t.Fatal("an adopted sandbox has to count")
	}
	// And it is watched: its death is a report, not a guess.
	if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ev, _ := decode[SandboxExited](warteAufTyp(t, control, TypeSandboxExited))
	if ev.AgentID != agentID {
		t.Fatalf("the wrong death was reported: %+v", ev)
	}
}

// What a host reports at connect is reconciled: a sandbox the pool placed there
// is adopted, one it did not place is stopped and its home secured — and a
// start for that agent waits until the stray is gone (#155).
func TestThePoolStopsWhatItDidNotPlaceAndAdoptsWhatItDid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID := uuid.New(), uuid.New()
	stray, placed := uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}

	p := NewPool(quietLog())
	p.Profiles = map[string]string{"base": "covey-sandbox:test"}
	p.StartTimeout = 10 * time.Second
	synced := make(chan HomeSynced, 4)
	p.SnapshotTaken = func(_ context.Context, _, _ uuid.UUID, res HomeSynced) error {
		synced <- res
		return nil
	}
	p.mu.Lock()
	p.placed[placed] = runnerID
	p.mu.Unlock()

	// The host holds both; the stray's home carries a run.
	lines := containerName(stray.String()) + "\t" + stray.String() + "\n" +
		containerName(placed.String()) + "\t" + placed.String() + "\n"
	if err := os.WriteFile(filepath.Join(dir, "ps"), []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	node.Blobs = blobs
	t.Cleanup(node.Close)
	home, _, _ := node.Docker.AgentHome(stray)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "work.md"), []byte("nobody else has this"), 0o644); err != nil {
		t.Fatal(err)
	}
	attachRemote(t, ctx, p, node)

	select {
	case res := <-synced:
		if res.AgentID != stray || res.Reason != "stray" {
			t.Fatalf("the stray's home has to be secured, got %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the stray was not stopped and secured")
	}
	c := p.connFor(orgID, runnerID)
	c.mu.Lock()
	count := c.sandboxes
	c.mu.Unlock()
	if count != 1 {
		t.Fatalf("the placed sandbox has to be adopted and counted, got %d", count)
	}
	args, _ := os.ReadFile(filepath.Join(dir, "args"))
	if !strings.Contains(string(args), "stop -t 5 "+containerName(stray.String())) {
		t.Fatal("the stray was not stopped")
	}
	if strings.Contains(string(args), "stop -t 5 "+containerName(placed.String())) {
		t.Fatal("the placed sandbox must not be stopped")
	}

	// A start for the stray's agent afterwards works, on a clean host.
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: stray, OrgID: orgID}); err != nil {
		t.Fatalf("Start after the stray was cleared: %v", err)
	}
}
