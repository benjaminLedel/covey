package integration

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

// TestDevPluginSandboxComputer: the dev plugin turns the sandbox into the
// agent's own computer — exec runs in the daemon (sandbox side), the broker
// grants without secrets (NoCredentials), but ACCESS.md and activation remain
// mandatory (fail-closed).
func TestDevPluginSandboxComputer(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Activation is opt-in — as with every target system.
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

	// No secrets needed at all: exec + supervisor cycle run entirely locally.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Code ausführen",
		`[mock:action dev/exec {"cmd":"echo eigener-rechner"}]`+
			` [mock:action dev/start {"name":"srv","cmd":"sleep 30"}]`+
			` [mock:action dev/list {}]`+
			` [mock:action dev/stop {"name":"srv"}]`+
			` [mock:result Sandbox-Rechner benutzt]`, "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "dev task done", 15*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})

	// The action audit shows every action with ok=true.
	for _, action := range []string{"dev:exec", "dev:start", "dev:list", "dev:stop"} {
		var n int
		if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM recording_events
			WHERE agent_id=$1 AND kind='action' AND payload->>'action'=$2 AND (payload->>'ok')::bool`,
			agent.ID, action).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Fatalf("action %s missing from the recording (count=%d)", action, n)
		}
	}

	// Without an ACCESS.md line the broker refuses despite NoCredentials.
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
	waitFor(t, "task fails without access", 15*time.Second, func() bool {
		return s.taskState(denied.ID) == backlog.StateFailed
	})
	got, err := s.backlog.Get(ctx, denied.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Error == nil || !strings.Contains(*got.Error, "ACCESS.md") {
		t.Fatalf("the error should name the missing access, got %v", got.Error)
	}
}
