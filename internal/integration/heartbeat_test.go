package integration

import (
	"context"
	"testing"
	"time"
)

// TestHeartbeat prüft den Zeitplan-Trigger aus spec/03: HEARTBEAT.md wird beim
// Speichern materialisiert, feuert nicht sofort, legt dann periodisch eine
// Backlog-Aufgabe (origin=heartbeat) an und wird beim Entfernen aufgeräumt.
func TestHeartbeat(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "puls", "Puls-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":      "# Puls-Agent",
		"HEARTBEAT.md": "- alle: 1s titel: Puls aufgabe: Melde dich. [mock:result ok]",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	// Baseline: ein frisch gespeicherter Heartbeat feuert nicht sofort.
	tasks, err := s.backlog.ListByAgent(ctx, agent.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("kein sofortiges Feuern erwartet, backlog: %+v", tasks)
	}

	// Nach Ablauf des Intervalls legt der Tick die Aufgabe an.
	waitFor(t, "heartbeat-aufgabe erscheint", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
		for _, task := range tasks {
			if task.Origin == "heartbeat" && task.Title == "Puls" {
				return true
			}
		}
		return false
	})

	// Kaputte HEARTBEAT.md erzeugt keine neue Version.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":      "# Puls-Agent",
		"HEARTBEAT.md": "- alle: 1s aufgabe: titel fehlt",
	}, &s.adminID); err == nil {
		t.Fatal("SaveConfig mit kaputter HEARTBEAT.md muss scheitern")
	}

	// Eintrag entfernen: die Materialisierung wird aufgeräumt.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md": "# Puls-Agent",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM agent_heartbeats WHERE agent_id=$1", agent.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("agent_heartbeats nicht aufgeräumt: %d Einträge", n)
	}
}
