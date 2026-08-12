package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

// P1: die Anmeldung hängt am Konto, die Organisation an der Mitgliedschaft
// (FR-002). Was diese Tests festhalten, ist der Zustand, den es vorher nicht
// geben KONNTE — angemeldet, aber ohne Sitz — und die Mehrfach-Mitgliedschaft,
// die vorher die globale Unique-Regel auf humans.email verhindert hat.

// Ein Konto ohne Mitgliedschaft meldet sich an. Es sieht dann nichts, aber es
// fliegt auch nicht auf die Anmeldemaske zurück: die API sagt, was fehlt.
func TestKontoOhneOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	hash, _ := identbuiltin.HashPassword("hinreichend-lang")
	if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		VALUES ($1,'heimatlos@example.de',$2,'Ohne Organisation',now())`, uuid.New(), hash); err != nil {
		t.Fatal(err)
	}

	c := login(t, s, "heimatlos@example.de", "hinreichend-lang")

	// Wer bin ich: beantwortbar ohne Organisation, sonst wüsste die Oberfläche
	// nicht einmal, wen sie vor sich hat.
	me := c.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["Email"] != "heimatlos@example.de" {
		t.Errorf("/auth/me liefert %v", me["Email"])
	}
	if me["OrgID"] != uuid.Nil.String() {
		t.Errorf("OrgID = %v, erwartet die leere UUID", me["OrgID"])
	}

	// Alles Org-gebundene antwortet mit einer eigenen, maschinenlesbaren
	// Auskunft — nicht mit 403 (das läse die Oberfläche als "falsches
	// Passwort") und nicht mit 401 (das würfe die Sitzung weg).
	resp := c.do(http.MethodGet, "/api/v1/agents", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("Agentenliste ohne Organisation ergibt %d, erwartet 409", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["error"] != "no_organization" {
		t.Errorf("Fehlerkennung = %q, erwartet no_organization", body["error"])
	}
}

// Dieselbe Person in zwei Organisationen — genau das, was die globale
// Unique-Regel auf humans.email bisher ausgeschlossen hat.
func TestEinKontoZweiOrganisationen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// Zweite Organisation, und der Admin des Stacks bekommt dort einen Sitz.
	var zweiteOrg uuid.UUID = uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO organizations (id, name) VALUES ($1,'Zweite GmbH')`, zweiteOrg); err != nil {
		t.Fatal(err)
	}
	var accountID uuid.UUID
	if err := s.pool.QueryRow(ctx, `SELECT account_id FROM humans WHERE id=$1`, s.adminID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	zweiterSitz := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,'admin@test.local','Admin',' ','auditor')`,
		zweiterSitz, zweiteOrg, accountID); err != nil {
		t.Fatalf("zweiter Sitz für dasselbe Konto abgelehnt: %v", err)
	}

	// Ein Passwort, zwei Sitze — und die Anmeldung landet reproduzierbar auf
	// dem ältesten, nicht auf dem, den die Datenbank zufällig zuerst liefert.
	c := login(t, s, "admin@test.local", "admin-passwort")
	me := c.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != s.orgID.String() {
		t.Errorf("angemeldet in %v, erwartet die ältere Organisation %v", me["OrgID"], s.orgID)
	}
	if me["Role"] != "platform_admin" {
		t.Errorf("Rolle = %v — die Rolle hängt am Sitz, nicht am Konto", me["Role"])
	}

	// Und die Sitzungen zählen je Konto: eine Person, eine Liste, egal in
	// welcher Organisation sie gerade arbeitet.
	sitzungen := c.expectList(http.MethodGet, "/api/v1/auth/sessions", nil, http.StatusOK)
	if len(sitzungen) != 1 {
		t.Errorf("%d Sitzungen, erwartet 1", len(sitzungen))
	}
}

// Das Passwort gehört dem Konto: wer es ändert, wird überall abgemeldet — auch
// im Browser, der in einer anderen Organisation offen ist.
func TestPasswortwechselBeendetAlleSitzungen(t *testing.T) {
	s := newStack(t)

	ersterBrowser := login(t, s, "admin@test.local", "admin-passwort")
	zweiterBrowser := login(t, s, "admin@test.local", "admin-passwort")

	zweiterBrowser.expect(http.MethodPatch, "/api/v1/auth/me", map[string]string{
		"current_password": "admin-passwort", "password": "noch-viel-laenger",
	}, http.StatusOK)

	resp := ersterBrowser.do(http.MethodGet, "/api/v1/auth/me", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("alte Sitzung antwortet %d, erwartet 401", resp.StatusCode)
	}
	// Und das neue Passwort gilt — es steht am Konto, nicht am Sitz.
	login(t, s, "admin@test.local", "noch-viel-laenger")
}
