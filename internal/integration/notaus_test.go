package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"covey/internal/backlog"
)

// The emergency stop for the whole fleet (spec/06) had not a single test — of
// all things the lever you need exactly once and that has to work then. A kill
// switch that only looks like one is worse than none: you believe the situation
// is under control while the agents keep working.
func TestNotausDerFlotte(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("notaus-agent")

	status := func() bool {
		t.Helper()
		var out struct {
			Killed bool `json:"fleet_killed"`
		}
		resp := admin.do(http.MethodGet, "/api/v1/fleet", nil)
		json.NewDecoder(resp.Body).Decode(&out)
		resp.Body.Close()
		return out.Killed
	}

	if status() {
		t.Fatal("a fresh organization does not start with the emergency stop engaged")
	}

	// Trigger it.
	admin.expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusOK)
	if !status() {
		t.Fatal("after triggering it the status reports no emergency stop")
	}

	// The core: a new task must NOT wake the agent any more. It stays put
	// instead of being worked on.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Despite the emergency stop", "should stay put", "manual", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Wait generously: the dispatcher ticks every 300 ms in the test stack. If
	// the emergency stop did not hold, the task would long be in progress.
	time.Sleep(2 * time.Second)
	if st := s.taskState(task.ID); st != backlog.StateOpen {
		t.Errorf("the task is in state %q despite the emergency stop — it does not hold", st)
	}
	if st := s.agentStatus(agent.ID); st == "working" || st == "triage" {
		t.Errorf("the agent works despite the emergency stop (status %q)", st)
	}

	// The direct wake call through the API has to bounce off as well.
	resp := admin.do(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/wake", nil)
	resp.Body.Close()
	time.Sleep(time.Second)
	if st := s.taskState(task.ID); st != backlog.StateOpen {
		t.Errorf("the wake call bypassed the emergency stop: task in state %q", st)
	}

	// Release it — and the same agent works again.
	admin.expect(http.MethodPost, "/api/v1/fleet/resume", nil, http.StatusOK)
	if status() {
		t.Fatal("after releasing it the status still reports an emergency stop")
	}
	waitFor(t, "task is worked on after the release", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st != backlog.StateOpen
	})
}

// The emergency stop is a security decision: security and platform admin may
// trigger it, nobody else. And it applies only to one's own organization — an
// emergency stop that takes the neighbours down with it would be the worst case
// on a multi-tenant platform.
func TestNotausRollenUndOrgGrenze(t *testing.T) {
	s := newStack(t)

	for email, rolle := range map[string]string{
		"sec-notaus@test.local":  "security",
		"owner@test.local":       "agent_owner",
		"auditor2@test.local":    "auditor",
		"controlling@test.local": "controlling",
	} {
		s.mitglied(t, email, rolle, rolle, "passwort-1234")
	}

	// May: security (and further down the platform admin).
	login(t, s, "sec-notaus@test.local", "passwort-1234").
		expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusOK)
	login(t, s, "sec-notaus@test.local", "passwort-1234").
		expect(http.MethodPost, "/api/v1/fleet/resume", nil, http.StatusOK)

	// May not: all the remaining roles.
	for _, email := range []string{"owner@test.local", "auditor2@test.local", "controlling@test.local"} {
		c := login(t, s, email, "passwort-1234")
		c.expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusForbidden)
		c.expect(http.MethodPost, "/api/v1/fleet/resume", nil, http.StatusForbidden)
	}

	// Org boundary: one organization's emergency stop leaves the other alone.
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]any{
		"name": "Nachbar-AG", "admin_email": "nachbar@test.local",
		"admin_name": "Nachbar-Admin", "admin_password": "nachbar-passwort",
	}, http.StatusCreated)
	nachbar := login(t, s, "nachbar@test.local", "nachbar-passwort")

	admin.expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusOK)

	var nachbarStatus struct {
		Killed bool `json:"fleet_killed"`
	}
	resp := nachbar.do(http.MethodGet, "/api/v1/fleet", nil)
	json.NewDecoder(resp.Body).Decode(&nachbarStatus)
	resp.Body.Close()
	if nachbarStatus.Killed {
		t.Error("one organization's emergency stop also stopped the neighbouring organization")
	}
}
