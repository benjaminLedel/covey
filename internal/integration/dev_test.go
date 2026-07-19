package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestDevPluginSandboxComputer: Das dev-Plugin macht die Sandbox zum eigenen
// Rechner des Agenten — exec läuft im Daemon (Sandbox-Seite), der Broker
// gewährt ohne Secrets (NoCredentials), aber ACCESS.md und Aktivierung
// bleiben Pflicht (fail-closed).
func TestDevPluginSandboxComputer(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Aktivierung ist opt-in — wie bei jedem Zielsystem.
	admin.expect(http.MethodPatch, "/api/v1/targets/dev", map[string]any{"enabled": true}, http.StatusOK)

	agent, err := s.registry.Create(ctx, s.orgID, "coder", "Coding-Agent", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, agent.ID, map[string]string{
		"SOUL.md":   "# Coding-Agent",
		"ACCESS.md": "- system: dev scope: exec,processes",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}

	// Keinerlei Secrets nötig: exec + Supervisor-Zyklus laufen komplett lokal.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Code ausführen",
		`[mock:action dev/exec {"cmd":"echo eigener-rechner"}]`+
			` [mock:action dev/start {"name":"srv","cmd":"sleep 30"}]`+
			` [mock:action dev/list {}]`+
			` [mock:action dev/stop {"name":"srv"}]`+
			` [mock:result Sandbox-Rechner benutzt]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "dev-aufgabe done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// Der Action-Audit belegt jede Aktion mit ok=true.
	for _, action := range []string{"dev:exec", "dev:start", "dev:list", "dev:stop"} {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='action' AND payload->>'action'=$2 AND (payload->>'ok')::bool`,
			agent.ID, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("aktion %s fehlt im recording (count=%d)", action, n)
		}
	}

	// Ohne ACCESS.md-Zeile verweigert der Broker trotz NoCredentials.
	stranger, err := s.registry.Create(ctx, s.orgID, "fremd", "Ohne-Zugang", "mock", &s.adminID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.registry.SaveConfig(ctx, stranger.ID, map[string]string{
		"SOUL.md": "# Kein dev-Zugang",
	}, &s.adminID); err != nil {
		t.Fatal(err)
	}
	denied, err := s.backlog.Create(ctx, s.orgID, stranger.ID, "Verbotener Versuch",
		`[mock:action dev/exec {"cmd":"echo darf-nicht"}]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "aufgabe scheitert ohne access", 15*time.Second, func() bool {
		return s.taskState(denied.ID) == backlog.StateFailed
	})
	got, err := s.backlog.Get(ctx, denied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "ACCESS.md") {
		t.Fatalf("fehler soll den fehlenden zugang nennen, got %v", got.Error)
	}
}
