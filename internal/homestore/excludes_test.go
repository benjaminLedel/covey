package homestore

import "testing"

/* Die Ausschlussliste verglich Präfixe ab der Wurzel des Homes — und traf damit
   genau die Fälle nicht, um die es geht: `__pycache__` liegt tief in einem
   Projekt, nie daneben, und `*.pyc` war überhaupt kein Muster, sondern ein
   Dateiname mit einem Stern darin (#103). */

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

// Ein kaputtes Muster schließt nichts aus. Im Zweifel sichern: eine Datei zu
// viel kostet Platz, eine fehlende kostet Arbeit, die niemand zurückholt.
func TestEinKaputtesMusterSchliesstNichtsAus(t *testing.T) {
	kaputt := Excludes{"[unvollständig"}
	if kaputt.skip("datei.txt") {
		t.Fatal("ein unlesbares Muster hat etwas ausgeschlossen")
	}
}

// Ohne Liste bleibt alles drin — das ist die Lage, in der eine Installation
// steht, die COVEY_HOME_EXCLUDES auf „none" gesetzt hat.
func TestOhneListeBleibtAllesDrin(t *testing.T) {
	leer, keine := Excludes{}, Excludes(nil)
	if leer.skip("__pycache__") || keine.skip("egal.pyc") {
		t.Fatal("ohne Liste wurde etwas ausgeschlossen")
	}
}
