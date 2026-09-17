package sandbox

import "testing"

/* The description stands once in the repository and is needed in two places:
   in the images (COPY … /etc/covey/workplace.json) and in the UI. Two separate
   lists would be two different truths within a month — this test pins that it
   is one. */

func TestJedesAusgelieferteProfilBeschreibtSich(t *testing.T) {
	// The list comes from the registry and not by hand: a profile that someone
	// adds and does not describe puts its agent back in the workshop without an
	// answer — and a test case maintained by hand would have missed exactly that
	// (#112).
	if len(All()) < 2 {
		t.Fatal("die Registrierung ist leer — dann prueft dieser Test nichts")
	}
	for _, profil := range All() {
		profil := profil.Name
		doc, ok := Workplace(profil)
		if !ok {
			t.Fatalf("%s hat keine Beschreibung — dann steht der Agent wieder ohne Auskunft da", profil)
		}
		if doc.Profile != profil {
			t.Fatalf("%s nennt sich %q", profil, doc.Profile)
		}
		if doc.Summary == "" || len(doc.Tools) == 0 {
			t.Fatalf("%s beschreibt sich leer: %+v", profil, doc)
		}
		// The sentence that triggers the tool enquiry in the first place: who
		// does not know that it has no root retrofits apt (#106).
		var sagtEsRootlos bool
		for _, n := range doc.Notes {
			if len(n) > 0 && (contains(n, "root") || contains(n, "apt")) {
				sagtEsRootlos = true
			}
		}
		if !sagtEsRootlos {
			t.Fatalf("%s sagt nicht, dass der Agent nicht root ist", profil)
		}
	}
}

// An own image is not a profile — then the honest answer is that the platform
// does not know, instead of showing somebody else's description.
func TestEinFremdesImageHatKeineBeschreibung(t *testing.T) {
	if _, ok := Workplace("ghcr.io/jemand/eigenes:latest"); ok {
		t.Fatal("für ein fremdes Image wurde etwas behauptet")
	}
	if _, ok := Workplace("../../etc/passwd"); ok {
		t.Fatal("ein Pfad wurde als Profil gelesen")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
