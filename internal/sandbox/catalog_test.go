package sandbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const katalog = `{
  "schema": 1,
  "generated_at": "2026-08-14T09:00:00Z",
  "workplaces": [
    {"name": "base", "label": "base", "description": "…",
     "images": [
       {"covey_version": "main",   "ref": "ghcr.io/x/covey-sandbox@sha256:aaa", "platforms": ["linux/amd64"]},
       {"covey_version": "v0.4.0", "ref": "ghcr.io/x/covey-sandbox@sha256:bbb"}
     ]},
    {"name": "dev", "label": "dev", "description": "…",
     "images": [{"covey_version": "main", "ref": "ghcr.io/x/covey-sandbox@sha256:ccc"}]}
  ]
}`

// The catalogue answers the question every installation had to answer for
// itself before: which image belongs to THIS build. Pinned on the digest,
// because a tag is movable and a movable pointer is not a pin.
func TestKatalogLiefertImagesDerLaufendenFassung(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(katalog))
	}))
	defer srv.Close()

	s := NewSource(srv.URL, nil, nil)
	images := s.Images(context.Background())
	// The test binary sits on no release tag — hence the rolling entry.
	if got := images["base"]; got != "ghcr.io/x/covey-sandbox@sha256:aaa" {
		t.Fatalf("base = %q", got)
	}
	if got := images["dev"]; got != "ghcr.io/x/covey-sandbox@sha256:ccc" {
		t.Fatalf("dev = %q", got)
	}
}

// No catalogue configured: then what held before holds. A catalogue is one
// source more, not a requirement.
func TestOhneKatalogBleibtAllesWieVorher(t *testing.T) {
	var s *Source
	if s.Enabled() {
		t.Fatal("ein nil-Source darf nicht aktiv sein")
	}
	if got := s.Images(context.Background()); got != nil {
		t.Fatalf("ohne Katalog keine Bilder, bekam %v", got)
	}
}

// The order is the statement: whoever set an environment variable has the last
// word — a catalogue that could overrule it would let a foreign file decide
// what runs on a foreign host.
func TestReihenfolgeUmgebungKatalogVoreinstellung(t *testing.T) {
	standard := Images(nil)
	katalog := map[string]string{"base": "ghcr.io/x@sha256:aaa", "dev": "ghcr.io/x@sha256:ccc"}
	env := map[string]string{"dev": "meins:1"}

	got := Resolve(env, katalog)
	if got["base"] != "ghcr.io/x@sha256:aaa" {
		t.Fatalf("der Katalog soll die Voreinstellung ersetzen: %q", got["base"])
	}
	if got["dev"] != "meins:1" {
		t.Fatalf("die Umgebung soll den Katalog schlagen: %q", got["dev"])
	}
	// And without either, the compiled default stays standing.
	if leer := Resolve(nil, nil); leer["base"] != standard["base"] {
		t.Fatalf("ohne Quellen die Voreinstellung: %q", leer["base"])
	}
}

// Where the catalogue comes from is not in the interface and not in a constant
// beside the address of the source text, but derived from it: a fork thus
// carries its own.
func TestVoreingestellteKatalogAdresse(t *testing.T) {
	want := "https://raw.githubusercontent.com/benjaminLedel/covey/catalog/sandbox-catalog.json"
	if got := DefaultCatalogURL(); got != want {
		t.Fatalf("DefaultCatalogURL() = %q", got)
	}
}

// The difference every piece of advice hangs on: a name without a registry
// exists only on the machine that built it — there "not there" means build. A
// published address `docker run` fetches itself.
func TestPullable(t *testing.T) {
	ziehbar := []string{
		"ghcr.io/benjaminledel/covey-sandbox@sha256:abc",
		"ghcr.io/benjaminledel/covey-sandbox:dev-latest",
		"registry.example.com:5000/team/sandbox:1",
		"localhost:5000/eigenes:1",
	}
	for _, ref := range ziehbar {
		if !Pullable(ref) {
			t.Errorf("Pullable(%q) = false", ref)
		}
	}
	lokal := []string{"covey-sandbox:latest", "covey-sandbox-dev:latest", "", "postgres:16"}
	for _, ref := range lokal {
		if Pullable(ref) {
			t.Errorf("Pullable(%q) = true — dann verschwindet der Bau-Hinweis", ref)
		}
	}
}

// A release moves the key that is searched for: the same instance asked
// yesterday for `main` and today for `v0.8.0`. If the catalogue copy is older
// than the release, it holds no answer — and the fallback to the compiled-in
// name (`covey-sandbox:latest`) is on a server the worst available: a name that
// demonstrably does not exist there, chosen over an image that demonstrably
// does. On a production instance the entire data plane stood still for an
// hour because of it.
func TestOhneEintragFuerDieseFassungGiltDerRollendeEintrag(t *testing.T) {
	e := CatalogEntry{Name: "dev", Images: []CatalogImage{
		{CoveyVersion: RollingVersion, Ref: "ghcr.io/x/covey-sandbox@sha256:rollend"},
		{CoveyVersion: "v0.7.0", Ref: "ghcr.io/x/covey-sandbox@sha256:alt"},
	}}

	img, ok := e.ForBuild("v0.8.0")
	if !ok || img.Ref != "ghcr.io/x/covey-sandbox@sha256:rollend" {
		t.Errorf("eine unbekannte Fassung muss den rollenden Eintrag bekommen, bekam %q (%v)", img.Ref, ok)
	}
	// The build's own version still beats the rolling entry.
	if img, ok := e.ForBuild("v0.7.0"); !ok || img.Ref != "ghcr.io/x/covey-sandbox@sha256:alt" {
		t.Errorf("die eigene Fassung muss vorgehen, bekam %q", img.Ref)
	}
}

// A catalogue without a rolling entry cannot help — then it stays at the
// compiled-in name, and that is right: a machine without a catalogue is mostly
// the one that builds its images itself.
func TestOhneRollendenEintragBleibtEsBeiDerVoreinstellung(t *testing.T) {
	e := CatalogEntry{Name: "dev", Images: []CatalogImage{
		{CoveyVersion: "v0.7.0", Ref: "ghcr.io/x/covey-sandbox@sha256:alt"},
	}}
	if img, ok := e.ForBuild("v0.8.0"); ok {
		t.Errorf("ohne rollenden Eintrag darf nichts erfunden werden, bekam %q", img.Ref)
	}
}

// And the fallback holds all the way through: what the instance resolves is a
// published image, not a local build name.
func TestDerRueckfallGehtDurchBisZurAufloesung(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(katalog))
	}))
	defer srv.Close()

	// The catalogue knows only `main` and `v0.4.0`; this build sits on
	// neither of the two.
	s := NewSource(srv.URL, nil, nil)
	images := s.Images(context.Background())
	aufgeloest := Resolve(nil, images)
	for _, name := range []string{"base", "dev"} {
		if !Pullable(aufgeloest[name]) {
			t.Errorf("%s löst auf %q auf — ein Name, den ein Server nicht ziehen kann", name, aufgeloest[name])
		}
	}
}
