package integration

import (
	"context"
	"net/http"
	"testing"
)

// A person who created an agent template can be deleted; the template stays
// with the organisation (#731).
func TestDeletePersonWhoCreatedATemplate(t *testing.T) {
	s := newStack(t)
	seat := s.mitglied(t, "tina@test.local", "Tina", "agent_owner", "tina-passwort")
	tina := login(t, s, "tina@test.local", "tina-passwort")
	created := tina.expect(http.MethodPost, "/api/v1/agents",
		map[string]string{"slug": "tina-agent", "display_name": "Tina's Agent", "runtime": "mock"}, http.StatusCreated)
	tpl := tina.expect(http.MethodPost, "/api/v1/templates", map[string]string{
		"name": "Tina's template", "from_agent_id": created["id"].(string)}, http.StatusCreated)

	admin := login(t, s, "admin@test.local", "admin-passwort")
	admin.expect(http.MethodDelete, "/api/v1/users/"+seat.String(), nil, http.StatusOK)

	var creator *string
	if err := s.pool.QueryRow(context.Background(), `SELECT created_by::text FROM agent_templates WHERE id=$1`,
		tpl["id"]).Scan(&creator); err != nil {
		t.Fatalf("the template went with its creator: %v", err)
	}
	if creator != nil {
		t.Errorf("the template still names its deleted creator %s", *creator)
	}
}

// A system admin deletes a whole account with its seats (#732), within the
// lockout rules.
func TestPlatformDeletesAccount(t *testing.T) {
	s := newStack(t)
	s.alsSystemadmin(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")
	second := admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]string{
		"name": "Second Org", "admin_email": "chef@second.local", "admin_name": "Chef", "admin_password": "chef-passwort",
	}, http.StatusCreated)["id"].(string)
	s.mitglied(t, "anna@test.local", "Anna", "agent_owner", "anna-passwort")

	ids := map[string]string{}
	for _, a := range admin.expectList(http.MethodGet, "/api/v1/platform/accounts", nil, http.StatusOK) {
		ids[a["email"].(string)] = a["id"].(string)
	}
	anna, chef, self := ids["anna@test.local"], ids["chef@second.local"], ids["admin@test.local"]
	admin.expect(http.MethodPost, "/api/v1/platform/orgs/"+second+"/members",
		map[string]string{"account_id": anna, "role": "auditor"}, http.StatusCreated)

	// Only the instance's system admin; to anybody else the route does not
	// exist, like the rest of the instance administration.
	annaClient := login(t, s, "anna@test.local", "anna-passwort")
	annaClient.expect(http.MethodDelete, "/api/v1/platform/accounts/"+chef, nil, http.StatusNotFound)
	// Not oneself, not the last org admin of an organisation.
	admin.expect(http.MethodDelete, "/api/v1/platform/accounts/"+self, nil, http.StatusConflict)
	admin.expect(http.MethodDelete, "/api/v1/platform/accounts/"+chef, nil, http.StatusConflict)

	// Anna, with seats in two organisations, goes entirely.
	admin.expect(http.MethodDelete, "/api/v1/platform/accounts/"+anna, nil, http.StatusOK)
	annaClient.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusUnauthorized)
	var left int
	if err := s.pool.QueryRow(context.Background(), `SELECT (SELECT count(*) FROM humans WHERE email='anna@test.local')
		+ (SELECT count(*) FROM accounts WHERE email='anna@test.local')`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("rows of anna left: %d %v", left, err)
	}
	admin.expect(http.MethodDelete, "/api/v1/platform/accounts/"+anna, nil, http.StatusNotFound)
	// Her address is free again.
	s.mitglied(t, "anna@test.local", "Anna", "auditor", "anna-neu")
}
