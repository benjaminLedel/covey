package integration

import (
	"context"
	"testing"
	"time"

	"covey/internal/agents"
)

// The platform's own default heartbeats are reconciled across every agent at
// process start, so that agents created before a default existed get it
// without anybody touching their config. The rule that makes that safe is the
// one worth a test: it touches only the rows the platform wrote, and an agent
// that declares a heartbeat of the same name in its own HEARTBEAT.md keeps its
// own — the override wins, or a platform default would silently replace what
// somebody wrote by hand.
func TestSystemHeartbeatsReachEveryAgentButOverrideNobody(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	before := s.newSupportAgent("vorher")
	// An agent with a heartbeat of its own, under the name the platform is
	// about to claim.
	eigen := s.newSupportAgent("mit-eigenem")
	if _, err := s.registry.SaveConfig(ctx, eigen.ID, map[string]string{
		"SOUL.md":      "# Eigen\n\n## Role\nEigen.",
		"HEARTBEAT.md": "- alle: 30m titel: Eigener Takt aufgabe: Sieh nach.\n",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	own, err := s.registry.Heartbeats(ctx, eigen.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(own) == 0 {
		t.Fatal("the agent's own heartbeat was not stored")
	}
	ownName := own[0].Name

	s.registry.SystemHeartbeats = []agents.Heartbeat{
		{Name: ownName, Every: time.Hour, Task: "Von der Plattform."},
		{Name: "plattform-takt", Every: 2 * time.Hour, Task: "Auch von der Plattform."},
	}
	if err := s.registry.ReconcileSystemHeartbeats(ctx); err != nil {
		t.Fatal(err)
	}

	// The platform's own default arrived at an agent that existed before it.
	got, err := s.registry.Heartbeats(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sawPlatform bool
	for _, hb := range got {
		if hb.Name == "plattform-takt" {
			sawPlatform = true
		}
	}
	if !sawPlatform {
		t.Errorf("the platform default did not reach an existing agent: %+v", got)
	}

	// And the agent's own entry of the same name was left alone.
	after, err := s.registry.Heartbeats(ctx, eigen.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, hb := range after {
		if hb.Name == ownName && hb.Task == "Von der Plattform." {
			t.Error("a platform default overwrote an agent's own heartbeat")
		}
	}

	// A default the platform withdraws disappears again — otherwise every
	// heartbeat the project ever shipped would keep firing forever.
	s.registry.SystemHeartbeats = nil
	if err := s.registry.ReconcileSystemHeartbeats(ctx); err != nil {
		t.Fatal(err)
	}
	gone, err := s.registry.Heartbeats(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, hb := range gone {
		if hb.Name == "plattform-takt" {
			t.Error("a withdrawn platform default is still there")
		}
	}
	// The agent's own one survives the withdrawal, because it was never the
	// platform's to remove.
	still, err := s.registry.Heartbeats(ctx, eigen.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ownSurvived bool
	for _, hb := range still {
		if hb.Name == ownName {
			ownSurvived = true
		}
	}
	if !ownSurvived {
		t.Error("withdrawing a platform default took the agent's own heartbeat with it")
	}
}

// Which workplace image an organisation's agents are running is asked
// org-scoped: whoever looks at their own organisation must not be told how many
// agents the neighbours run.
func TestSandboxImagesInUseAreCountedPerOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	a := s.newSupportAgent("bild-eins")
	b := s.newSupportAgent("bild-zwei")
	if err := s.registry.SetSandboxImage(ctx, a.ID, "eigenes:1"); err != nil {
		t.Fatal(err)
	}
	if err := s.registry.SetSandboxImage(ctx, b.ID, "eigenes:1"); err != nil {
		t.Fatal(err)
	}

	mine, err := s.registry.SandboxImagesInUseForOrg(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if mine["eigenes:1"] != 2 {
		t.Errorf("the organisation's own count is %v", mine)
	}

	// A killed agent is running nothing, so it is not counted — the figure
	// answers "what is in use", not "what was ever configured".
	if err := s.registry.SetKilled(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	after, err := s.registry.SandboxImagesInUseForOrg(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if after["eigenes:1"] != 1 {
		t.Errorf("a killed agent is still counted: %v", after)
	}
}
