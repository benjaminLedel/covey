package runner

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

/* Phases ist die Antwort auf „worauf wartet dieser Agent gerade". Sie muss drei
   Dinge können: die Zahlen mitführen, den Beginn festhalten und rechtzeitig
   aufhören, etwas zu behaupten. */

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
	// Die Dauer ist das, was ein Mensch zuerst liest. Bei jedem Lebenszeichen
	// neu zu beginnen ließe jede Wartezeit frisch aussehen.
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

// Eine neue Phase beginnt neu: Image holen und Home herstellen sind zwei
// Wartezeiten, keine fortgesetzte.
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

// Ein Host, der mitten in einer Phase verschwindet, hinterlässt sonst einen
// Balken, der für immer steht — und ein Balken, der stillsteht, ist schlimmer
// als keiner, weil er etwas behauptet.
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
	// Und All räumt sie weg — eine Karte, die nur wächst, ist in einem Prozess
	// mit Monaten Laufzeit ein Leck.
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

// Ein nil-Tracker ist erlaubt: wer einen Pool von Hand baut, verliert die
// Live-Anzeige und sonst nichts.
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
