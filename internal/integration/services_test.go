package integration

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/orchestrator"
	"covey/internal/sandbox"
)

/* The services that run beside a sandbox (spec/16, issue #121).

   A workplace was an image and nothing else, so a project's database ended up
   either built into the image and started by hand — `mariadb-install-db` into
   the agent's own home, which is walked at every wake — or missing, at which
   point the QA agent's procedure had one exit: write a finding and hand over.

   What is checked here is the whole way, because the two halves fail
   differently: the declaration has to survive a real Postgres as jsonb, and it
   has to arrive at the provider that starts the sandbox. A field that stores
   correctly and is dropped in the orchestrator would pass a unit test on either
   side. */

// recordingProvider keeps the spec of every wake so a test can look at what the
// data plane was actually asked for.
type recordingProvider struct {
	inner orchestrator.SandboxProvider
	mu    sync.Mutex
	specs []orchestrator.SandboxSpec
}

func (p *recordingProvider) Start(ctx context.Context, spec orchestrator.SandboxSpec) (orchestrator.Sandbox, error) {
	p.mu.Lock()
	p.specs = append(p.specs, spec)
	p.mu.Unlock()
	return p.inner.Start(ctx, spec)
}

func (p *recordingProvider) last() (orchestrator.SandboxSpec, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.specs) == 0 {
		return orchestrator.SandboxSpec{}, false
	}
	return p.specs[len(p.specs)-1], true
}

func TestTheDeclaredServicesReachTheSandbox(t *testing.T) {
	ctx := context.Background()
	prov := &recordingProvider{}
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			prov.inner = &inprocProvider{homeBase: homeBase, log: log}
			return prov
		},
	})

	agent := s.newSupportAgent("agent-with-a-database")
	if err := s.registry.SetServices(ctx, agent.ID, []sandbox.Service{
		{Name: "db", Image: "postgres:16", Env: map[string]string{"POSTGRES_PASSWORD": "test"}},
	}); err != nil {
		t.Fatal(err)
	}

	// Through the database and back: jsonb, a real Postgres, not a struct held
	// in memory by the test.
	got, err := s.registry.Get(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 1 {
		t.Fatalf("the declaration did not survive the round trip: %+v", got.Services)
	}
	if got.Services[0].Name != "db" || got.Services[0].Image != "postgres:16" ||
		got.Services[0].Env["POSTGRES_PASSWORD"] != "test" {
		t.Fatalf("the declaration came back changed: %+v", got.Services[0])
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas erledigen",
		"[mock:result erledigt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	spec, ok := prov.last()
	if !ok {
		t.Fatal("no sandbox was started")
	}
	if len(spec.Services) != 1 || spec.Services[0].Name != "db" {
		t.Fatalf("the data plane was not asked for the service: %+v", spec.Services)
	}
}

// An agent that declares nothing must arrive with nothing. That is the normal
// case, and the one where a default creeping in would cost every installation.
func TestAnAgentWithoutServicesAsksForNone(t *testing.T) {
	ctx := context.Background()
	prov := &recordingProvider{}
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			prov.inner = &inprocProvider{homeBase: homeBase, log: log}
			return prov
		},
	})

	agent := s.newSupportAgent("agent-without-a-database")
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas erledigen",
		"[mock:result erledigt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	spec, ok := prov.last()
	if !ok {
		t.Fatal("no sandbox was started")
	}
	if len(spec.Services) != 0 {
		t.Fatalf("a service appeared that nobody declared: %+v", spec.Services)
	}
}

// The registry refuses what the runner would otherwise have to interpret: the
// validation sits before the database, not after it.
func TestTheRegistryRefusesAnUnusableDeclaration(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("agent-with-a-bad-declaration")

	if err := s.registry.SetServices(ctx, agent.ID, []sandbox.Service{
		{Name: "My DB", Image: "postgres:16"},
	}); err == nil {
		t.Fatal("a name that is not a host name was stored")
	}
	got, err := s.registry.Get(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Services) != 0 {
		t.Fatalf("the refused declaration was stored anyway: %+v", got.Services)
	}
}
