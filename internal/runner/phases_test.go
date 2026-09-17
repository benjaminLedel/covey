package runner

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

/* Phases is the answer to "what is this agent waiting for right now". It must
   do three things: carry the numbers, hold on to the start and stop
   claiming something in time. */

func TestDiePhaseHaeltIhrenBeginn(t *testing.T) {
	p := NewPhases()
	agent, runner := uuid.New(), uuid.New()

	p.Note(runner, Progress{AgentID: agent, Phase: PhaseImage, Detail: "sandbox:main"})
	erste, ok := p.Of(agent)
	if !ok {
		t.Fatal("die begonnene Phase fehlt")
	}
	time.Sleep(5 * time.Millisecond)
	p.Note(runner, Progress{AgentID: agent, Phase: PhaseImage, Bytes: 500, BytesTotal: 1000})

	zweite, ok := p.Of(agent)
	if !ok {
		t.Fatal("die laufende Phase fehlt")
	}
	// The duration is what a person reads first. Starting it anew at every sign
	// of life would let every wait look fresh.
	if !zweite.Since.Equal(erste.Since) {
		t.Fatalf("der Beginn wanderte mit: %v → %v", erste.Since, zweite.Since)
	}
	if zweite.Bytes != 500 || zweite.BytesTotal != 1000 {
		t.Fatalf("die Zahlen kamen nicht an: %+v", zweite)
	}
	if zweite.Runner != runner {
		t.Fatal("der Host fehlt — bei zwei Maschinen ist das die erste Frage")
	}
}

// A new phase begins anew: fetching the image and building the home are two
// waits, not one continued.
func TestEineAnderePhaseBeginntNeu(t *testing.T) {
	p := NewPhases()
	agent := uuid.New()
	p.Note(uuid.New(), Progress{AgentID: agent, Phase: PhaseImage})
	erste, _ := p.Of(agent)
	time.Sleep(5 * time.Millisecond)
	p.Note(uuid.New(), Progress{AgentID: agent, Phase: PhaseHome})
	zweite, _ := p.Of(agent)
	if zweite.Phase != PhaseHome || !zweite.Since.After(erste.Since) {
		t.Fatalf("die zweite Phase erbte die erste: %+v", zweite)
	}
}

func TestDieSchlussmeldungBeendetDieAnzeige(t *testing.T) {
	p := NewPhases()
	agent := uuid.New()
	p.Note(uuid.New(), Progress{AgentID: agent, Phase: PhaseHomeSync})
	p.Note(uuid.New(), Progress{AgentID: agent, Phase: PhaseHomeSync, Bytes: 42, Done: true})
	if _, ok := p.Of(agent); ok {
		t.Fatal("die fertige Phase steht weiter als laufend da")
	}
}

// A host that disappears in the middle of a phase otherwise leaves a bar
// that stands forever — and a bar that stands still is worse
// than none, because it claims something.
func TestEinePhaseOhneLebenszeichenVerfaellt(t *testing.T) {
	p := NewPhases()
	agent := uuid.New()
	p.Note(uuid.New(), Progress{AgentID: agent, Phase: PhaseHomeSync})

	p.mu.Lock()
	ph := p.cur[agent]
	ph.Updated = time.Now().Add(-phaseStale - time.Second)
	p.cur[agent] = ph
	p.mu.Unlock()

	if _, ok := p.Of(agent); ok {
		t.Fatal("eine Phase ohne Lebenszeichen gilt weiter als laufend")
	}
	// And All clears them away too — a map that only grows is a leak in a
	// process with months of uptime.
	if len(p.All()) != 0 {
		t.Fatal("All gibt die verfallene Phase heraus")
	}
	p.mu.Lock()
	rest := len(p.cur)
	p.mu.Unlock()
	if rest != 0 {
		t.Fatalf("%d verfallene Einträge blieben liegen", rest)
	}
}

func TestClearBeendetDieAnzeige(t *testing.T) {
	p := NewPhases()
	agent := uuid.New()
	p.Note(uuid.New(), Progress{AgentID: agent, Phase: PhaseHome})
	p.Clear(agent)
	if _, ok := p.Of(agent); ok {
		t.Fatal("Clear hat nichts beendet")
	}
}

// A nil tracker is allowed: whoever builds a pool by hand loses the live
// display and nothing else.
func TestOhneTrackerFaelltNichtsUm(t *testing.T) {
	var p *Phases
	p.Note(uuid.New(), Progress{AgentID: uuid.New(), Phase: PhaseHome})
	p.Clear(uuid.New())
	if _, ok := p.Of(uuid.New()); ok {
		t.Fatal("der nil-Tracker behauptet eine Phase")
	}
	if p.All() != nil {
		t.Fatal("der nil-Tracker gibt eine Karte heraus")
	}
}
