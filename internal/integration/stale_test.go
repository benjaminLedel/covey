package integration

import (
	"context"
	"testing"
	"time"
)

/* Ein Agent stand um 08:02 auf `working`: letzte Aufgabe um 06:35 fertig,
   Backlog leer, auf dem Host lief weiter ein Container (#83). Zwischen „letzte
   Aufgabe fertig" und „Sandbox unten" hängt keine Frist, die den ganzen Vorgang
   umfasst — jeder Schritt hat eine, der Ablauf als solcher nicht. Und ein
   Neustart der Steuerebene heilt es nicht: die Sitzungen liegen im Speicher,
   der Zustand in der Datenbank, und niemand vergleicht die beiden.

   Die Ursache ist unbekannt. Was hier festgehalten wird, ist das, was die
   Plattform trotzdem können muss: aus einem Zustand herauskommen, den niemand
   mehr trägt — ohne dass jemand sie neu startet. */

func TestEinZustandOhneSitzungLoestSichAuf(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{staleAfter: time.Second})
	agent := s.newSupportAgent("verwaist")

	// Der Zustand aus dem Vorfall, von Hand hergestellt: die Datenbank sagt
	// „arbeitet", im Orchestrator läuft nichts.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET status='working' WHERE id=$1`, agent.ID); err != nil {
		t.Fatal(err)
	}

	// Nicht beim ersten Hinsehen: zwischen „Zustand gesetzt" und „Sitzung
	// eingetragen" liegt ein Augenblick, und den darf niemand als Ausfall
	// lesen. Die Frist steht in diesem Stapel auf einer Sekunde, der Tick auf
	// 300 ms — nach einer halben Sekunde ist also gesehen, aber nicht gehandelt.
	time.Sleep(500 * time.Millisecond)
	if st := s.agentStatus(agent.ID); st != "working" {
		t.Fatalf("nach einer halben Sekunde schon %q — die Frist wird nicht abgewartet", st)
	}

	waitFor(t, "der Agent kommt nicht aus dem Zustand heraus", 30*time.Second, func() bool {
		return s.agentStatus(agent.ID) == "sleeping"
	})

	// Und es steht in der Aufzeichnung. Ohne diese Zeile sähe hinterher
	// niemand, dass die Plattform etwas aufgelöst hat — der Agent hätte
	// „einfach so" geschlafen, und die Stunde davor bliebe unerklärt.
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

// Die Gegenprobe, und sie ist die wichtigere: ein Agent, der WIRKLICH arbeitet,
// darf davon nichts merken. Ein Wächter, der laufende Läufe abräumt, ist
// schlimmer als der Fehler, gegen den er antritt.
func TestEinLaufenderAgentWirdNichtAufgeloest(t *testing.T) {
	ctx := context.Background()
	s := newStackWith(t, stackOpts{staleAfter: time.Second})
	agent := s.newSupportAgent("laeuft-wirklich")

	// Eine Aufgabe, die den Agenten beschäftigt hält, während der Wächter
	// mehrfach vorbeikommt (Tick alle 300 ms, Frist 1 s).
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Etwas, das dauert",
		"[mock:sleep 4s][mock:result fertig]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "der Lauf beginnt nicht", 20*time.Second, func() bool {
		st := s.agentStatus(agent.ID)
		return st == "working" || st == "triage"
	})

	// Über die Frist hinaus beschäftigt bleiben und dann normal fertig werden.
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
