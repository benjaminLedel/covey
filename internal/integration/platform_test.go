package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"

	"covey/internal/accounts"
	identbuiltin "covey/internal/identity/builtin"
)

// P2: die Instanz-Ebene (FR-003, Befund F).
//
// Die Mandantenverwaltung hing bis hierher an platform_admin — einer Rolle,
// die JEDE Organisation an sich selbst vergibt. Auf einer Instanz mit mehreren
// Mandanten hiess das: der erste Selbstregistrierte kann die anderen löschen.
// Diese Tests halten fest, dass die Grenze jetzt woanders verläuft.

func TestMandantenverwaltungNurFuerSystemadmin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// Der Admin des Stacks ist platform_admin seiner Organisation — und sonst
	// nichts. Für ihn gibt es die Verwaltung der Instanz nicht.
	admin := login(t, s, "admin@test.local", "admin-passwort")
	for _, fall := range []struct {
		methode, pfad string
	}{
		{http.MethodGet, "/api/v1/platform/orgs"},
		{http.MethodPost, "/api/v1/platform/orgs"},
	} {
		resp := admin.do(fall.methode, fall.pfad, map[string]string{"name": "Fremde GmbH"})
		resp.Body.Close()
		// 404 statt 403: ob es diese Verwaltung überhaupt gibt, geht niemanden
		// etwas an, der nicht dazugehört.
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("%s %s als platform_admin ergibt %d, erwartet 404",
				fall.methode, fall.pfad, resp.StatusCode)
		}
	}

	// Die alten Adressen gibt es nicht mehr — sie waren für jede Organisation
	// erreichbar.
	resp := admin.do(http.MethodGet, "/api/v1/orgs", nil)
	resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		t.Error("/api/v1/orgs antwortet noch — die Route war der Befund")
	}

	// Erhoben wird die Ebene nur ausserhalb der HTTP-Schicht.
	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	// Die laufende Sitzung liest die Rolle bei jeder Anfrage neu — kein
	// Neuanmelden nötig, und ein Entzug wirkt ebenso sofort.
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

// Wer die Installation verwaltet, muss nicht Mitglied eines ihrer Mandanten
// sein. Die Middleware hängt deshalb an auth und nicht an rbac — sonst
// bekäme genau diese Person ein "no_organization" zu sehen.
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

	// Umgekehrt gilt die Ebene nicht nach unten: die Instanz zu verwalten ist
	// nicht dasselbe wie in einer Organisation zu arbeiten.
	resp := betreiber.do(http.MethodGet, "/api/v1/agents", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("Agentenliste ergibt %d, erwartet 409 no_organization", resp.StatusCode)
	}
}
