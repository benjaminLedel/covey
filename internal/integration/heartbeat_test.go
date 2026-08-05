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

// TestHeartbeatSignatureSurvivesAFailedRun covers the standstill that stopped a
// QA agent on covey.work: the signature the nur-wenn check fired on is
// remembered at DISPATCH. If that run then ends without a result, the state
// counts as seen anyway — and as long as nothing changes in the target system,
// no further heartbeat fires. The agent has consumed its own alarm clock without
// doing the work.
//
// The test drives the two halves the orchestrator connects: a heartbeat with a
// signature, and a task of that name that ends as failed. Afterwards the
// signature has to be released.
func TestHeartbeatSignatureSurvivesAFailedRun(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "puls-abbruch", "Puls Abbruch", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	// 1h: the scheduler must not fire alongside within the test window.
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":      "# Puls Abbruch",
		"HEARTBEAT.md": "- alle: 1h titel: Abnahme aufgabe: Nimm ab. [mock:fail Sub-Agent kaputt]",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	// The state the check fired on — as the dispatch would have noted it.
	if _, err := s.pool.Exec(ctx,
		"UPDATE agent_heartbeats SET last_work_sig=$2 WHERE agent_id=$1 AND name='Abnahme'",
		agent.ID, "mr40!1692@815"); err != nil {
		t.Fatal(err)
	}

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost,
		"/api/v1/agents/"+agent.ID.String()+"/heartbeats/Abnahme/fire", nil, http.StatusOK)

	waitFor(t, "run ends as failed", 20*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
		for _, task := range tasks {
			if task.Title == "Abnahme" && task.State == "failed" {
				return true
			}
		}
		return false
	})

	waitFor(t, "signature released", 10*time.Second, func() bool {
		var sig string
		if err := s.pool.QueryRow(ctx,
			"SELECT last_work_sig FROM agent_heartbeats WHERE agent_id=$1 AND name='Abnahme'",
			agent.ID).Scan(&sig); err != nil {
			return false
		}
		return sig == ""
	})
}

// The counterpart: a run that DID deliver a result keeps the suppression —
// otherwise the agent would be woken again for a state it deliberately left as
// it was, and would have to comment just to switch off its own alarm clock.
func TestHeartbeatSignatureStaysAfterASuccessfulRun(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	agent, err := s.registry.Create(ctx, s.orgID, "puls-fertig", "Puls fertig", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":      "# Puls fertig",
		"HEARTBEAT.md": "- alle: 1h titel: Abnahme aufgabe: Nimm ab. [mock:result abgenommen]",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	const sig = "mr40!1688@900"
	if _, err := s.pool.Exec(ctx,
		"UPDATE agent_heartbeats SET last_work_sig=$2 WHERE agent_id=$1 AND name='Abnahme'",
		agent.ID, sig); err != nil {
		t.Fatal(err)
	}

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost,
		"/api/v1/agents/"+agent.ID.String()+"/heartbeats/Abnahme/fire", nil, http.StatusOK)

	waitFor(t, "run done", 20*time.Second, func() bool {
		tasks, _ := s.backlog.ListByAgent(ctx, agent.ID, true)
		for _, task := range tasks {
			if task.Title == "Abnahme" && task.State == "done" {
				return true
			}
		}
		return false
	})

	var got string
	if err := s.pool.QueryRow(ctx,
		"SELECT last_work_sig FROM agent_heartbeats WHERE agent_id=$1 AND name='Abnahme'",
		agent.ID).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != sig {
		t.Fatalf("a completed run keeps the suppression, signature is now %q", got)
	}
}
