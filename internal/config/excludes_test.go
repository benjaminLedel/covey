package config

import (
	"slices"
	"testing"
)

/* "Empty" was the wrong default: one measured home carried 19.1 GB into
   the store, among them __pycache__, .dartServer and a hand-built
   apt tree — scanned on every wake, written back after every run
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
	// The package caches do NOT belong in it: leaving them out saves space
	// and costs a renewed download on the next host. That is a decision per
	// installation, not a guess about someone else's operation.
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
	// "none" is the way back to the old state: everything is stored. Without it
	// there would be none — an empty variable now means "the default".
	for _, aus := range []string{"none", "NONE", " none "} {
		if got := homeExcludes(aus); len(got) != 0 {
			t.Fatalf("%q hat nicht abgeschaltet: %v", aus, got)
		}
	}
}
