package integration

import (
	"encoding/json"
	"net/http"
	"testing"

	"covey/internal/workplaces"
)

// Ein eigenes Image wird angemeldet, nicht getippt.
//
// Vorher stand es als freier Text am Agenten. Das kostete drei Dinge: Es tauchte
// in keiner Uebersicht auf, es trug keine Beschreibung (eine Registry-Adresse
// sagt nicht, wozu sie da ist), und ein Tippfehler fiel erst beim Wecken auf.
// Jetzt ist es ein benanntes Ding, das man einmal anlegt und danach auswaehlt —
// wie ein Profil aus dem Katalog.
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

	// Derselbe Name zweimal: Ein Name gehoert innerhalb einer Organisation
	// genau einem Arbeitsplatz, sonst entschiede die Reihenfolge einer
	// Schleife, welches Image gemeint ist.
	c.expect(http.MethodPost, "/api/v1/workplaces", anlegen, http.StatusConflict)

	// Und ein Name aus dem Katalog ist vergeben, auch wenn diese Organisation
	// ihn nie benutzt hat — die Vereinigung wie die Rollen (#112).
	for _, vergeben := range []string{"dev", "dev-flutter"} {
		c.expect(http.MethodPost, "/api/v1/workplaces", map[string]string{
			"name": vergeben, "image": "registry.example.com/team/anderes:1",
		}, http.StatusConflict)
	}

	// In der Liste steht er neben den veroeffentlichten — fuer den, der
	// auswaehlt, sind es dieselben Dinge.
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

	// Ein Agent zieht ein — und steht danach an seinem Arbeitsplatz, mit Namen.
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

	// Loeschen, solange jemand darin arbeitet, wuerde die Agenten auf einen
	// Namen zeigen lassen, hinter dem nichts mehr steht.
	c.expect(http.MethodDelete, "/api/v1/workplaces/dev-flutter-intern", nil, http.StatusConflict)

	c.expect(http.MethodPatch, "/api/v1/agents/"+agent.ID.String()+"/sandbox-image",
		map[string]string{"sandbox_image": ""}, http.StatusOK)
	c.expect(http.MethodDelete, "/api/v1/workplaces/dev-flutter-intern", nil, http.StatusOK)
}
