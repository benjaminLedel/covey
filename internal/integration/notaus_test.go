package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	identbuiltin "covey/internal/identity/builtin"
)

// Der Notaus für die ganze Flotte (spec/06) hatte keinen einzigen Test —
// ausgerechnet der Griff, den man genau einmal braucht und der dann
// funktionieren muss. Ein Kill-Switch, der nur so aussieht, ist schlimmer als
// keiner: Man hält die Lage für beherrscht, während die Agenten weiterarbeiten.
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
		t.Fatal("frische Organisation startet nicht im Notaus")
	}

	// Auslösen.
	admin.expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusOK)
	if !status() {
		t.Fatal("nach dem Auslösen meldet der Status keinen Notaus")
	}

	// Der Kern: Eine neue Aufgabe darf den Agenten NICHT mehr wecken. Sie
	// bleibt liegen, statt bearbeitet zu werden.
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Trotz Notaus", "sollte liegenbleiben", "manual", 1)
	if err != nil {
		t.Fatal(err)
	}
	// Großzügig warten: Der Dispatcher tickt alle 300 ms im Test-Stack. Würde
	// der Notaus nicht greifen, wäre die Aufgabe längst in Arbeit.
	time.Sleep(2 * time.Second)
	if st := s.taskState(task.ID); st != backlog.StateOpen {
		t.Errorf("Aufgabe ist trotz Notaus im Zustand %q — der Notaus hält nicht", st)
	}
	if st := s.agentStatus(agent.ID); st == "working" || st == "triage" {
		t.Errorf("Agent arbeitet trotz Notaus (Status %q)", st)
	}

	// Auch der direkte Weckruf über die API muss abprallen.
	resp := admin.do(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/wake", nil)
	resp.Body.Close()
	time.Sleep(time.Second)
	if st := s.taskState(task.ID); st != backlog.StateOpen {
		t.Errorf("Weckruf hat den Notaus umgangen: Aufgabe im Zustand %q", st)
	}

	// Zurücknehmen — und derselbe Agent arbeitet wieder.
	admin.expect(http.MethodPost, "/api/v1/fleet/resume", nil, http.StatusOK)
	if status() {
		t.Fatal("nach dem Zurücknehmen meldet der Status weiter Notaus")
	}
	waitFor(t, "Aufgabe wird nach dem Zurücknehmen bearbeitet", 20*time.Second, func() bool {
		st := s.taskState(task.ID)
		return st != backlog.StateOpen
	})
}

// Der Notaus ist eine Sicherheitsentscheidung: Security und Plattform-Admin
// dürfen ihn auslösen, sonst niemand. Und er gilt nur für die eigene
// Organisation — ein Notaus, der die Nachbarn mitnimmt, wäre in einer
// Mandantenplattform der GAU.
func TestNotausRollenUndOrgGrenze(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	for email, rolle := range map[string]string{
		"sec-notaus@test.local":  "security",
		"owner@test.local":       "agent_owner",
		"auditor2@test.local":    "auditor",
		"controlling@test.local": "controlling",
	} {
		hash, _ := identbuiltin.HashPassword("passwort-1234")
		if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
			VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), s.orgID, email, rolle, hash, rolle); err != nil {
			t.Fatal(err)
		}
	}

	// Darf: Security (und weiter unten der Plattform-Admin).
	login(t, s, "sec-notaus@test.local", "passwort-1234").
		expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusOK)
	login(t, s, "sec-notaus@test.local", "passwort-1234").
		expect(http.MethodPost, "/api/v1/fleet/resume", nil, http.StatusOK)

	// Darf nicht: alle übrigen Rollen.
	for _, email := range []string{"owner@test.local", "auditor2@test.local", "controlling@test.local"} {
		c := login(t, s, email, "passwort-1234")
		c.expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusForbidden)
		c.expect(http.MethodPost, "/api/v1/fleet/resume", nil, http.StatusForbidden)
	}

	// Org-Grenze: Der Notaus der einen Organisation lässt die andere in Ruhe.
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
		t.Error("der Notaus einer Organisation hat die Nachbar-Organisation mitgestoppt")
	}
}
