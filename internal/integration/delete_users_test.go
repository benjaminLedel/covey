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
