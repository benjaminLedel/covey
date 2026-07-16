package integration

import (
	"encoding/json"
	"net/http"
	"testing"
)

// TestUserAndOrgAdmin deckt die Benutzer- und Mandanten-Verwaltung ab:
// CRUD, RBAC (nur platform_admin), Last-Admin-Schutz, Session-Widerruf
// beim Passwort-Reset und der Schutz der eigenen Organisation.
func TestUserAndOrgAdmin(t *testing.T) {
	s := newStack(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Nutzer anlegen.
	created := admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "Owner@Test.local", "display_name": "Owner", "role": "agent_owner",
		"password": "owner-passwort",
	}, http.StatusCreated)
	ownerID := created["id"].(string)
	if created["email"] != "owner@test.local" {
		t.Fatalf("email muss normalisiert (lowercase) sein, got %v", created["email"])
	}

	// Validierung: kurzes Passwort, unbekannte Rolle, doppelte E-Mail.
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "x@test.local", "display_name": "X", "role": "agent_owner", "password": "kurz",
	}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "x@test.local", "display_name": "X", "role": "superuser", "password": "lang-genug",
	}, http.StatusBadRequest)
	admin.expect(http.MethodPost, "/api/v1/users", map[string]string{
		"email": "owner@test.local", "display_name": "Doppelt", "role": "auditor", "password": "lang-genug",
	}, http.StatusConflict)

	// Liste enthält Admin + Owner.
	resp := admin.do(http.MethodGet, "/api/v1/users", nil)
	var users []map[string]any
	json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if len(users) != 2 {
		t.Fatalf("erwartet 2 nutzer, got %d", len(users))
	}

	// RBAC: der neue agent_owner darf die Verwaltung nicht sehen.
	owner := login(t, s, "owner@test.local", "owner-passwort")
	owner.expect(http.MethodGet, "/api/v1/users", nil, http.StatusForbidden)
	owner.expect(http.MethodGet, "/api/v1/orgs", nil, http.StatusForbidden)

	// Rolle ändern wirkt sofort (auth liest die Rolle pro Request frisch).
	admin.expect(http.MethodPatch, "/api/v1/users/"+ownerID, map[string]string{"role": "platform_admin"}, http.StatusOK)
	owner.expect(http.MethodGet, "/api/v1/users", nil, http.StatusOK)

	// Last-Admin-Schutz: mit zwei Admins geht die Herabstufung, danach nicht mehr.
	admin.expect(http.MethodPatch, "/api/v1/users/"+ownerID, map[string]string{"role": "auditor"}, http.StatusOK)
	admin.expect(http.MethodPatch, "/api/v1/users/"+s.adminID.String(), map[string]string{"role": "auditor"}, http.StatusConflict)
	admin.expect(http.MethodDelete, "/api/v1/users/"+s.adminID.String(), nil, http.StatusConflict) // eigenes Konto

	// Passwort-Reset widerruft die Sessions des Nutzers.
	admin.expect(http.MethodPatch, "/api/v1/users/"+ownerID, map[string]string{"password": "neues-passwort"}, http.StatusOK)
	r := owner.do(http.MethodGet, "/api/v1/auth/me", nil)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("nach passwort-reset erwartet 401, got %d", r.StatusCode)
	}
	r.Body.Close()
	login(t, s, "owner@test.local", "neues-passwort")

	// Nutzer löschen.
	admin.expect(http.MethodDelete, "/api/v1/users/"+ownerID, nil, http.StatusOK)

	// --- Mandanten ---

	// Neue Organisation braucht einen initialen Admin.
	admin.expect(http.MethodPost, "/api/v1/orgs", map[string]string{"name": "Ohne Admin"}, http.StatusBadRequest)
	created = admin.expect(http.MethodPost, "/api/v1/orgs", map[string]string{
		"name": "Zweite Org", "admin_email": "chef@zweite.local", "admin_name": "Chef",
		"admin_password": "chef-passwort",
	}, http.StatusCreated)
	orgID := created["id"].(string)

	// Der neue Org-Admin kann sich anmelden und sieht nur die eigene (leere) Nutzerliste.
	chef := login(t, s, "chef@zweite.local", "chef-passwort")
	resp = chef.do(http.MethodGet, "/api/v1/users", nil)
	users = nil
	json.NewDecoder(resp.Body).Decode(&users)
	resp.Body.Close()
	if len(users) != 1 || users[0]["email"] != "chef@zweite.local" {
		t.Fatalf("org-scoping verletzt: %v", users)
	}

	// Umbenennen, eigene Org nicht löschbar, fremde schon.
	admin.expect(http.MethodPatch, "/api/v1/orgs/"+orgID, map[string]string{"name": "Zweite Org GmbH"}, http.StatusOK)
	admin.expect(http.MethodDelete, "/api/v1/orgs/"+s.orgID.String(), nil, http.StatusConflict)
	admin.expect(http.MethodDelete, "/api/v1/orgs/"+orgID, nil, http.StatusOK)

	// Mit der Organisation verschwinden ihre Nutzer (Cascade) samt Sessions.
	r = chef.do(http.MethodGet, "/api/v1/auth/me", nil)
	if r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("nach org-löschung erwartet 401, got %d", r.StatusCode)
	}
	r.Body.Close()
}
