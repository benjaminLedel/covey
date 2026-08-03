package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

type auditEintrag struct {
	ActorEmail string `json:"actor_email"`
	ActorRole  string `json:"actor_role"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Status     int    `json:"status"`
}

func auditSpur(t *testing.T, c *apiClient) []auditEintrag {
	t.Helper()
	var out []auditEintrag
	resp := c.do(http.MethodGet, "/api/v1/audit", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("audit-spur lesen: HTTP %d", resp.StatusCode)
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// Covey tritt mit lückenloser Nachvollziehbarkeit an (spec/06) — hatte aber
// nur die eine Hälfte: Was AGENTEN tun, steht im Recording; was MENSCHEN an
// der Plattform tun, stand nirgends. Ohne diese Hälfte könnte jemand eine
// Guard-Rail löschen, den Agenten arbeiten lassen und die Regel danach wieder
// anlegen — im Recording stünde ein tadelloser Lauf.
func TestAuditSpurHaeltVerwaltungshandlungenFest(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Ein paar Handgriffe, wie sie ein Admin macht.
	created := admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "audit-agent", "display_name": "Audit", "runtime": "mock"}, http.StatusCreated)
	agentID := created["id"].(string)
	admin.expect(http.MethodPut, "/api/v1/secrets/anthropic_api_key",
		map[string]string{"value": "sk-ant-api03-streng-geheim"}, http.StatusOK)
	regel := admin.expect(http.MethodPost, "/api/v1/guardrails", map[string]any{
		"scope_level": "global", "rule_type": "deny_system", "pattern": "zammad", "enabled": true,
	}, http.StatusCreated)
	admin.expect(http.MethodDelete, "/api/v1/guardrails/"+regel["id"].(string), nil, http.StatusOK)
	admin.expect(http.MethodPost, "/api/v1/fleet/kill", nil, http.StatusOK)

	spur := auditSpur(t, admin)
	if len(spur) < 5 {
		t.Fatalf("erwartet mindestens 5 Einträge, got %d: %+v", len(spur), spur)
	}
	hat := func(method, teilPfad string) bool {
		for _, e := range spur {
			if e.Method == method && strings.Contains(e.Path, teilPfad) {
				return true
			}
		}
		return false
	}
	for _, f := range [][2]string{
		{http.MethodPost, "/agents"},
		{http.MethodPut, "/secrets/anthropic_api_key"},
		{http.MethodPost, "/guardrails"},
		{http.MethodDelete, "/guardrails/"},
		{http.MethodPost, "/fleet/kill"},
	} {
		if !hat(f[0], f[1]) {
			t.Errorf("%s %s fehlt in der Spur", f[0], f[1])
		}
	}

	// Der Handelnde steht dabei — eine Spur ohne Namen beantwortet nichts.
	for _, e := range spur {
		if e.ActorEmail != "admin@test.local" || e.ActorRole != "platform_admin" {
			t.Errorf("Eintrag ohne Handelnden: %+v", e)
		}
	}

	// DER Punkt: Der Secret-WERT darf nirgends in der Spur auftauchen. Eine
	// Audit-Spur, die man wegen ihres Inhalts nicht aufbewahren darf, ist keine.
	roh, _ := json.Marshal(spur)
	if strings.Contains(string(roh), "streng-geheim") {
		t.Fatal("der Secret-Wert steht in der Audit-Spur")
	}

	// Lesen wird NICHT protokolliert — sonst wäre jeder Seitenaufruf ein
	// Eintrag und die Spur unlesbar.
	vorher := len(auditSpur(t, admin))
	admin.do(http.MethodGet, "/api/v1/agents/"+agentID, nil).Body.Close()
	admin.do(http.MethodGet, "/api/v1/agents", nil).Body.Close()
	if nachher := len(auditSpur(t, admin)); nachher != vorher {
		t.Errorf("Lesen erzeugt Einträge: vorher %d, nachher %d", vorher, nachher)
	}

	// Auch der ABGEWIESENE Versuch gehört hinein — der Versuch, eine
	// Guard-Rail einer fremden Organisation zu löschen, ist die interessantere
	// Zeile.
	admin.do(http.MethodDelete, "/api/v1/guardrails/"+uuid.NewString(), nil).Body.Close()
	spur = auditSpur(t, admin)
	var gefunden bool
	for _, e := range spur {
		if e.Method == http.MethodDelete && strings.Contains(e.Path, "/guardrails/") && e.Status >= 400 {
			gefunden = true
		}
	}
	if !gefunden {
		t.Error("ein abgewiesener Versuch steht nicht in der Spur")
	}
}

// Die Spur ist org-gescopt, und lesen darf sie nur, wen sie angeht.
func TestAuditSpurRollenUndOrgGrenze(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "spur-agent", "display_name": "Spur", "runtime": "mock"}, http.StatusCreated)

	for email, rolle := range map[string]string{
		"pruefer@test.local":  "auditor",
		"sicher@test.local":   "security",
		"besitzer@test.local": "agent_owner",
		"kosten@test.local":   "controlling",
	} {
		hash, _ := identbuiltin.HashPassword("passwort-1234")
		if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
			VALUES ($1,$2,$3,$4,$5,$6)`, uuid.New(), s.orgID, email, rolle, hash, rolle); err != nil {
			t.Fatal(err)
		}
	}
	// Dürfen lesen: Auditor und Security (und der Admin oben).
	for _, email := range []string{"pruefer@test.local", "sicher@test.local"} {
		login(t, s, email, "passwort-1234").expect(http.MethodGet, "/api/v1/audit", nil, http.StatusOK)
	}
	// Dürfen nicht: Agent-Owner und Controlling — sie stehen selbst darin.
	for _, email := range []string{"besitzer@test.local", "kosten@test.local"} {
		login(t, s, email, "passwort-1234").expect(http.MethodGet, "/api/v1/audit", nil, http.StatusForbidden)
	}

	// Andere Organisation, andere Spur.
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]any{
		"name": "Spur-Nachbar", "admin_email": "spurnachbar@test.local",
		"admin_name": "Nachbar", "admin_password": "nachbar-passwort",
	}, http.StatusCreated)
	nachbar := login(t, s, "spurnachbar@test.local", "nachbar-passwort")
	for _, e := range auditSpur(t, nachbar) {
		if strings.Contains(e.Path, "spur-agent") || e.ActorEmail == "admin@test.local" {
			t.Errorf("die Nachbar-Organisation sieht fremde Handlungen: %+v", e)
		}
	}
}
