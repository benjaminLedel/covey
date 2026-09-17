package integration

import (
	"context"
	"net/http"
	"testing"

	"covey/internal/accounts"
	"covey/internal/settings"
)

// The platform panel: accounts, installation settings, waitlist codes.
//
// All three hung on the command line or on nothing at all — the stores have
// existed since FR-002 P3/P4, an address for them has not. These tests hold what
// they answer now, and who gets an answer at all.

func TestPlattformVerwaltungNurFuerSystemadmin(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	admin := login(t, s, "admin@test.local", "admin-passwort")

	// As org_admin: this admin API does not exist.
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

	// Accounts: one, with exactly one seat — and the role on it is the seat's,
	// not the instance's.
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
	// The login set last_login_at — the column existed since 0058, nobody had
	// written to it.
	if konten[0]["last_login_at"] == nil {
		t.Error("last_login_at ist leer, obwohl dieses Konto sich gerade angemeldet hat")
	}

	// Giving up one's own tier does not work while nobody else holds it: the
	// way back would run over the server's shell.
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

	// Valid, invalid, unknown: three answers, not one.
	//
	// Since #167 the valid value needs proven mail delivery — an instance
	// without it would admit accounts whose confirmation link never
	// went out. What the proof is worth, mail_test.go checks.
	s.proveMailer(t)
	admin.expect(http.MethodPut, "/api/v1/platform/settings/"+settings.SignupMode,
		map[string]string{"value": settings.ModeWaitlist}, http.StatusOK)
	admin.expect(http.MethodPut, "/api/v1/platform/settings/"+settings.SignupMode,
		map[string]string{"value": "vielleicht"}, http.StatusBadRequest)
	admin.expect(http.MethodPut, "/api/v1/platform/settings/signup.mod",
		map[string]string{"value": "off"}, http.StatusNotFound)

	// And the flag takes effect: the public page now reports it.
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

	// The plaintext exists exactly once — in this response.
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
	// The list carries the hash, not the code.
	hash, _ := liste[0]["hash"].(string)
	if hash == "" || hash == erzeugt["code"] {
		t.Fatalf("Liste zeigt %q — das darf nicht der Klartext sein", hash)
	}

	// Revoking does not delete, it closes: whoever redeemed it stays
	// visible.
	admin.expect(http.MethodDelete, "/api/v1/platform/waitlist-codes/"+hash[:16], nil, http.StatusOK)
	liste = admin.expectList(http.MethodGet, "/api/v1/platform/waitlist-codes", nil, http.StatusOK)
	if len(liste) != 1 || liste[0]["revoked_at"] == nil {
		t.Errorf("Code nach dem Zurückziehen: %v", liste[0])
	}
	admin.expect(http.MethodDelete, "/api/v1/platform/waitlist-codes/"+hash[:16], nil, http.StatusNotFound)
}
