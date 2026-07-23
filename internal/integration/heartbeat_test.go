package integration

import (
	"context"
	"net/http"
	"strings"
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

	// Monitoring-Sicht: Zeitplan und nächster Lauf sind konsistent.
	hbs, err := s.registry.Heartbeats(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hbs) != 1 || hbs[0].Name != "Puls" {
		t.Fatalf("ein Heartbeat 'Puls' erwartet: %+v", hbs)
	}
	if hbs[0].EverySeconds == nil || *hbs[0].EverySeconds != 1 {
		t.Fatalf("every_seconds=1 erwartet: %+v", hbs[0])
	}
	if !hbs[0].NextRun.After(hbs[0].LastFiredAt) {
		t.Fatalf("next_run muss nach last_fired liegen: %+v", hbs[0])
	}

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

// TestHeartbeatManualFire prüft den manuellen Trigger (POST …/heartbeats/
// {name}/fire): feuert sofort unabhängig vom Zeitplan, dedupliziert gegen die
// offene Aufgabe des letzten Laufs, feuert nach Abschluss erneut und lehnt
// gekillte Agenten ab.
func TestHeartbeatManualFire(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "puls-manuell", "Puls manuell", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	// 1h-Intervall: der Scheduler-Tick feuert im Testfenster nie selbst.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":      "# Puls manuell",
		"HEARTBEAT.md": "- alle: 1h titel: Wochenpuls aufgabe: Melde dich. [mock:result ok]",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	admin := login(t, s, "admin@test.local", "admin-passwort")
	fire := "/api/v1/agents/" + agent.ID.String() + "/heartbeats/Wochenpuls/fire"

	created := admin.expect(http.MethodPost, fire, nil, http.StatusOK)
	if created["origin"] != "heartbeat" || created["title"] != "Wochenpuls" {
		t.Fatalf("heartbeat-aufgabe erwartet, got %v", created)
	}

	// Solange die Aufgabe offen ist, wird nicht erneut gefeuert.
	admin.expect(http.MethodPost, fire, nil, http.StatusConflict)

	// Unbekannter Heartbeat-Name → 404.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/heartbeats/Nix/fire",
		nil, http.StatusNotFound)

	// Nach Abschluss der Aufgabe (Mock-Runtime) feuert der Trigger wieder.
	waitFor(t, "heartbeat-aufgabe abgeschlossen", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
		for _, task := range tasks {
			if task.Origin == "heartbeat" && task.State == "done" {
				return true
			}
		}
		return false
	})
	admin.expect(http.MethodPost, fire, nil, http.StatusOK)

	// Gekillter Agent feuert nicht (Kill-Switch geht vor Dedup).
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/kill", nil, http.StatusOK)
	conflict := admin.expect(http.MethodPost, fire, nil, http.StatusConflict)
	if msg, _ := conflict["error"].(string); !strings.Contains(msg, "gestoppt") {
		t.Fatalf("kill-fehler erwartet, got %v", conflict)
	}
}
