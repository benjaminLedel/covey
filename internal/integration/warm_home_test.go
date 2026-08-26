package integration

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/orchestrator"
)

// A warm agent's sandbox is not stopped when its job ends — and the sync of the
// home used to hang off that stop alone. What such a run produced therefore
// lived in one container volume on one host until something tore the sandbox
// down, and every way of losing the container in between (a hard restart, a
// prune, an OOM kill) lost the run with it. `spec/16-runner.md` decides the
// other way round: after every job the home goes into the store.
//
// This test guards the CALL SITE, which unit tests cannot: the orchestrator's
// own teardown, at the end of a real run through the real stack. Whoever moves
// the parking around later has to keep this true — the sync is not a property
// of the sandbox, it is a property of a job that has ended.

// syncCountingProvider hands out sandboxes that count how often their home was
// written away, and whether the compute was stopped.
type syncCountingProvider struct {
	inner  orchestrator.SandboxProvider
	syncs  atomic.Int32
	stops  atomic.Int32
	synced chan struct{}
}

func (p *syncCountingProvider) Start(ctx context.Context, spec orchestrator.SandboxSpec) (orchestrator.Sandbox, error) {
	sb, err := p.inner.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &syncCountingSandbox{inner: sb, p: p}, nil
}

type syncCountingSandbox struct {
	inner orchestrator.Sandbox
	p     *syncCountingProvider
}

func (s *syncCountingSandbox) Stop(ctx context.Context) error {
	s.p.stops.Add(1)
	return s.inner.Stop(ctx)
}

func (s *syncCountingSandbox) SyncHome(context.Context) error {
	s.p.syncs.Add(1)
	select {
	case s.p.synced <- struct{}{}:
	default:
	}
	return nil
}

func TestAFinishedJobPutsAWarmHomeIntoTheStore(t *testing.T) {
	ctx := context.Background()
	prov := &syncCountingProvider{synced: make(chan struct{}, 4)}
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			prov.inner = &inprocProvider{homeBase: homeBase, log: log}
			return prov
		},
	})

	agent := s.newSupportAgent("warm-home-agent")
	if err := s.registry.SetWarmSandbox(ctx, agent.ID, true); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas erledigen",
		"[mock:result erledigt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// The sync runs off the sleep path, so it may arrive shortly after the task
	// is done — that is the point of it, not a race in the test.
	select {
	case <-prov.synced:
	case <-time.After(20 * time.Second):
		t.Fatal("the finished job did not put the home into the store — a warm run would exist only in its container")
	}
	if got := prov.syncs.Load(); got != 1 {
		t.Errorf("expected exactly one sync for one job, got %d", got)
	}
	// And the compute stays up: that is what makes it warm. A stop here would
	// buy the snapshot with a cold start on the next wake.
	if got := prov.stops.Load(); got != 0 {
		t.Errorf("the warm sandbox was stopped %d times — then it is not warm any more", got)
	}
}
