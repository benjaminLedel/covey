package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestHeartbeat checks the schedule trigger from spec/03: HEARTBEAT.md is
// materialized on save, does not fire immediately, then periodically creates a
// backlog task (origin=heartbeat) and is cleaned up when the entry is removed.
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

	// Baseline: a freshly saved heartbeat does not fire immediately.
	tasks, err := s.backlog.ListByAgent(ctx, agent.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("no immediate firing expected, backlog: %+v", tasks)
	}

	// Once the interval has elapsed, the tick creates the task.
	waitFor(t, "heartbeat task appears", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
		for _, task := range tasks {
			if task.Origin == "heartbeat" && task.Title == "Puls" {
				return true
			}
		}
		return false
	})

	// Monitoring view: schedule and next run are consistent.
	hbs, err := s.registry.Heartbeats(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(hbs) != 1 || hbs[0].Name != "Puls" {
		t.Fatalf("expected one heartbeat 'Puls': %+v", hbs)
	}
	if hbs[0].EverySeconds == nil || *hbs[0].EverySeconds != 1 {
		t.Fatalf("expected every_seconds=1: %+v", hbs[0])
	}
	if !hbs[0].NextRun.After(hbs[0].LastFiredAt) {
		t.Fatalf("next_run must lie after last_fired: %+v", hbs[0])
	}

	// A broken HEARTBEAT.md produces no new version.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":      "# Puls-Agent",
		"HEARTBEAT.md": "- alle: 1s aufgabe: titel fehlt",
	}, &s.adminID); err == nil {
		t.Fatal("SaveConfig with a broken HEARTBEAT.md must fail")
	}

	// Removing the entry: the materialization is cleaned up.
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
		t.Fatalf("agent_heartbeats not cleaned up: %d entries", n)
	}
}

// TestHeartbeatManualFire checks the manual trigger (POST …/heartbeats/
// {name}/fire): it fires immediately regardless of the schedule, deduplicates
// against the open task of the last run, fires again after completion and
// rejects killed agents.
func TestHeartbeatManualFire(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "puls-manuell", "Puls manuell", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	// A 1h interval: the scheduler tick never fires by itself within the test window.
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
		t.Fatalf("expected a heartbeat task, got %v", created)
	}

	// As long as the task is open, it does not fire again.
	admin.expect(http.MethodPost, fire, nil, http.StatusConflict)

	// An unknown heartbeat name → 404.
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/heartbeats/Nix/fire",
		nil, http.StatusNotFound)

	// After the task is completed (mock runtime), the trigger fires again.
	waitFor(t, "heartbeat task completed", 15*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
		for _, task := range tasks {
			if task.Origin == "heartbeat" && task.State == "done" {
				return true
			}
		}
		return false
	})
	admin.expect(http.MethodPost, fire, nil, http.StatusOK)

	// A killed agent does not fire (the kill switch comes before the dedup).
	admin.expect(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/kill", nil, http.StatusOK)
	conflict := admin.expect(http.MethodPost, fire, nil, http.StatusConflict)
	if msg, _ := conflict["error"].(string); !strings.Contains(msg, "stopped") {
		t.Fatalf("expected the kill error, got %v", conflict)
	}
}
