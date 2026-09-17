package integration

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/sandbox"
)

/* The agent brings up what its project needs (spec/16, #121).

   That is the half that makes something usable out of the mechanism: a
   typed-up declaration presupposes that someone KNEW BEFORE the run which
   database this project wants. For a QA agent that signs off merge requests in
   several projects, that stops being true from the second project on —
   and the answer was standing in the repository the whole time.

   The whole path is checked: the agent sends the content of the file, the
   control plane reads the subset, asks the organisation's allowlist and
   has the host bring up what is left. */

const composeFile = `services:
  app:
    build: .
  db:
    image: postgres:16
    environment:
      POSTGRES_PASSWORD: test
  forbidden:
    image: attacker/backdoor:latest
`

// addingProvider stands in for a host that can take services while it runs.
type addingProvider struct {
	inner orchestrator.SandboxProvider
}

func (p *addingProvider) Start(ctx context.Context, spec orchestrator.SandboxSpec) (orchestrator.Sandbox, error) {
	sb, err := p.inner.Start(ctx, spec)
	if err != nil {
		return nil, err
	}
	return &addingSandbox{Sandbox: sb}, nil
}

type addingSandbox struct {
	orchestrator.Sandbox
	got []sandbox.Service
}

func (s *addingSandbox) StartServices(_ context.Context, services []sandbox.Service) ([]sandbox.ServiceRun, error) {
	s.got = append(s.got, services...)
	runs := make([]sandbox.ServiceRun, 0, len(services))
	for _, svc := range services {
		runs = append(runs, sandbox.ServiceRun{Name: svc.Name, Image: svc.Image, ImageID: "sha256:" + svc.Name})
	}
	return runs, nil
}

func TestTheAgentBringsUpItsProjectsServices(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			return &addingProvider{inner: &inprocProvider{homeBase: homeBase, log: log}}
		},
	})

	agent := s.newSupportAgent("qa-that-sets-itself-up")
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# QA\n\n## Rolle\nNimmt ab.",
		"ACCESS.md": "- system: covey scope: services:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	// The organisation permits Postgres — and nothing else.
	if _, err := s.workplaces.AddServicePattern(ctx, s.orgID, "postgres:*", ""); err != nil {
		t.Fatal(err)
	}

	body, err := json.Marshal(map[string]string{"compose": composeFile})
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Projekt aufsetzen",
		"[mock:action covey/start_services "+string(body)+"]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 40*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	events, err := s.obs.Events(ctx, agent.ID, &task.ID, 0, 500)
	if err != nil {
		t.Fatal(err)
	}
	var payload string
	for _, e := range events {
		if e.Kind == observability.KindService && strings.Contains(string(e.Payload), "compose") {
			payload = string(e.Payload)
		}
	}
	if payload == "" {
		t.Fatal("the agent's request for services was not recorded on the job")
	}
	var got struct {
		Status   string               `json:"status"`
		Source   string               `json:"source"`
		Services []sandbox.ServiceRun `json:"services"`
		Refused  []struct {
			Name  string `json:"name"`
			Image string `json:"image"`
		} `json:"refused"`
	}
	if err := json.Unmarshal([]byte(payload), &got); err != nil {
		t.Fatalf("the event is not readable: %v\n%s", err, payload)
	}

	// The database runs — under the name that the compose file gives it.
	if len(got.Services) != 1 || got.Services[0].Name != "db" || got.Services[0].Image != "postgres:16" {
		t.Fatalf("the database did not come up: %+v", got.Services)
	}
	// The foreign image does not, and that stands there as a refusal instead of
	// vanishing quietly: an agent that does not know that something is missing
	// reports the wrong finding.
	if len(got.Refused) != 1 || got.Refused[0].Name != "forbidden" {
		t.Fatalf("the image outside the allowlist was not refused: %+v", got.Refused)
	}
	if got.Source != "compose" {
		t.Errorf("the recording does not say where the list came from: %+v", got)
	}
}

// TestWithoutTheScopeTheAgentNeitherReadsNorCallsIt: without the scope the action
// does not exist for the agent — and it reads nothing about it either. A
// hinted capability that is then refused is the worst kind (spec/20).
func TestWithoutTheScopeTheAgentNeitherReadsNorCallsIt(t *testing.T) {
	ctx := context.Background()
	s := newStack(t)

	agent := s.newSupportAgent("qa-ohne-scope")
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# QA\n\n## Rolle\nNimmt ab.",
		"ACCESS.md": "- system: covey scope: agents:write",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	body, _ := json.Marshal(map[string]string{"compose": composeFile})
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Prompt und Aktion",
		"[mock:prompt] [mock:action covey/start_services "+string(body)+"]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task settled", 40*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st == backlog.StateDone || st == backlog.StateFailed
	})
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	result := ""
	if got.Result != nil {
		result = *got.Result
	}
	if strings.Contains(result, "start_services") && !strings.Contains(result, "covey") {
		t.Fatalf("an agent without the scope read about the action:\n%s", kürzen(result))
	}
	// And the action itself is rejected — with the line that would unlock
	// it, because whoever reads this is a person at a config.
	events, _ := s.obs.Events(ctx, agent.ID, &task.ID, 0, 500)
	for _, e := range events {
		if e.Kind == observability.KindService {
			t.Fatalf("services were started without the scope: %s", e.Payload)
		}
	}
}
