package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"covey/internal/config"
	"covey/internal/sandbox"
)

// Wo das Image eines Arbeitsplatzes liegt, kommt aus einem Katalog — derselben
// Form, in der die Plugins kommen (spec/22): eine Datei hinter einer URL,
// gepinnt auf den Digest.
//
// Der Test haelt die Reihenfolge fest, die die ganze Sache traegt: Was die
// Instanz ausdruecklich gesetzt hat, gewinnt; darunter der Katalog; darunter
// die kompilierte Voreinstellung. Ein Katalog, der eine gesetzte Adresse
// ueberstimmen koennte, liesse eine fremde Datei entscheiden, was auf einem
// fremden Host laeuft.
func TestArbeitsplaetzeAusDemKatalog(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	katalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"schema":1,"generated_at":"2026-08-14T09:00:00Z","workplaces":[
			{"name":"base","label":"base","description":"…",
			 "images":[{"covey_version":%q,"ref":"ghcr.io/test/covey-sandbox@sha256:aaa"}]},
			{"name":"dev","label":"dev","description":"…",
			 "images":[{"covey_version":%q,"ref":"ghcr.io/test/covey-sandbox@sha256:bbb"}]}
		]}`, sandbox.CatalogVersion(), sandbox.CatalogVersion())
	}))
	defer katalog.Close()

	s.srv.Workplaces = sandbox.NewSource(katalog.URL, nil, nil)
	// Nur fuer `dev` hat diese Instanz selbst etwas gesagt.
	s.srv.Config = &config.Config{
		SandboxImage:    "covey-sandbox:test",
		SandboxImageEnv: map[string]string{"dev": "eigenes-dev:2026"},
	}

	var list []struct {
		Name   string `json:"name"`
		Image  string `json:"image"`
		Source string `json:"source"`
	}
	resp := c.do(http.MethodGet, "/api/v1/workplaces", nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("workplaces: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	nach := map[string]struct{ image, source string }{}
	for _, w := range list {
		nach[w.Name] = struct{ image, source string }{w.Image, w.Source}
	}
	if got := nach["base"]; got.image != "ghcr.io/test/covey-sandbox@sha256:aaa" || got.source != "catalog" {
		t.Errorf("base kommt aus dem Katalog: %+v", got)
	}
	if got := nach["dev"]; got.image != "eigenes-dev:2026" || got.source != "env" {
		t.Errorf("dev bleibt bei dem, was die Instanz gesetzt hat: %+v", got)
	}
}

// Ohne Katalog bleibt alles, wie es war. Ein Katalog ist eine Quelle mehr und
// keine Voraussetzung — eine Installation ohne Netz soll nichts vermissen.
func TestOhneKatalogGeltenVoreinstellungUndUmgebung(t *testing.T) {
	s := newStack(t)
	c := login(t, s, "admin@test.local", "admin-passwort")

	s.srv.Workplaces = nil
	s.srv.Config = &config.Config{SandboxImage: "covey-sandbox:test"}

	var list []struct {
		Name   string `json:"name"`
		Image  string `json:"image"`
		Source string `json:"source"`
	}
	resp := c.do(http.MethodGet, "/api/v1/workplaces", nil)
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()

	for _, w := range list {
		if w.Source != "builtin" {
			t.Errorf("%s: source = %q, ohne Katalog und ohne Umgebung bleibt die Voreinstellung", w.Name, w.Source)
		}
		if prof, ok := sandbox.Get(w.Name); ok && w.Image != prof.Image {
			t.Errorf("%s: image = %q, erwartet %q", w.Name, w.Image, prof.Image)
		}
	}
}
