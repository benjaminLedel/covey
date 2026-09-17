package integration

import (
	"context"
	"testing"
	"time"
)

/* An agent stood on `working` at 08:02: last task done at 06:35, backlog empty,
   a container still running on the host (#83). Between "last task done" and
   "sandbox down" no deadline hangs that covers the whole process — every step
   has one, the sequence as a whole does not. And a restart of the control plane
   does not heal it: the sessions lie in memory, the state in the database, and
   nobody compares the two.

   The cause is unknown. What is recorded here is what the platform must be able
   to do anyway: get out of a state that nobody carries any more — without
   anyone restarting it. */

func TestEinZustandOhneSitzungLoestSichAuf(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{staleAfter: time.Second})
	agent := s.newSupportAgent("verwaist")

	// The state from the incident, produced by hand: the database says `working`,
	// nothing runs in the orchestrator.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET status='working' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}

	// Not on the first look: between "state set" and "session registered" lies a
	// moment, and nobody may read that as an outage. The deadline stands at one
	// second in this stack, the tick at 300 ms — so after half a second it is
	// seen, but not acted on.
	time.Sleep(500 * time.Millisecond)
	if st := s.agentStatus(agent.ID); st != "working" {
		t.Fatalf("nach einer halben Sekunde schon %q — die Frist wird nicht abgewartet", st)
	}

	waitFor(t, "der Agent kommt nicht aus dem Zustand heraus", 30*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})

	// And it stands in the recording. Without this line nobody would see
	// afterwards that the platform resolved something — the agent would have
	// slept "just like that", and the hour before would stay unexplained.
	var n int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
		WHERE agent_id=$1 AND kind='lifecycle' AND payload->>'status'='stale'
		  AND payload->>'was'='working'`, agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("aufgelöst, aber nicht aufgeschrieben")
	}
}

// The counter-check, and it is the more important one: an agent that REALLY
// works may notice nothing of it. A guard that clears running runs is worse than
// the error it is meant against.
func TestEinLaufenderAgentWirdNichtAufgeloest(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{staleAfter: time.Second})
	agent := s.newSupportAgent("laeuft-wirklich")

	// A task that keeps the agent busy while the guard comes past several times
	// (tick every 300 ms, deadline 1 s).
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas, das dauert",
		"[mock:sleep 4s][mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "der Lauf beginnt nicht", 20*time.Second, func() bool {
		st := s.agentStatus(agent.ID)
		return st == "working" || st == "triage"
	})

	// Stay busy past the deadline and then finish normally.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if st := s.agentStatus(agent.ID); st == "sleeping" {
			t.Fatal("der Wächter hat einen laufenden Agenten schlafen gelegt")
		}
		time.Sleep(200 * time.Millisecond)
	}
	waitFor(t, "die Aufgabe wird nicht fertig", 40*time.Second, func() bool {
		return s.taskState(task.ID) == "done"
	})
}
