package integration

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// A key is minted for the seat the session works from, and since #262 one
// account can hold seats in several organisations. The listing therefore has to
// say which organisation each key reaches — the name is otherwise the only
// thing telling two keys apart, and a name is what somebody typed rather than
// what the key can do (#277).
func TestAPIKeyListNamesItsOrganisation(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	// A second organisation and a second seat for the SAME account — the
	// situation the switcher created.
	secondOrg := uuid.New()
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO organizations (id, name) VALUES ($1,'Zweite GmbH')`, secondOrg); err != nil {
		t.Fatal(err)
	}
	var accountID uuid.UUID
	if err := s.pool.QueryRow(ctx,
		`SELECT account_id FROM humans WHERE id=$1`, s.adminID).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		 VALUES ($1,$2,$3,'admin@test.local','Admin',' ','org_admin')`,
		uuid.New(), secondOrg, accountID); err != nil {
		t.Fatal(err)
	}

	// The session starts on the older seat, so this key belongs to it.
	c := login(t, s, "admin@test.local", "admin-passwort")
	createKey(t, c, "first seat", 0)

	// Switch, and mint a second one. Same account, same password, other
	// organisation — and that is the whole point of the listing below.
	c.expect(http.MethodPost, "/api/v1/auth/switch-org",
		map[string]any{"org_id": secondOrg.String()}, http.StatusOK)
	createKey(t, c, "second seat", 0)

	keys := c.expectList(http.MethodGet, "/api/v1/auth/api-keys", nil, http.StatusOK)
	if len(keys) != 2 {
		t.Fatalf("%d keys, expected 2 — the list spans the seats of the account", len(keys))
	}

	// One entry per organisation, each naming its own.
	byName := map[string]map[string]any{}
	for _, k := range keys {
		name, _ := k["name"].(string)
		byName[name] = k
	}
	for _, want := range []struct {
		key     string
		orgID   uuid.UUID
		orgName string
	}{
		{"first seat", s.orgID, ""},
		{"second seat", secondOrg, "Zweite GmbH"},
	} {
		k, ok := byName[want.key]
		if !ok {
			t.Fatalf("key %q missing from the list", want.key)
		}
		if k["org_id"] != want.orgID.String() {
			t.Errorf("%s: org_id = %v, expected %v", want.key, k["org_id"], want.orgID)
		}
		if want.orgName != "" && k["org_name"] != want.orgName {
			t.Errorf("%s: org_name = %v, expected %q", want.key, k["org_name"], want.orgName)
		}
		if name, _ := k["org_name"].(string); name == "" {
			t.Errorf("%s: org_name is empty — an id alone is not something an operator can read", want.key)
		}
	}

	// The answer that CREATES a key deliberately carries no organisation: it is
	// the one the operator is looking at, and the list behind it re-fetches.
	fresh := c.expect(http.MethodPost, "/api/v1/auth/api-keys",
		map[string]any{"name": "third", "expires_in_days": 0}, http.StatusCreated)
	if _, ok := fresh["org_name"]; ok {
		t.Errorf("the creating answer names an organisation — omitempty was meant to keep it out: %v", fresh)
	}
}
