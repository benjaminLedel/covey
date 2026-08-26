package integration

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"covey/internal/observability"
)

// Where an agent last worked is a question with three possible answers, and only
// one of them is right: the orchestrator knows it while the sandbox stands, the
// recording knows it afterwards, and the home store knows only where the
// working copy lies — which is the same host until a run is interrupted before
// its sync, and then it is confidently wrong.
//
// This covers the middle one, and with it the case that made the interface show
// the wrong host on covey.work: the newest `sandbox` event has to be found no
// matter how many events have been written since. The window the log view loads
// is 500, and a talkative run fills that in nine minutes.
func TestTheLastPlacementIsFoundBehindALoudRun(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	agent := s.newSupportAgent("agent-that-talks-a-lot")

	first := uuid.New()
	if err := s.obs.Record(ctx, s.orgID, agent.ID, nil, observability.KindLifecycle, map[string]any{
		"status": "sandbox", "runner": first.String(), "runner_name": "built-in",
	}); err != nil {
		t.Fatal(err)
	}
	// The run it belonged to, then a second one somewhere else.
	second := uuid.New()
	if err := s.obs.Record(ctx, s.orgID, agent.ID, nil, observability.KindLifecycle, map[string]any{
		"status": "sandbox", "runner": second.String(), "runner_name": "build host frankfurt",
	}); err != nil {
		t.Fatal(err)
	}
	// And 600 events of chatter on top — more than the log view ever loads.
	for i := 0; i < 600; i++ {
		if err := s.obs.Record(ctx, s.orgID, agent.ID, nil, observability.KindRuntime, map[string]any{
			"type": "assistant", "n": i,
		}); err != nil {
			t.Fatal(err)
		}
	}

	runnerID, name, err := s.obs.LastPlacement(ctx, agent.ID)
	if err != nil {
		t.Fatalf("LastPlacement: %v", err)
	}
	if runnerID != second.String() {
		t.Errorf("the newest placement counts: %s", runnerID)
	}
	if name != "build host frankfurt" {
		t.Errorf("the host's name travels with it: %q", name)
	}

	// An agent that has never woken has no host, and that is not an error —
	// the caller falls back to the home store, or to nothing.
	fresh := s.newSupportAgent("agent-that-never-woke")
	if runnerID, _, err := s.obs.LastPlacement(ctx, fresh.ID); err != nil || runnerID != "" {
		t.Errorf("without a run there is no host: %q, %v", runnerID, err)
	}
}
