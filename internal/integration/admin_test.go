package integration

import (
	"covey/internal/accounts"
	"encoding/json"
	"net/http"
	"testing"
)

// TestUserAndOrgAdmin covers user and tenant administration: CRUD, RBAC
// (platform_admin only), last-admin protection, session revocation on password
// reset and the protection of one's own organization.
func TestUserAndOrgAdmin(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Create a user.
	created := admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "Owner@Test.local", "display_name": "Owner", "role": "agent_owner",
		"password": "owner-passwort",
	}, http.StatusCreated)
	ownerID := created["id"].(string)
	if created["email"] != "owner@test.local" {
		t.Fatalf("email must be normalized (lowercase), got %v", created["email"])
	}

	// Validation: short password, unknown role, duplicate e-mail.
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "x@test.local", "display_name": "X", "role": "agent_owner", "password": "kurz",
	}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "x@test.local", "display_name": "X", "role": "superuser", "password": "lang-genug",
	}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "owner@test.local", "display_name": "Doppelt", "role": "auditor", "password": "lang-genug",
	}, http.StatusConflict)

	// The list contains admin + owner.
	resp := admin.do(http.MethodGet, "/api/v1/users", nil)
	var users []map[string]any
	json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if len(users) != 2 {
		t.Fatalf("expected 2 users, got %d", len(users))
	}

	// RBAC: the new agent_owner must not see the administration.
	owner := login(t, s, "owner@test.local", "owner-passwort")
	owner.expect(http.MethodGet, "/api/v1/users", nil, http.StatusForbidden)
	// Die Mandantenverwaltung ist keine Frage der Organisations-Rolle mehr:
	// sie liegt auf der Instanz-Ebene und antwortet allen anderen mit 404
	// (FR-003, Befund F). Auch dem platform_admin — siehe platform_test.go.
	owner.expect(http.MethodGet, "/api/v1/platform/orgs", nil, http.StatusNotFound)

	// A role change takes effect immediately (auth reads the role fresh per request).
	admin.expect(http.MethodPatch, "/api/v1/users/"+ownerID, map[string]string{"role": "platform_admin"}, http.StatusOK)
	owner.expect(http.MethodGet, "/api/v1/users", nil, http.StatusOK)

	// Last-admin protection: with two admins the demotion works, afterwards no longer.
	admin.expect(http.MethodPatch, "/api/v1/users/"+ownerID, map[string]string{"role": "auditor"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/users/"+s.adminID.String(), map[string]string{"role": "auditor"}, http.StatusConflict)
	admin.expect(http.MethodDelete, "/api/v1/users/"+s.adminID.String(), nil, http.StatusConflict) // own account

	// A password reset revokes the user's sessions.
	admin.expect(http.MethodPatch, "/api/v1/users/"+ownerID, map[string]string{"password": "neues-passwort"}, http.StatusOK)
	r := owner.do(http.MethodGet, "/api/v1/auth/me", nil)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after password reset, got %d", r.StatusCode)
	}
	r.Body.Close()
	login(t, s, "owner@test.local", "neues-passwort")

	// Delete the user.
	admin.expect(http.MethodDelete, "/api/v1/users/"+ownerID, nil, http.StatusOK)

	// --- Tenants ---

	// Ab hier verwaltet der Admin die INSTANZ, nicht mehr nur seine
	// Organisation. Die Ebene wird ausserhalb der HTTP-Schicht vergeben —
	// ein Endpunkt dafuer waere aus einer Organisation heraus erreichbar.
	if err := accounts.New(s.pool).SetPlatformRole(t.Context(), "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}

	// A new organization needs an initial admin.
	admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]string{"name": "Ohne Admin"}, http.StatusBadRequest)
	created = admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]string{
		"name": "Zweite Org", "admin_email": "chef@second.local", "admin_name": "Chef",
		"admin_password": "chef-passwort",
	}, http.StatusCreated)
	orgID := created["id"].(string)

	// The new org admin can log in and only sees its own (empty) user list.
	chef := login(t, s, "chef@second.local", "chef-passwort")
	resp = chef.do(http.MethodGet, "/api/v1/users", nil)
	users = nil
	json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if len(users) != 1 || users[0]["email"] != "chef@second.local" {
		t.Fatalf("org scoping violated: %v", users)
	}

	// Rename, own org not deletable, a foreign one is.
	admin.expect(http.MethodPatch, "/api/v1/platform/orgs/"+orgID, map[string]string{"name": "Zweite Org GmbH"}, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/platform/orgs/"+s.orgID.String(), nil, http.StatusConflict)
	admin.expect(http.MethodDelete, "/api/v1/platform/orgs/"+orgID, nil, http.StatusOK)

	// Deleting the organization takes its users (cascade) and their sessions with it.
	r = chef.do(http.MethodGet, "/api/v1/auth/me", nil)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 after org deletion, got %d", r.StatusCode)
	}
	r.Body.Close()
}
