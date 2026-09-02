package runner

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/orchestrator"
)

// A handshake that names another runner's id is refused BEFORE it touches the
// pool. It used to be admitted first and checked second, and the detach that
// followed the refusal removed what had just been inserted — the victim's own
// live entry, since both went under the same key (#159).
func TestAForeignIdentityIsRefusedWithoutEvictingItsOwner(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	_, victim, _ := registriereFalschenRunner(t, p, orgID)

	// A second connection, authenticated as another runner of the same
	// organisation, claims the victim's id.
	impostorToken := uuid.New()
	control, nodeEnd := NewInProc()
	reg, _ := encode(TypeRegistered, "", Registered{RunnerID: victim, OrgID: orgID, Protocol: Protocol})
	ctx := context.Background()
	if err := nodeEnd.Send(ctx, reg); err != nil {
		t.Fatal(err)
	}
	err := p.AttachRemote(ctx, control, impostorToken, orgID, nil)
	if err == nil {
		t.Fatal("a handshake naming a foreign identity has to be refused")
	}

	c := p.connFor(orgID, victim)
	if c == nil {
		t.Fatal("the refused handshake evicted the runner whose id it claimed")
	}
	select {
	case <-c.gone:
		t.Fatal("the victim's connection was ended by the refused handshake")
	default:
	}
}

// A runner reporting on an agent that runs on ANOTHER host is telling a story
// about somebody else's sandbox. sandbox_exited from it must not end that
// sandbox's wake (#159).
func TestASandboxExitIsOnlyTakenFromTheHostThatRunsIt(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	died := make(chan uuid.UUID, 1)
	p.SandboxDied = func(agentID uuid.UUID, _ string) { died <- agentID }

	// Two runners; the agent is placed on the first.
	_, host, _ := registriereFalschenRunner(t, p, orgID)
	other, _, _ := registriereFalschenRunner(t, p, orgID)
	agentID := uuid.New()
	p.mu.Lock()
	p.placed[agentID] = host
	p.mu.Unlock()

	exit, _ := encode(TypeSandboxExited, "", SandboxExited{AgentID: agentID, Reason: "made up"})
	if err := other.Send(context.Background(), exit); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-died:
		t.Fatalf("a report from a host the agent does not run on ended its wake: %s", id)
	case <-time.After(300 * time.Millisecond):
	}
}

// An assignment from the interface while a wake is picking a host: the writer
// takes c.mu, the readers used to take only p.mu. The race detector is the
// assertion here (#157).
func TestAssigningCapabilitiesWhileSchedulingIsRaceFree(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	_, id, _ := registriereFalschenRunner(t, p, orgID)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 200; i++ {
			p.SetCapabilities(id, []string{"gpu"}, []string{"img"}, true)
			p.SetCapabilities(id, nil, nil, false)
		}
	}()
	for i := 0; i < 200; i++ {
		_, _ = p.candidates(need{orgID: orgID, tags: []string{"gpu"}})
		_ = p.LiveFor(orgID)
	}
	<-done
}

// EnsureLocal used to be asked on EVERY failed pick — tags no host carries,
// every host paused — and every answer attached a fresh built-in runner beside
// the one already there, which kept running for the life of the process. One
// per organisation, and once (#160).
func TestTheBuiltInRunnerIsBroughtUpOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	p := NewPool(quietLog())
	p.Profiles = map[string]string{"base": "covey-sandbox:test"}
	p.StartTimeout = 5 * time.Second

	var ensured atomic.Int32
	runnerID := uuid.New()
	p.EnsureLocal = func(ctx context.Context, org uuid.UUID) error {
		ensured.Add(1)
		node := NewNode(runnerID, org, &Docker{
			RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
			DockerBin: fakeDockerBin(t, dir, "nothing"),
		}, quietLog())
		t.Cleanup(node.Close)
		return p.AttachLocal(ctx, node)
	}

	for i := 0; i < 3; i++ {
		_, err := p.Start(ctx, orchestrator.SandboxSpec{
			AgentID: uuid.New(), OrgID: orgID, RunnerTags: []string{"gpu"},
		})
		if err == nil {
			t.Fatal("no host carries the tag — the start has to fail")
		}
	}
	if n := ensured.Load(); n != 1 {
		t.Fatalf("the built-in runner was brought up %d times", n)
	}
	if got := len(p.LiveFor(orgID)); got != 1 {
		t.Fatalf("%d built-in runners stand in the pool", got)
	}
}
