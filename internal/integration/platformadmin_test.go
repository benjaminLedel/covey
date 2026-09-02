package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/accounts"
	"covey/internal/settings"
)

// Das Plattform-Panel: Konten, Schalter der Installation, Wartelisten-Codes.
//
// Alle drei hingen bisher an der Kommandozeile oder an gar nichts — die Stores
// gab es seit FR-002 P3/P4, eine Adresse dafür nicht. Diese Tests halten fest,
// was sie jetzt beantworten und wem sie überhaupt antworten.

func TestPlattformVerwaltungNurFuerSystemadmin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Als org_admin: es gibt diese Verwaltung nicht.
	for _, pfad := range []string{
		"/api/v1/platform/accounts",
		"/api/v1/platform/settings",
		"/api/v1/platform/waitlist-codes",
	} {
		resp := admin.do(http.MethodGet, pfad, nil)
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("GET %s als org_admin ergibt %d, erwartet 404", pfad, resp.StatusCode)
		}
	}

	if err := accounts.New(s.pool).SetPlatformRole(ctx, "admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}

	// Konten: eines, mit genau einem Sitz — und die Rolle daran ist die des
	// Sitzes, nicht die der Instanz.
	konten := admin.expectList(http.MethodGet, "/api/v1/platform/accounts", nil, http.StatusOK)
	if len(konten) != 1 {
		t.Fatalf("%d Konten, erwartet 1", len(konten))
	}
	sitze, _ := konten[0]["seats"].([]any)
	if len(sitze) != 1 {
		t.Fatalf("%d Sitze, erwartet 1", len(sitze))
	}
	sitz := sitze[0].(map[string]any)
	if sitz["role"] != "org_admin" {
		t.Errorf("Rolle am Sitz = %v, erwartet org_admin", sitz["role"])
	}
	if konten[0]["platform_role"] != accounts.RoleSystemAdmin {
		t.Errorf("platform_role = %v, erwartet system_admin", konten[0]["platform_role"])
	}
	// Die Anmeldung hat last_login_at gesetzt — die Spalte gab es seit 0058,
	// geschrieben hat sie niemand.
	if konten[0]["last_login_at"] == nil {
		t.Error("last_login_at ist leer, obwohl dieses Konto sich gerade angemeldet hat")
	}

	// Die eigene Ebene abzugeben geht nicht, solange niemand sonst sie hat:
	// der Weg zurück führte über die Shell des Servers.
	id := konten[0]["id"].(string)
	admin.expect(http.MethodPatch, "/api/v1/platform/accounts/"+id,
		map[string]string{"platform_role": "user"}, http.StatusConflict)
}

func TestSystemEinstellungenUeberDieApi(t *testing.T) {
	s := newStack(t)
	if err := accounts.New(s.pool).SetPlatformRole(context.Background(),
		"admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")

	liste := admin.expectList(http.MethodGet, "/api/v1/platform/settings", nil, http.StatusOK)
	if len(liste) != len(settings.Keys()) {
		t.Fatalf("%d Schalter, erwartet %d", len(liste), len(settings.Keys()))
	}
	for _, e := range liste {
		if e["key"] == settings.SignupMode && e["value"] != settings.ModeOff {
			t.Errorf("signup.mode = %v — eine Installation, die nichts weiß, nimmt niemanden auf", e["value"])
		}
	}

	// Gültig, ungültig, unbekannt: drei Antworten, nicht eine.
	//
	// Der gültige Wert braucht seit #167 einen nachgewiesenen Mailversand —
	// eine Instanz ohne ihn nähme Konten auf, deren Bestätigungslink nie
	// abginge. Was der Nachweis wert ist, prüft mail_test.go.
	s.proveMailer(t)
	admin.expect(http.MethodPut, "/api/v1/platform/settings/"+settings.SignupMode,
		map[string]string{"value": settings.ModeWaitlist}, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/platform/settings/"+settings.SignupMode,
		map[string]string{"value": "vielleicht"}, http.StatusBadRequest)
	admin.expect(http.MethodPut, "/api/v1/platform/settings/signup.mod",
		map[string]string{"value": "off"}, http.StatusNotFound)

	// Und der Schalter wirkt: die öffentliche Seite gibt jetzt Auskunft.
	oeffentlich := admin.expect(http.MethodGet, "/api/v1/public/signup-state", nil, http.StatusOK)
	if oeffentlich["mode"] != settings.ModeWaitlist {
		t.Errorf("öffentlicher Zustand = %v, erwartet waitlist", oeffentlich["mode"])
	}
}

func TestWartelistenCodesUeberDieApi(t *testing.T) {
	s := newStack(t)
	if err := accounts.New(s.pool).SetPlatformRole(context.Background(),
		"admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// Der Klartext existiert genau einmal — in dieser Antwort.
	erzeugt := admin.expect(http.MethodPost, "/api/v1/platform/waitlist-codes",
		map[string]any{"label": "Konferenz X", "max_uses": 3}, http.StatusCreated)
	if erzeugt["code"] == nil || erzeugt["code"] == "" {
		t.Fatal("kein Code in der Antwort — er ist danach nicht mehr rekonstruierbar")
	}

	liste := admin.expectList(http.MethodGet, "/api/v1/platform/waitlist-codes", nil, http.StatusOK)
	if len(liste) != 1 {
		t.Fatalf("%d Codes, erwartet 1", len(liste))
	}
	if liste[0]["label"] != "Konferenz X" {
		t.Errorf("Label = %v", liste[0]["label"])
	}
	// Die Liste trägt den Hash, nicht den Code.
	hash, _ := liste[0]["hash"].(string)
	if hash == "" || hash == erzeugt["code"] {
		t.Fatalf("Liste zeigt %q — das darf nicht der Klartext sein", hash)
	}

	// Zurückziehen löscht nicht, es schließt: wer ihn eingelöst hat, bleibt
	// sichtbar.
	admin.expect(http.MethodDelete, "/api/v1/platform/waitlist-codes/"+hash[:16], nil, http.StatusOK)
	liste = admin.expectList(http.MethodGet, "/api/v1/platform/waitlist-codes", nil, http.StatusOK)
	if len(liste) != 1 || liste[0]["revoked_at"] == nil {
		t.Errorf("Code nach dem Zurückziehen: %v", liste[0])
	}
	admin.expect(http.MethodDelete, "/api/v1/platform/waitlist-codes/"+hash[:16], nil, http.StatusNotFound)
}
