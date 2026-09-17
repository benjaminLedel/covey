package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"covey/internal/workplaces"
)

// An own image is registered, not typed in.
//
// Before it stood at the agent as free text. That cost three things: it turned up
// in no overview, it carried no description (a registry address
// does not say what it is there for), and a typo only showed at wake.
// Now it is a named thing that you create once and afterwards pick —
// like a profile from the catalogue.
func TestEigenerArbeitsplatz(t *testing.T) {
	s := newStack(t)
	s.srv.OrgWorkplaces = workplaces.New(s.pool)
	c := login(t, s, "admin@test.local", "admin-passwort")

	anlegen := map[string]string{
		"name":        "dev-flutter-intern",
		"label":       "Flutter (interne CA)",
		"description": "Flutter-Toolchain plus internes Zertifikat",
		"image":       "registry.example.com/team/sandbox:2026-08",
	}
	c.expect(http.MethodPost, "/api/v1/workplaces", anlegen, http.StatusCreated)

	// The same name twice: a name belongs inside an organisation
	// to exactly one workplace, otherwise the order of a
	// loop would decide which image is meant.
	c.expect(http.MethodPost, "/api/v1/workplaces", anlegen, http.StatusConflict)

	// And a name from the catalogue is taken, even if this organisation
	// never used it — the union, same as the roles (#112).
	for _, vergeben := range []string{"dev", "dev-flutter"} {
		c.expect(http.MethodPost, "/api/v1/workplaces", map[string]string{
			"name": vergeben, "image": "registry.example.com/team/anderes:1",
		}, http.StatusConflict)
	}

	// In the list it stands next to the published ones — for the one who
	// picks, they are the same things.
	var list []struct {
		Name   string `json:"name"`
		Kind   string `json:"kind"`
		Image  string `json:"image"`
		Agents []struct {
			DisplayName string `json:"display_name"`
		} `json:"agents"`
	}
	resp := c.do(http.MethodGet, "/api/v1/workplaces", nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()

	nach := map[string]int{}
	for i, w := range list {
		nach[w.Name] = i
	}
	eigener, ok := nach["dev-flutter-intern"]
	if !ok {
		t.Fatalf("der eigene Arbeitsplatz fehlt in der Liste: %+v", list)
	}
	if list[eigener].Kind != "own" || list[eigener].Image != anlegen["image"] {
		t.Errorf("eigener Arbeitsplatz: %+v", list[eigener])
	}
	if list[nach["base"]].Kind != "catalog" {
		t.Errorf("base soll aus dem Katalog kommen: %+v", list[nach["base"]])
	}

	// An agent moves in — and afterwards stands at its workplace, by name.
	agent := s.newSupportAgent("flutter-agent")
	c.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/sandbox-image",
		map[string]string{"sandbox_image": "dev-flutter-intern"}, http.StatusOK)

	resp = c.do(http.MethodGet, "/api/v1/workplaces", nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	for i, w := range list {
		nach[w.Name] = i
	}
	if n := len(list[nach["dev-flutter-intern"]].Agents); n != 1 {
		t.Fatalf("erwartet 1 Agent an diesem Arbeitsplatz, sind %d", n)
	}

	// Deleting while somebody works in it would let the agents point at a
	// name behind which nothing stands any more.
	c.expect(http.MethodDelete, "/api/v1/workplaces/dev-flutter-intern", nil, http.StatusConflict)

	c.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/sandbox-image",
		map[string]string{"sandbox_image": ""}, http.StatusOK)
	c.expect(http.MethodDelete, "/api/v1/workplaces/dev-flutter-intern", nil, http.StatusOK)
}
