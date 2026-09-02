package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/orchestrator"
)

// The tests in this file are about one thing: a connection ends far more often
// than a runner does, and what a node or a pool holds must not end with it
// (#154), nor may a state the store holds be lost because the message that
// named it went into a closed socket (#153).

// startNodeOn runs the node against a fresh in-process pair and returns the
// control end once the node has registered — the second, third, … connection
// of a runner whose process kept running.
func startNodeOn(t *testing.T, ctx context.Context, node *Node) (Transport, chan error) {
	t.Helper()
	control, nodeEnd := NewInProc()
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx, nodeEnd) }()
	if msg, err := control.Receive(ctx); err != nil || msg.Type != TypeRegistered {
		t.Fatalf("registration: %v (%q)", err, msg.Type)
	}
	return control, done
}

// A sandbox that dies while its runner is between two connections is still a
// death the control plane has to hear of. Before #154 the watcher wrote it to
// the transport of the connection that had started the sandbox — closed by
// then — and the control plane went back to inferring the crash from the
// ReadyTimeout, which is precisely what the watcher exists to replace.
func TestADeathBetweenConnectionsIsReportedOnTheNextOne(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	t.Cleanup(node.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	first, done := startNodeOn(t, ctx, node)
	agentID := uuid.New()
	start, _ := encode(TypeStartSandbox, "1", StartSandbox{AgentID: agentID, OrgID: orgID})
	if err := first.Send(ctx, start); err != nil {
		t.Fatal(err)
	}
	warteAufTyp(t, first, TypeSandboxStarted)

	// The connection goes; the node and its watcher stay.
	_ = first.Close()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run has to end with the transport")
	}
	// The container dies while nobody is listening.
	if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("137\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Give the watcher the moment it needs to see the exit and find no link.
	time.Sleep(500 * time.Millisecond)

	// The next connection hears of it first thing.
	second, _ := startNodeOn(t, ctx, node)
	msg := warteAufTyp(t, second, TypeSandboxExited)
	ev, err := decode[SandboxExited](msg)
	if err != nil || ev.AgentID != agentID {
		t.Fatalf("the death of %s has to arrive on the new connection, got %+v (%v)", agentID, ev, err)
	}
}

// attachRemote takes a node's connection into the pool the way the WebSocket
// handler does, and waits until the pool holds it.
func attachRemote(t *testing.T, ctx context.Context, p *Pool, node *Node) Transport {
	t.Helper()
	control, nodeEnd := NewInProc()
	go func() { _ = node.Run(ctx, nodeEnd) }()
	go func() { _ = p.Attach(ctx, control, false) }()
	warteBis(t, 5*time.Second, func() bool {
		c := p.connFor(node.OrgID, node.RunnerID)
		if c == nil {
			return false
		}
		select {
		case <-c.gone:
			return false
		default:
			return true
		}
	})
	return control
}

// A sandbox handle names its host, not the connection the start went over.
// Before #154 a Stop after a reconnect failed at once with ErrRunnerGone, the
// container stayed up on the host, and — worse — the home sync that belongs to
// the stop never ran: the run's work existed in one working copy on one host
// until the next wake wrote the previous snapshot over it.
func TestAStopAfterAReconnectReachesTheHostAndSyncs(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := NewPool(quietLog())
	p.Profiles = map[string]string{"base": "covey-sandbox:test"}
	p.StartTimeout = 10 * time.Second
	var mu sync.Mutex
	var recorded []HomeSynced
	p.SnapshotTaken = func(_ context.Context, _, _ uuid.UUID, res HomeSynced) error {
		mu.Lock()
		defer mu.Unlock()
		recorded = append(recorded, res)
		return nil
	}

	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	node.Blobs = blobs
	t.Cleanup(node.Close)

	first := attachRemote(t, ctx, p, node)
	sb, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: agentID, OrgID: orgID})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	home, _, _ := node.Docker.AgentHome(agentID)
	if err := os.WriteFile(filepath.Join(home, "work.md"), []byte("the run's work"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The connection drops and comes back — a deploy of the control plane, a
	// proxy timeout. The sandbox is still running.
	_ = first.Close()
	warteBis(t, 5*time.Second, func() bool { return p.connFor(orgID, runnerID) == nil })
	attachRemote(t, ctx, p, node)

	if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sb.Stop(ctx); err != nil {
		t.Fatalf("Stop after a reconnect: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recorded) != 1 || recorded[0].ManifestHash == "" {
		t.Fatalf("the stop has to sync the home over the new connection, recorded %+v", recorded)
	}
	if got := homestore.SyncedHash(home); got != recorded[0].ManifestHash {
		t.Fatalf("the working copy has to name the state it was synced to: %q vs %q", got, recorded[0].ManifestHash)
	}
}

// A home_synced that arrives outside its correlation — an earlier connection
// asked, this one delivers — is recorded like any other. Without it the store
// holds a state the database does not know, and the next wake asks for the
// previous one (#153).
func TestALateSyncResultIsRecorded(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	got := make(chan HomeSynced, 1)
	p.SnapshotTaken = func(_ context.Context, _, _ uuid.UUID, res HomeSynced) error {
		got <- res
		return nil
	}
	nodeEnd, _, _ := registriereFalschenRunner(t, p, orgID)

	agentID := uuid.New()
	late, _ := encode(TypeHomeSynced, uuid.NewString(), HomeSynced{
		AgentID: agentID, ManifestHash: "abcdef0123", TotalSize: 42, Blocks: 1,
	})
	if err := nodeEnd.Send(context.Background(), late); err != nil {
		t.Fatal(err)
	}
	select {
	case res := <-got:
		if res.AgentID != agentID || res.ManifestHash != "abcdef0123" || res.Reason != "recovered" {
			t.Fatalf("recorded the wrong thing: %+v", res)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the late result was not recorded")
	}
}

// syncedHome puts a home into the store and marks the copy as that state.
func syncedHome(t *testing.T, ctx context.Context, blobs homestore.BlobStore, orgID uuid.UUID, home string) string {
	t.Helper()
	res, err := homestore.Sync(ctx, blobs, orgID, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	homestore.MarkSynced(home, res.ManifestHash)
	return res.ManifestHash
}

// A working copy that has been worked in since the snapshot the control plane
// holds was taken is the later state. Materialising the snapshot over it would
// rewrite every file the run changed — prune=false only spares the ones it
// added. So the copy is secured first, the control plane learns the state, and
// the sandbox starts on the copy untouched (#153).
func TestAWorkingCopyNewerThanTheSnapshotIsSecuredNotReverted(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	node.Blobs = blobs
	t.Cleanup(node.Close)

	// The last state the control plane knows: work.md at v1.
	home, _, _ := node.Docker.AgentHome(agentID)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "work.md"), []byte("v1"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := syncedHome(t, ctx, blobs, orgID, home)
	snapshotAt := time.Now()
	time.Sleep(20 * time.Millisecond)

	// A run after that: the copy goes into use, changes a file, adds one — and
	// its sync never reached the database.
	homestore.MarkInUse(home)
	if err := os.WriteFile(filepath.Join(home, "work.md"), []byte("v2"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "new.md"), []byte("added"), 0o644); err != nil {
		t.Fatal(err)
	}

	control, _ := startNodeOn(t, ctx, node)
	start, _ := encode(TypeStartSandbox, "1", StartSandbox{
		AgentID: agentID, OrgID: orgID, Snapshot: old, SnapshotAt: snapshotAt,
	})
	if err := control.Send(ctx, start); err != nil {
		t.Fatal(err)
	}
	synced := warteAufTyp(t, control, TypeHomeSynced)
	res, err := decode[HomeSynced](synced)
	if err != nil || res.ManifestHash == "" || res.ManifestHash == old {
		t.Fatalf("the copy has to be secured as a NEW state before the start: %+v (%v)", res, err)
	}
	warteAufTyp(t, control, TypeSandboxStarted)

	if b, _ := os.ReadFile(filepath.Join(home, "work.md")); string(b) != "v2" {
		t.Fatalf("the run's change was reverted: work.md = %q", b)
	}
	if _, err := os.Stat(filepath.Join(home, "new.md")); err != nil {
		t.Fatalf("the run's new file is gone: %v", err)
	}
	if _, err := homestore.Load(ctx, blobs, orgID, res.ManifestHash); err != nil {
		t.Fatalf("the reported state is not in the store: %v", err)
	}
}

// The other direction stays what it was: a copy that went into use BEFORE the
// control plane's snapshot was taken is superseded by it — the agent ran
// elsewhere afterwards — and the snapshot is materialised.
func TestAWorkingCopyOlderThanTheSnapshotIsMaterialisedOver(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	node.Blobs = blobs
	t.Cleanup(node.Close)

	// The copy on this host: in use, with a stale change nobody synced.
	home, _, _ := node.Docker.AgentHome(agentID)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "work.md"), []byte("stale"), 0o644); err != nil {
		t.Fatal(err)
	}
	homestore.MarkInUse(home)
	time.Sleep(20 * time.Millisecond)

	// The snapshot the control plane holds was taken later, on another host.
	other := filepath.Join(dir, "elsewhere")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(other, "work.md"), []byte("newer"), 0o644); err != nil {
		t.Fatal(err)
	}
	newer := syncedHome(t, ctx, blobs, orgID, other)

	control, _ := startNodeOn(t, ctx, node)
	start, _ := encode(TypeStartSandbox, "1", StartSandbox{
		AgentID: agentID, OrgID: orgID, Snapshot: newer, SnapshotAt: time.Now(),
	})
	if err := control.Send(ctx, start); err != nil {
		t.Fatal(err)
	}
	warteAufTyp(t, control, TypeSandboxStarted)
	if b, _ := os.ReadFile(filepath.Join(home, "work.md")); string(b) != "newer" {
		t.Fatalf("the later snapshot has to win over a stale copy: work.md = %q", b)
	}
}

// A link the network dropped without a FIN: the control plane's watchdog closes
// its side after three beats, and the node used to sit on its Receive until the
// kernel gave up on the heartbeat's writes — offline for a quarter of an hour
// while its own log said connected. It now closes the link itself when it has
// heard nothing for as long (#158).
func TestANodeThatHearsNothingClosesTheLinkAndDialsAgain(t *testing.T) {
	orgID, runnerID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{RunnerID: runnerID, DataDir: t.TempDir()}, quietLog())
	node.ReadSilence = 300 * time.Millisecond
	t.Cleanup(node.Close)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	control, done := startNodeOn(t, ctx, node)
	// The control plane says nothing at all.
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run ended with an error rather than a closed transport: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the node has to close a silent link")
	}
	// Whatever was still queued drains; then the closed transport says so.
	short, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	for {
		if _, err := control.Receive(short); err != nil {
			if !errors.Is(err, ErrTransportClosed) {
				t.Fatalf("the transport has to be closed from the node's side, got %v", err)
			}
			return
		}
	}
}
