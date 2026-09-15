package integration

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

// One account in two organisations (#262): the operator hands out the seat, the
// person switches, and every answer follows the active seat.
func TestAccountInTwoOrganisations(t *testing.T) {
	s := newStack(t)
	s.alsSystemadmin(t)
	admin := login(t, s, "admin@test.local", "admin-passwort")

	created := admin.expect(http.MethodPost, "/api/v1/platform/orgs", map[string]string{
		"name": "Second Org", "admin_email": "chef@second.local", "admin_name": "Chef",
		"admin_password": "chef-passwort",
	}, http.StatusCreated)
	first, second := s.orgID.String(), created["id"].(string)
	s.mitglied(t, "anna@test.local", "Anna", "agent_owner", "anna-passwort")

	ids := map[string]string{}
	for _, a := range admin.expectList(http.MethodGet, "/api/v1/platform/accounts", nil, http.StatusOK) {
		ids[a["email"].(string)] = a["id"].(string)
	}
	anna, chef := ids["anna@test.local"], ids["chef@second.local"]
	if anna == "" || chef == "" {
		t.Fatalf("accounts missing from the list: %v", ids)
	}

	members := "/api/v1/platform/orgs/" + second + "/members"
	admin.expect(http.MethodPost, members, map[string]string{"account_id": anna, "role": "boss"}, http.StatusBadRequest)
	admin.expect(http.MethodPost, members, map[string]string{"account_id": uuid.NewString(), "role": "org_admin"}, http.StatusNotFound)
	admin.expect(http.MethodPost, "/api/v1/platform/orgs/"+uuid.NewString()+"/members",
		map[string]string{"account_id": anna, "role": "org_admin"}, http.StatusNotFound)
	admin.expect(http.MethodPost, members, map[string]string{"account_id": anna, "role": "org_admin"}, http.StatusCreated)
	admin.expect(http.MethodPost, members, map[string]string{"account_id": anna, "role": "auditor"}, http.StatusConflict)

	// Handing out seats is the instance's business: the second organisation's
	// own admin does not reach it.
	chefClient := login(t, s, "chef@second.local", "chef-passwort")
	chefClient.expect(http.MethodPost, members, map[string]string{"account_id": chef, "role": "org_admin"}, http.StatusNotFound)

	annaClient := login(t, s, "anna@test.local", "anna-passwort")
	if seats := annaClient.expectList(http.MethodGet, "/api/v1/auth/memberships", nil, http.StatusOK); len(seats) != 2 {
		t.Fatalf("%d memberships, expected 2: %v", len(seats), seats)
	}
	// Sign-in starts in the older seat, where she is agent_owner.
	if me := annaClient.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK); me["OrgID"] != first {
		t.Fatalf("signed in to %v, expected the first organisation", me["OrgID"])
	}
	annaClient.expect(http.MethodGet, "/api/v1/users", nil, http.StatusForbidden)

	// An organisation without her seat is no destination.
	annaClient.expect(http.MethodPost, "/api/v1/auth/switch-org", map[string]string{"org_id": uuid.NewString()}, http.StatusNotFound)

	switched := annaClient.expect(http.MethodPost, "/api/v1/auth/switch-org", map[string]string{"org_id": second}, http.StatusOK)
	if switched["OrgID"] != second || switched["Role"] != "org_admin" {
		t.Fatalf("after the switch: org %v, role %v", switched["OrgID"], switched["Role"])
	}
	users := annaClient.expectList(http.MethodGet, "/api/v1/users", nil, http.StatusOK)
	if len(users) != 2 {
		t.Fatalf("%d users in the second organisation, expected chef and anna: %v", len(users), users)
	}
	for _, u := range users {
		if u["org_id"] != second {
			t.Errorf("a user of another organisation: %v", u)
		}
	}

	// The role of the seat changes under a running session.
	admin.expect(http.MethodPatch, members+"/"+anna, map[string]string{"role": "auditor"}, http.StatusOK)
	if me := annaClient.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK); me["Role"] != "auditor" {
		t.Errorf("role after the change = %v, expected auditor", me["Role"])
	}
	// chef is now the second organisation's only admin.
	admin.expect(http.MethodDelete, members+"/"+chef, nil, http.StatusConflict)

	// Removing the seat she works from moves her session back to the first
	// organisation instead of signing her out of both.
	admin.expect(http.MethodDelete, members+"/"+anna, nil, http.StatusOK)
	if me := annaClient.expect(http.MethodGet, "/api/v1/auth/me", nil, http.StatusOK); me["OrgID"] != first {
		t.Fatalf("after removal the session is in %v, expected the first organisation", me["OrgID"])
	}
	admin.expect(http.MethodDelete, members+"/"+anna, nil, http.StatusNotFound)
}
