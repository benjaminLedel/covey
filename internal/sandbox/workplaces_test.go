package sandbox

import "testing"

/* Die Beschreibung liegt einmal im Repository und wird an zwei Stellen
   gebraucht: in den Images (COPY … /etc/covey/workplace.json) und in der
   Oberfläche. Zwei getrennte Listen wären in einem Monat zwei verschiedene
   Wahrheiten — dieser Test hält fest, dass es eine ist. */

func TestJedesAusgelieferteProfilBeschreibtSich(t *testing.T) {
	for _, profil := range []string{"base", "dev"} {
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
		// Der Satz, der die Werkzeug-Anfrage überhaupt erst auslöst: Wer nicht
		// weiß, dass er kein root hat, baut sich apt nach (#106).
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

// Ein eigenes Image ist kein Profil — dann ist die ehrliche Antwort, dass die
// Plattform es nicht weiß, statt eine fremde Beschreibung zu zeigen.
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
