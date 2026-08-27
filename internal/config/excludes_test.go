package config

import (
	"slices"
	"testing"
)

/* „Leer" war die falsche Voreinstellung: ein gemessenes Home trug 19,1 GB in
   den Store, darunter __pycache__, .dartServer und ein von Hand nachgebauter
   apt-Baum — bei jedem Weckruf durchgesehen, nach jedem Lauf zurückgeschrieben
   (#103). */

func TestOhneEinstellungGiltDieSchrottKlasse(t *testing.T) {
	got := homeExcludes("")
	if len(got) == 0 {
		t.Fatal("ohne Einstellung wird wieder alles gesichert")
	}
	for _, muss := range []string{"__pycache__", ".dartServer"} {
		if !slices.Contains(got, muss) {
			t.Fatalf("%s fehlt in der Voreinstellung: %v", muss, got)
		}
	}
	// Die Paket-Caches gehören NICHT hinein: sie herauszulassen spart Platz
	// und kostet einen erneuten Download auf dem nächsten Host. Das ist eine
	// Entscheidung je Installation, keine Vermutung über fremde Betriebe.
	for _, darfNicht := range []string{".npm", ".gradle", ".pub-cache", ".composer"} {
		if slices.Contains(got, darfNicht) {
			t.Fatalf("%s steht in der Voreinstellung — das ist die Entscheidung des Betreibers", darfNicht)
		}
	}
}

func TestDieEinstellungErsetztUndKannAbschalten(t *testing.T) {
	if got := homeExcludes("repos/scratch, *.tmp"); len(got) != 2 || got[0] != "repos/scratch" {
		t.Fatalf("die eigene Liste kam nicht durch: %v", got)
	}
	// „none" ist der Weg zurück zur alten Lage: alles wird gesichert. Ohne ihn
	// gäbe es keinen — eine leere Variable heißt jetzt „die Voreinstellung".
	for _, aus := range []string{"none", "NONE", " none "} {
		if got := homeExcludes(aus); len(got) != 0 {
			t.Fatalf("%q hat nicht abgeschaltet: %v", aus, got)
		}
	}
}
