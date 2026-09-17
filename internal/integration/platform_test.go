package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/accounts"
	identbuiltin "covey/internal/identity/builtin"
)

// P2: the instance level (FR-003, finding F).
//
// Tenant management hung on org_admin up to here — a role
// that EVERY organisation grants to itself. On an instance with several
// tenants that meant: the first self-registered one can delete the others.
// These tests record that the boundary now runs somewhere else.

func TestMandantenverwaltungNurFuerSystemadmin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// The admin of the stack is org_admin of its organisation — and otherwise
	// nothing. For him, managing the instance does not exist.
	admin := login(t, s, "admin@test.local", "admin-passwort")
	for _, fall := range []struct {
		methode, pfad string
	}{
		{http.MethodGet, "/api/v1/platform/orgs"},
		{http.MethodPost, "/api/v1/platform/orgs"},
	} {
		resp := admin.do(fall.methode, fall.pfad, map[string]string{"name": "Fremde GmbH"})
		resp.Body.Close()
		// 404 instead of 403: whether this management exists at all is nobody's
		// business who does not belong to it.
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s als org_admin ergibt %d, erwartet 404",
				fall.methode, fall.pfad, resp.StatusCode)
		}
	}

	// The old addresses no longer exist — they were reachable for every
	// organisation.
	resp := admin.do(http.MethodGet, "/api/v1/orgs", nil)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("/api/v1/orgs antwortet noch — die Route war der Befund")
	}

	// The level is raised only outside the HTTP layer.
	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	// The running session reads the role again on every request — no
	// re-login needed, and a revocation takes effect just as immediately.
	liste := admin.expectList(http.MethodGet, "/api/v1/platform/orgs", nil, http.StatusOK)
	if len(liste) != 1 {
		t.Errorf("%d Organisationen, erwartet 1", len(liste))
	}

	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleUser); err != nil {
		t.Fatal(err)
	}
	resp = admin.do(http.MethodGet, "/api/v1/platform/orgs", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("nach dem Entzug %d, erwartet 404", resp.StatusCode)
	}
}

// Whoever manages the installation does not have to be a member of one of its
// tenants. The middleware therefore hangs on auth and not on rbac — otherwise
// exactly this person would be shown a "no_organization".
func TestSystemadminOhneOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	hash, _ := identbuiltin.HashPassword("hinreichend-lang")
	if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at, platform_role)
		VALUES ($1,'betreiber@example.de',$2,'Betreiber',now(),'system_admin')`, uuid.New(), hash); err != nil {
		t.Fatal(err)
	}

	betreiber := login(t, s, "betreiber@example.de", "hinreichend-lang")

	liste := betreiber.expectList(http.MethodGet, "/api/v1/platform/orgs", nil, http.StatusOK)
	if len(liste) != 1 {
		t.Errorf("%d Organisationen, erwartet 1", len(liste))
	}

	// In reverse the level does not reach downwards: managing the instance is
	// not the same as working inside an organisation.
	resp := betreiber.do(http.MethodGet, "/api/v1/agents", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("Agentenliste ergibt %d, erwartet 409 no_organization", resp.StatusCode)
	}
}
