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

/* Der Agent fährt hoch, was sein Projekt braucht (spec/16, #121).

   Das ist die Hälfte, die aus dem Mechanismus etwas Brauchbares macht: Eine
   getippte Deklaration setzt voraus, dass jemand VOR dem Lauf wusste, welche
   Datenbank dieses Projekt will. Für einen QA-Agenten, der Merge Requests in
   mehreren Projekten abnimmt, stimmt das ab dem zweiten Projekt nicht mehr —
   und die Antwort stand die ganze Zeit im Repository.

   Geprüft wird der ganze Weg: Der Agent schickt den Inhalt der Datei, die
   Steuerebene liest die Teilmenge, fragt die Allowlist der Organisation und
   lässt den Host hochfahren, was übrig bleibt. */

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
	// Die Organisation erlaubt Postgres — und sonst nichts.
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

	// Die Datenbank läuft — unter dem Namen, den die Compose-Datei ihr gibt.
	if len(got.Services) != 1 || got.Services[0].Name != "db" || got.Services[0].Image != "postgres:16" {
		t.Fatalf("the database did not come up: %+v", got.Services)
	}
	// Das fremde Image nicht, und das steht als Ablehnung da statt still zu
	// verschwinden: Ein Agent, der nicht weiß, dass etwas fehlt, meldet den
	// falschen Befund.
	if len(got.Refused) != 1 || got.Refused[0].Name != "forbidden" {
		t.Fatalf("the image outside the allowlist was not refused: %+v", got.Refused)
	}
	if got.Source != "compose" {
		t.Errorf("the recording does not say where the list came from: %+v", got)
	}
}

// Ohne den Scope existiert die Aktion für den Agenten nicht — und er liest auch
// nichts darüber. Eine angedeutete Fähigkeit, die dann abgewiesen wird, ist die
// schlechteste Sorte (spec/20).
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
	// Und die Aktion selbst wird abgewiesen — mit der Zeile, die sie
	// freischalten würde, denn wer das liest, ist ein Mensch an einer Config.
	events, _ := s.obs.Events(ctx, agent.ID, &task.ID, 0, 500)
	for _, e := range events {
		if e.Kind == observability.KindService {
			t.Fatalf("services were started without the scope: %s", e.Payload)
		}
	}
}
