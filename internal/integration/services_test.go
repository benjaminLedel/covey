package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/observability"
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
	sb, err := p.inner.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	// Stands in for the host: it reports back which image each service
	// actually started from. In-process there is no docker to ask, so the id
	// is made up — what is under test here is that the answer travels and gets
	// recorded, not what docker would have said.
	runs := make([]sandbox.ServiceRun, 0, len(spec.Services))
	for _, svc := range spec.Services {
		runs = append(runs, sandbox.ServiceRun{
			Name: svc.Name, Image: svc.Image, ImageID: "sha256:" + svc.Name,
		})
	}
	return &sandboxWithServices{Sandbox: sb, services: runs}, nil
}

type sandboxWithServices struct {
	orchestrator.Sandbox
	services []sandbox.ServiceRun
}

func (s *sandboxWithServices) Services() []sandbox.ServiceRun { return s.services }

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
	// The organisation says which images may run beside its sandboxes. Without
	// this line the wake below is refused, and that is the subject of
	// TestAnImageOutsideTheAllowlistDoesNotRun.
	if _, err := s.workplaces.AddServicePattern(ctx, s.orgID, "postgres:*", "test"); err != nil {
		t.Fatal(err)
	}
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

// The allowlist is the answer to "which foreign code may run on the runner",
// and the wake is the last gate before a container exists. An image outside it
// does not get a smaller workplace — it gets no wake, with the reason recorded.
func TestAnImageOutsideTheAllowlistDoesNotRun(t *testing.T) {
	ctx := context.Background()
	prov := &recordingProvider{}
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			prov.inner = &inprocProvider{homeBase: homeBase, log: log}
			return prov
		},
	})

	agent := s.newSupportAgent("agent-with-a-foreign-image")
	// The organisation allows postgres and nothing else.
	if _, err := s.workplaces.AddServicePattern(ctx, s.orgID, "postgres:*", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.registry.SetServices(ctx, agent.ID, []sandbox.Service{
		{Name: "db", Image: "postgres:16"},
		{Name: "evil", Image: "attacker/backdoor:latest"},
	}); err != nil {
		t.Fatal(err)
	}

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas erledigen",
		"[mock:result erledigt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	// The refusal is recorded; the task does not get done.
	waitFor(t, "the refusal is recorded", 30*time.Second, func() bool {
		events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
		for _, e := range events {
			if e.Kind == observability.KindService &&
				strings.Contains(string(e.Payload), "refused") {
				return true
			}
		}
		return false
	})
	if _, ok := prov.last(); ok {
		t.Error("a sandbox was started although one of its images is not allowed")
	}
	if state := s.taskState(task.ID); state == backlog.StateDone {
		t.Error("the task was done although the workplace was refused")
	}

	// And the refusal carries its remedy: the pattern that would let it run.
	events, _ := s.obs.Events(ctx, agent.ID, nil, 0, 500)
	var found string
	for _, e := range events {
		if e.Kind == observability.KindService && strings.Contains(string(e.Payload), "refused") {
			found = string(e.Payload)
		}
	}
	for _, want := range []string{"attacker/backdoor:latest", "attacker/backdoor:*"} {
		if !strings.Contains(found, want) {
			t.Errorf("the recorded refusal does not contain %q:\n%s", want, found)
		}
	}
}

// What a job ran against has to be readable ON the job. A warm sandbox serves
// many of them, and the services it works with came up in a waking phase that
// may be long off the screen.
func TestTheJobRecordsWhichImagesItRanAgainst(t *testing.T) {
	ctx := context.Background()
	prov := &recordingProvider{}
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			prov.inner = &inprocProvider{homeBase: homeBase, log: log}
			return prov
		},
	})

	agent := s.newSupportAgent("agent-that-records-its-services")
	if _, err := s.workplaces.AddServicePattern(ctx, s.orgID, "postgres:*", ""); err != nil {
		t.Fatal(err)
	}
	if err := s.registry.SetServices(ctx, agent.ID, []sandbox.Service{
		{Name: "db", Image: "postgres:16"},
	}); err != nil {
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

	// Scoped to the TASK, not to the agent: this is the query the run view
	// makes, and an event hanging off the agent alone would be invisible in it.
	events, err := s.obs.Events(ctx, agent.ID, &task.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var payload string
	for _, e := range events {
		if e.Kind == observability.KindService {
			payload = string(e.Payload)
		}
	}
	if payload == "" {
		t.Fatal("the job does not record which services it ran against")
	}
	// The image AND what it resolved to: a tag is a moving target, and the
	// question six months later is which bytes ran. Compared through the
	// decoded payload rather than the raw text — jsonb decides its own
	// whitespace, and a test that pins that is testing Postgres.
	var got struct {
		Status   string               `json:"status"`
		Services []sandbox.ServiceRun `json:"services"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("the service event is not readable: %v\n%s", err, payload)
	}
	if got.Status != "running" || len(got.Services) != 1 {
		t.Fatalf("unexpected service event: %+v", got)
	}
	if svc := got.Services[0]; svc.Name != "db" || svc.Image != "postgres:16" || svc.ImageID != "sha256:db" {
		t.Errorf("the job recorded the wrong service: %+v", svc)
	}
}
