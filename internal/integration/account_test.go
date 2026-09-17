package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/google/uuid"

	identbuiltin "covey/internal/identity/builtin"
)

// P1: the login hangs on the account, the organisation on the membership
// (FR-002). What these tests pin down is the state that could not exist
// BEFORE — signed in, but without a seat — and the multiple membership,
// which the global unique rule on humans.email had prevented until then.

// An account without a membership signs in. It then sees nothing, but it is
// also not thrown back onto the login mask: the API says what is missing.
func TestKontoOhneOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	hash, _ := identbuiltin.HashPassword("hinreichend-lang")
	if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		VALUES ($1,'heimatlos@example.de',$2,'Ohne Organisation',now())`, uuid.New(), hash); err != nil {
		t.Fatal(err)
	}

	c := login(t, s, "heimatlos@example.de", "hinreichend-lang")

	// Who am I: answerable without an organisation, otherwise the interface
	// would not even know who it has in front of it.
	me := c.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["Email"] != "heimatlos@example.de" {
		t.Errorf("/auth/me liefert %v", me["Email"])
	}
	if me["OrgID"] != uuid.Nil.String() {
		t.Errorf("OrgID = %v, erwartet die leere UUID", me["OrgID"])
	}

	// Everything tied to an organisation answers with its own, machine-readable
	// information — not with 403 (the interface would read that as "wrong
	// password") and not with 401 (that would throw the session away).
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

// The same person in two organisations — exactly what the global
// unique rule on humans.email had ruled out until now.
func TestEinKontoZweiOrganisationen(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// Second organisation, and the admin of the stack gets a seat there.
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

	// One password, two seats — and the login lands reproducibly on
	// the oldest one, not on the one the database happens to return first.
	c := login(t, s, "admin@test.local", "admin-passwort")
	me := c.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK)
	if me["OrgID"] != s.orgID.String() {
		t.Errorf("angemeldet in %v, erwartet die ältere Organisation %v", me["OrgID"], s.orgID)
	}
	if me["Role"] != "org_admin" {
		t.Errorf("Rolle = %v — die Rolle hängt am Sitz, nicht am Konto", me["Role"])
	}

	// And the sessions are counted per account: one person, one list, no matter
	// in which organisation they are currently working.
	sitzungen := c.expectList(http.MethodGet, "/api/v1/auth/sessions", nil, http.StatusOK)
	if len(sitzungen) != 1 {
		t.Errorf("%d Sitzungen, erwartet 1", len(sitzungen))
	}
}

// The password belongs to the account: whoever changes it is signed out
// everywhere — also in the browser that is open in another organisation.
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
	// And the new password is valid — it stands on the account, not on the seat.
	login(t, s, "admin@test.local", "noch-viel-laenger")
}
