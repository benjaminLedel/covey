package homestore

import "testing"

/* The exclude list compared prefixes from the root of the home — and hit
   exactly the cases it was not about: `__pycache__` sits deep in a
   project, never beside one, and `*.pyc` was no pattern at all, but a
   file name with a star in it (#103). */

func TestAusschlussKenntPfadeNamenUndMuster(t *testing.T) {
	e := Excludes{"repos/scratch", "__pycache__", "*.pyc", "aptroot/debs"}

	fälle := []struct {
		pfad string
		raus bool
		was  string
	}{
		{"repos/scratch", true, "der Pfad selbst"},
		{"repos/scratch/tief/datei.txt", true, "alles darunter"},
		{"repos/scratch-anders/datei.txt", false, "ein Präfix ist kein Verzeichnis"},
		{"repos/app/__pycache__", true, "ein Name in der Tiefe"},
		{"repos/app/__pycache__/modul.cpython-312.pyc", true, "und was darunter liegt"},
		{"__pycache__", true, "derselbe Name oben"},
		{"repos/app/modul.pyc", true, "ein Muster auf dem Dateinamen"},
		{"repos/app/modul.py", false, "und nur darauf"},
		{"aptroot/debs/paket.deb", true, "ein Pfad mit Verzeichnis"},
		{"aptroot/sources.list", false, "der Nachbar bleibt"},
		{"repos/app/main.go", false, "der Normalfall bleibt drin"},
	}
	for _, f := range fälle {
		if got := e.skip(f.pfad); got != f.raus {
			t.Errorf("%s: skip(%q) = %v, erwartet %v (%s)", f.was, f.pfad, got, f.raus, f.was)
		}
	}
}

// A broken pattern excludes nothing. When in doubt, secure: one file too
// many costs space, a missing one costs work nobody takes back.
func TestEinKaputtesMusterSchliesstNichtsAus(t *testing.T) {
	kaputt := Excludes{"[unvollständig"}
	if kaputt.skip("datei.txt") {
		t.Fatal("ein unlesbares Muster hat etwas ausgeschlossen")
	}
}

// Without a list everything stays in — that is the situation an installation
// is in that set COVEY_HOME_EXCLUDES to "none".
func TestOhneListeBleibtAllesDrin(t *testing.T) {
	leer, keine := Excludes{}, Excludes(nil)
	if leer.skip("__pycache__") || keine.skip("egal.pyc") {
		t.Fatal("ohne Liste wurde etwas ausgeschlossen")
	}
}
