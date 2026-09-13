package orchestrator

import (
	"testing"

	"github.com/google/uuid"
)

// imagedSandbox is a fakeSandbox that also knows what it is running.
type imagedSandbox struct {
	fakeSandbox
	ref, id string
}

func (s *imagedSandbox) Image() (string, string) { return s.ref, s.id }

// The warm sandbox is the case this exists for. An agent that never falls
// asleep never starts again either, so it keeps the image — and with it the
// plugins — of the day its sandbox came up. Half an hour went into suspecting
// a fix that was fine, because the agent's page showed the control plane's
// version and nothing said which workplace the sandbox was actually in (#217).
func TestAParkedSandboxStillSaysWhichImageItRuns(t *testing.T) {
	o := New(Options{})
	agent := uuid.New()

	if _, _, ok := o.Workplace(agent); ok {
		t.Fatal("nothing is standing, so there is no image to name")
	}

	sb := &imagedSandbox{ref: "ghcr.io/x/covey-sandbox@sha256:old", id: "sha256:aaa"}
	o.parkWarm(agent, newFakeLink(), sb)

	ref, id, ok := o.Workplace(agent)
	if !ok || ref != sb.ref || id != sb.id {
		t.Fatalf("a parked sandbox has to answer: ref=%q id=%q ok=%v", ref, id, ok)
	}
}

// A provider that cannot say must not be made to. Then nothing is claimed, and
// the interface shows no comparison rather than a wrong one.
func TestASandboxThatCannotSayIsNotGuessedFor(t *testing.T) {
	o := New(Options{})
	agent := uuid.New()
	o.parkWarm(agent, newFakeLink(), &fakeSandbox{})

	if ref, id, ok := o.Workplace(agent); ok || ref != "" || id != "" {
		t.Fatalf("nothing may be invented here: ref=%q id=%q ok=%v", ref, id, ok)
	}
}
