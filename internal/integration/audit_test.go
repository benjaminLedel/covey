package integration

import (
	"covey/internal/accounts"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
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
		t.Fatalf("reading the audit trail: HTTP %d", resp.StatusCode)
	}
	json.NewDecoder(resp.Body).Decode(&out)
	return out
}

// covey promises gapless traceability (spec/06) — but only had one half of it:
// what AGENTS do is in the recording; what HUMANS do to the platform was
// nowhere. Without that half someone could delete a guard rail, let the agent
// work and create the rule again afterwards — and the recording would show a
// flawless run.
func TestAuditSpurHaeltVerwaltungshandlungenFest(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// A few handgrips of the kind an admin performs.
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
		t.Fatalf("expected at least 5 entries, got %d: %+v", len(spur), spur)
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
			t.Errorf("%s %s is missing from the trail", f[0], f[1])
		}
	}

	// The actor is recorded along with it — a trail without names answers nothing.
	for _, e := range spur {
		if e.ActorEmail != "admin@test.local" || e.ActorRole != "org_admin" {
			t.Errorf("entry without an actor: %+v", e)
		}
	}

	// THE point: the secret VALUE must appear nowhere in the trail. An audit
	// trail one is not allowed to retain because of its content is none.
	roh, _ := json.Marshal(spur)
	if strings.Contains(string(roh), "streng-geheim") {
		t.Fatal("the secret value is in the audit trail")
	}

	// Reads are NOT logged — otherwise every page view would be an entry and
	// the trail unreadable.
	vorher := len(auditSpur(t, admin))
	admin.do(http.MethodGet, "/api/v1/agents/"+agentID, nil).Body.Close()
	admin.do(http.MethodGet, "/api/v1/agents", nil).Body.Close()
	if nachher := len(auditSpur(t, admin)); nachher != vorher {
		t.Errorf("reading produces entries: before %d, after %d", vorher, nachher)
	}

	// The REJECTED attempt belongs in there too — the attempt to delete a guard
	// rail of a foreign organization is the more interesting line.
	admin.do(http.MethodDelete, "/api/v1/guardrails/"+uuid.NewString(), nil).Body.Close()
	spur = auditSpur(t, admin)
	var gefunden bool
	for _, e := range spur {
		if e.Method == http.MethodDelete && strings.Contains(e.Path, "/guardrails/") && e.Status >= 400 {
			gefunden = true
		}
	}
	if !gefunden {
		t.Error("a rejected attempt is not in the trail")
	}
}

// The trail is org-scoped, and only those it concerns may read it.
func TestAuditSpurRollenUndOrgGrenze(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "spur-agent", "display_name": "Spur", "runtime": "mock"}, http.StatusCreated)

	for email, rolle := range map[string]string{
		"pruefer@test.local":  "auditor",
		"sicher@test.local":   "security",
		"besitzer@test.local": "agent_owner",
		"kosten@test.local":   "controlling",
	} {
		s.mitglied(t, email, rolle, rolle, "passwort-1234")
	}
	// May read: auditor and security (and the admin above).
	for _, email := range []string{"pruefer@test.local", "sicher@test.local"} {
		login(t, s, email, "passwort-1234").expect(http.MethodGet, "/api/v1/audit", nil, http.StatusOK)
	}
	// May not: agent owner and controlling — they appear in it themselves.
	for _, email := range []string{"besitzer@test.local", "kosten@test.local"} {
		login(t, s, email, "passwort-1234").expect(http.MethodGet, "/api/v1/audit", nil, http.StatusForbidden)
	}

	// Another organization, another trail. Angelegt wird sie auf der
	// Instanz-Ebene — eine Organisation legt keine zweite an (FR-003, F).
	if err := accounts.New(s.pool).SetPlatformRole(t.Context(), "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]any{
		"name": "Spur-Nachbar", "admin_email": "spurnachbar@test.local",
		"admin_name": "Nachbar", "admin_password": "nachbar-passwort",
	}, http.StatusCreated)
	nachbar := login(t, s, "spurnachbar@test.local", "nachbar-passwort")
	for _, e := range auditSpur(t, nachbar) {
		if strings.Contains(e.Path, "spur-agent") || e.ActorEmail == "admin@test.local" {
			t.Errorf("the neighbouring organization sees foreign actions: %+v", e)
		}
	}
}
