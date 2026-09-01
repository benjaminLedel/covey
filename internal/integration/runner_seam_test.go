package integration

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/homestore"
	"covey/internal/orchestrator"
	"covey/internal/runner"
	"covey/internal/sandbox"
)

// Every sandbox starts through the runner protocol — including on a single
// machine, where the runner sits in the control plane's own process. These
// tests take that path in full: orchestrator → pool → protocol → node →
// docker (faked). What they are guarding is the reason the seam exists at all:
// if the in-process case took a shortcut past the protocol, the remote path
// would be exercised only by whoever operates two machines, and would rot
// everywhere else.

// fakeDocker writes a docker stand-in. It never starts a daemon — which is the
// point here: the sandbox comes up and nothing connects, exactly the situation
// a crash produces. `wait` blocks until the file `exit` appears and then
// reports that code, so the test decides when the container dies — or until
// the temp directory is gone, so that no test can leave it spinning.
func fakeDocker(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s ' "$@" >> ` + filepath.Join(dir, "args") + `
printf '\n' >> ` + filepath.Join(dir, "args") + `
if [ "$1" = "wait" ]; then
  while [ ! -f ` + filepath.Join(dir, "exit") + ` ]; do
    # The last line of defence against a stray sandbox watcher. Go cannot kill
    # this child if the test binary itself is killed, and a wait that then
    # polls on at 20 Hz forever is a fork bomb nothing on the host can trace
    # back to a test. The temp directory goes with the test, so its absence is
    # the one signal that always arrives.
    # Quoted, and exiting NON-zero: an unquoted path with a space makes
    # the test fail, and an exit 0 would then be read as a container that
    # ended by itself - a reported death for a sandbox that is running fine.
    [ -d '` + dir + `' ] || exit 1
    sleep 0.05
  done
  cat ` + filepath.Join(dir, "exit") + `
  exit 0
fi
echo ok
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A dead sandbox is a reported fact, not something inferred from a timeout.
// The ReadyTimeout here is deliberately far longer than the test's patience:
// if the report did not arrive, the test would fail on its own deadline — and
// that is precisely the minutes an operator used to spend waiting, only to be
// told that the daemon had not connected.
func TestDeadSandboxEndsTheWakeInsteadOfTimingOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	dockerBin := fakeDocker(t, dir)

	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		// Long on purpose: the death has to end the wake, not the clock.
		readyTimeout: 90 * time.Second,
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
			pool.StartTimeout = 20 * time.Second
			return pool
		},
		afterOrch: func(o *orchestrator.Orchestrator) { pool.SandboxDied = o.SandboxDied },
	})
	ctx := context.Background()

	// The built-in runner of this organisation — one process, one connection,
	// the same protocol a foreign host would speak.
	runnerID := uuid.New()
	node := runner.NewNode(runnerID, s.orgID, &runner.Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: dockerBin,
	}, slog.Default())
	t.Cleanup(node.Close)
	if err := pool.AttachLocal(ctx, node); err != nil {
		t.Fatalf("built-in runner: %v", err)
	}

	agent := s.newSupportAgent("dying-sandbox")
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Ein Lauf, der nie beginnt", "", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// Wait until the sandbox is up (the fake docker has been called), then let
	// the container die the way an OOM kill does.
	waitFor(t, "sandbox started", 20*time.Second, func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, "args"))
		return err == nil && strings.Contains(string(raw), "covey-sandbox-"+agent.ID.String())
	})
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte("137\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Well inside the ReadyTimeout: the report is what ends this, not the clock.
	var found string
	waitFor(t, "the run ends after the death of the sandbox", 30*time.Second, func() bool {
		events, err := s.obs.Events(ctx, agent.ID, nil, 0, 500)
		if err != nil {
			return false
		}
		for _, e := range events {
			if payload := string(e.Payload); strings.Contains(payload, "wake_failed") {
				found = payload
				return true
			}
		}
		return false
	})
	if !strings.Contains(found, "did not survive") {
		t.Fatalf("the recording has to say that the sandbox died: %s", found)
	}
	if s.taskState(task.ID) == backlog.StateDone {
		t.Errorf("a task whose sandbox died must not count as done")
	}
	// The reason has to travel: without it the entry says only that something
	// went wrong, and the search starts at the runtime instead of at memory.
	if !strings.Contains(found, "137") {
		t.Errorf("the reason belongs in the message, got %q", found)
	}
}

// The home store is what makes a home replaceable — and only then is a runner
// switch not data loss. This drives it through the whole seam: orchestrator →
// pool → protocol → node → store, with the sync at the real falling-asleep and
// the restore on the next wake.
//
// What it is really guarding is the promise the construction makes: the 48 MB
// an agent produced and that exist nowhere else survive losing the working
// copy, without anyone having written down that they exist.
func TestHomeSurvivesTheLossOfItsRunnerWorkingCopy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	dockerBin := fakeDocker(t, dir)
	ctx := context.Background()

	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}

	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
			pool.StartTimeout = 20 * time.Second
			return pool
		},
	})

	agent := s.newSupportAgent("home-store-agent")
	pool.LatestSnapshot = func(ctx context.Context, agentID uuid.UUID) (string, error) {
		snap, err := s.runners.LatestSnapshot(ctx, agentID)
		return snap.ManifestHash, err
	}
	pool.SnapshotTaken = func(ctx context.Context, agentID, runnerID uuid.UUID, res runner.HomeSynced) error {
		_, err := s.runners.RecordSnapshot(ctx, s.orgID, agentID, &runnerID,
			res.ManifestHash, res.TotalSize, res.Blocks, res.BytesUp, "job")
		return err
	}

	// The organisation's built-in runner — the row a snapshot points at when it
	// records where the working copy sat.
	rn, err := s.runners.EnsureBuiltin(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	runnerID := rn.ID
	homes := filepath.Join(dir, "data")
	node := runner.NewNode(runnerID, s.orgID, &runner.Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: homes, DockerBin: dockerBin,
	}, slog.Default())
	node.Blobs = blobs
	t.Cleanup(node.Close)
	if err := pool.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	// The agent has worked: interim results scattered through the home, exactly
	// the way an agent puts them down — not in a folder provided for it.
	home := filepath.Join(homes, "homes", agent.ID.String())
	if err := os.MkdirAll(filepath.Join(home, "fix223"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, content := range map[string]string{
		"useSevenAssistant.ts":     "// 95 KB extrahierter Code",
		"fix223/analyse.md":        "Was hier schiefging",
		".claude/transkript.jsonl": `{"typ":"nachricht"}`,
	} {
		p := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Start and stop through the protocol — the stop is the real falling-asleep,
	// and that is what triggers the sync.
	sb, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sb.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	snap, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ManifestHash == "" {
		t.Fatal("falling asleep has to produce a snapshot")
	}
	if snap.RunnerID == nil || *snap.RunnerID != runnerID {
		t.Errorf("the snapshot has to know where the working copy sat: %+v", snap.RunnerID)
	}
	// The preference of the scheduler follows from it — a hint, not a promise.
	reloaded, err := s.registry.Get(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = reloaded

	// The runner loses its working copy: a cleared disk, a new machine.
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}

	// Next wake: the home comes back out of the store before the sandbox starts.
	if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID}); err != nil {
		t.Fatalf("second start: %v", err)
	}
	for path, want := range map[string]string{
		"useSevenAssistant.ts":     "// 95 KB extrahierter Code",
		"fix223/analyse.md":        "Was hier schiefging",
		".claude/transkript.jsonl": `{"typ":"nachricht"}`,
	} {
		got, err := os.ReadFile(filepath.Join(home, path))
		if err != nil {
			t.Errorf("%s did not come back: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s came back changed: %q", path, got)
		}
	}
}

// The other direction of the same seam: the STORE loses part of a home while
// the working copy still holds it. The agent has to wake anyway.
//
// This is the incident that closed #137/#138, in the shape it actually took on
// covey.work. A sweep deleted the manifest chunks of a snapshot minutes after
// it was written; from then on every wake ended with "manifest chunk …: block
// not found". The refusal was deliberate — never work on a half state — but it
// was wrong here: the home lay complete on the runner, unchanged since the sync
// that wrote that very snapshot. The agent stayed down for six and a half hours
// beside its own intact files.
//
// So a start is allowed when the copy PROVES it is the state that was asked
// for: SyncedHash is written after a successful sync and names it. Anything
// else still refuses.
func TestWakeUsesTheWorkingCopyWhenTheStoreLostTheState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	dockerBin := fakeDocker(t, dir)
	ctx := context.Background()

	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
			pool.StartTimeout = 20 * time.Second
			return pool
		},
	})
	agent := s.newSupportAgent("verlorener-block")
	pool.LatestSnapshot = func(ctx context.Context, agentID uuid.UUID) (string, error) {
		snap, err := s.runners.LatestSnapshot(ctx, agentID)
		return snap.ManifestHash, err
	}
	pool.SnapshotTaken = func(ctx context.Context, agentID, runnerID uuid.UUID, res runner.HomeSynced) error {
		_, err := s.runners.RecordSnapshot(ctx, s.orgID, agentID, &runnerID,
			res.ManifestHash, res.TotalSize, res.Blocks, res.BytesUp, "job")
		return err
	}
	rn, err := s.runners.EnsureBuiltin(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	homes := filepath.Join(dir, "data")
	node := runner.NewNode(rn.ID, s.orgID, &runner.Docker{
		RunnerID: rn.ID, Image: "covey-sandbox:test", DataDir: homes, DockerBin: dockerBin,
	}, slog.Default())
	node.Blobs = blobs
	t.Cleanup(node.Close)
	if err := pool.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	home := filepath.Join(homes, "homes", agent.ID.String())
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "arbeit.md"), []byte("was der Agent zuletzt tat"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A run, and the sync at the end of it: now the copy and the snapshot are
	// the same state, and the copy says so.
	sb, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sb.Stop(ctx); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	snap, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil || snap.ManifestHash == "" {
		t.Fatalf("no snapshot after the sync: %v", err)
	}
	if stand := homestore.SyncedHash(home); stand != snap.ManifestHash {
		t.Fatalf("the working copy does not name the state it was synced to: %q vs %q", stand, snap.ManifestHash)
	}

	// The store loses the manifest — the sweep's mistake, in one line.
	if err := blobs.Delete(ctx, s.orgID, snap.ManifestHash); err != nil {
		t.Fatal(err)
	}
	if _, err := homestore.Load(ctx, blobs, s.orgID, snap.ManifestHash); err == nil {
		t.Fatal("the state has to be unreadable for this test to be the case it claims")
	}

	// The wake works anyway, out of the copy on this host.
	if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID}); err != nil {
		t.Fatalf("the agent stayed down beside its own intact home: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(home, "arbeit.md")); err != nil || string(b) != "was der Agent zuletzt tat" {
		t.Errorf("the working copy did not survive the start: %v", err)
	}
}
