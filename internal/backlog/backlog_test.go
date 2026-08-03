package backlog

import (
	"strings"
	"testing"
)

// Die Zustandsmaschine der Aufgabe ist der Kern des Backlogs — und war bisher
// nur mittelbar geprüft: Der Durchstich in internal/integration lief zufällig
// über einige Übergänge, hielt aber keinen einzigen davon fest. Wer
// validTransitions umbaut, bekam von den Tests kein Wort dazu.
//
// Diese Tabelle ist deshalb bewusst in einer ANDEREN Form geschrieben als der
// Produktivcode (dort eine map[string][]string, hier ein Raster). Eine Kopie
// derselben Datenstruktur würde jede Änderung stillschweigend mitmachen; ein
// Raster muss man lesen und bewusst umstellen. Nebenbei sieht man die
// Maschine zum ersten Mal am Stück.
//
// Zeile = Ausgangszustand, Spalte = Zielzustand, x = erlaubt.
const uebergangsRaster = `
              open in_progress blocked done failed cancelled
open           .        x         .      .     .        x
in_progress    x        .         x      x     x        x
blocked        x        .         .      .     .        x
done           .        .         .      .     .        .
failed         x        .         .      .     .        .
cancelled      x        .         .      .     .        .
`

// parseRaster macht aus dem Raster die Menge der erlaubten Paare.
func parseRaster(t *testing.T) (spalten []string, erlaubt map[[2]string]bool) {
	t.Helper()
	zeilen := []string{}
	for _, z := range strings.Split(strings.TrimSpace(uebergangsRaster), "\n") {
		if strings.TrimSpace(z) != "" {
			zeilen = append(zeilen, z)
		}
	}
	spalten = strings.Fields(zeilen[0])
	erlaubt = map[[2]string]bool{}
	for _, z := range zeilen[1:] {
		f := strings.Fields(z)
		if len(f) != len(spalten)+1 {
			t.Fatalf("rasterzeile %q hat %d felder, erwartet %d", z, len(f), len(spalten)+1)
		}
		von := f[0]
		for i, zelle := range f[1:] {
			if zelle == "x" {
				erlaubt[[2]string{von, spalten[i]}] = true
			}
		}
	}
	return spalten, erlaubt
}

// TestUebergangsMatrix prüft JEDE Kombination — auch die verbotenen. Ein Test,
// der nur die erlaubten Wege abgeht, merkt nicht, wenn versehentlich ein
// weiterer dazukommt.
func TestUebergangsMatrix(t *testing.T) {
	spalten, erlaubt := parseRaster(t)

	// Das Raster muss alle Zustände kennen, die der Code kennt — sonst prüft
	// es einen Ausschnitt und behauptet, es sei die ganze Maschine.
	alle := []string{StateOpen, StateInProgress, StateBlocked, StateDone, StateFailed, StateCancelled}
	if len(spalten) != len(alle) {
		t.Fatalf("raster kennt %d zustände, der code %d", len(spalten), len(alle))
	}
	for i, s := range alle {
		if spalten[i] != s {
			t.Fatalf("spalte %d ist %q, erwartet %q", i, spalten[i], s)
		}
	}

	for _, von := range alle {
		for _, nach := range alle {
			want := erlaubt[[2]string{von, nach}]
			if got := transitionAllowed(von, nach); got != want {
				t.Errorf("%s → %s: transitionAllowed=%v, laut Raster %v", von, nach, got, want)
			}
		}
	}
}

// Die Eigenschaften hinter der Tabelle — sie überleben auch eine bewusste
// Umstellung des Rasters und sagen, WARUM die Maschine so aussieht.
func TestUebergangsEigenschaften(t *testing.T) {
	// „done" ist die einzige Sackgasse: Erledigtes wird nicht wieder aufgemacht.
	// Fehlgeschlagenes (Retry) und Verworfenes (Wiederaufnahme) dagegen schon.
	for _, nach := range []string{StateOpen, StateInProgress, StateBlocked, StateFailed, StateCancelled} {
		if transitionAllowed(StateDone, nach) {
			t.Errorf("aus done darf kein weg herausführen, gefunden: done → %s", nach)
		}
	}
	for _, von := range []string{StateFailed, StateCancelled} {
		if !transitionAllowed(von, StateOpen) {
			t.Errorf("%s muss sich wieder öffnen lassen (Retry bzw. Wiederaufnahme)", von)
		}
	}

	// In Arbeit geht ein Lauf NUR aus dem Backlog heraus: open ist der einzige
	// Vorzustand von in_progress. Sonst könnte ein zweiter Lauf eine wartende
	// Aufgabe an sich reißen.
	for _, von := range []string{StateInProgress, StateBlocked, StateDone, StateFailed, StateCancelled} {
		if transitionAllowed(von, StateInProgress) {
			t.Errorf("nur open darf nach in_progress führen, gefunden: %s → in_progress", von)
		}
	}

	// Abbrechen geht aus jedem nicht-terminalen Zustand — der Kill-Switch darf
	// an keiner Stelle auflaufen.
	for _, von := range []string{StateOpen, StateInProgress, StateBlocked} {
		if !transitionAllowed(von, StateCancelled) {
			t.Errorf("aus %s muss sich abbrechen lassen", von)
		}
	}

	// Kein Zustand führt zu sich selbst: ein Übergang ist eine Änderung.
	for _, s := range []string{StateOpen, StateInProgress, StateBlocked, StateDone, StateFailed, StateCancelled} {
		if transitionAllowed(s, s) {
			t.Errorf("%s → %s (auf sich selbst) darf nicht erlaubt sein", s, s)
		}
	}
}

// Unbekannte Zustände sind kein Sonderfall, sondern schlicht nicht erlaubt —
// fail-closed. Käme aus der Datenbank je ein Wert, den der Code nicht kennt,
// wäre jeder Übergang gesperrt statt jeder erlaubt.
func TestUnbekannteZustaende(t *testing.T) {
	for _, paar := range [][2]string{
		{"quatsch", StateOpen},
		{StateOpen, "quatsch"},
		{"", StateOpen},
		{StateOpen, ""},
		{"OPEN", StateInProgress}, // Groß-/Kleinschreibung zählt
	} {
		if transitionAllowed(paar[0], paar[1]) {
			t.Errorf("%q → %q darf nicht erlaubt sein", paar[0], paar[1])
		}
	}
}

func TestTerminalState(t *testing.T) {
	terminal := map[string]bool{
		StateOpen: false, StateInProgress: false, StateBlocked: false,
		StateDone: true, StateFailed: true, StateCancelled: true,
	}
	for s, want := range terminal {
		if got := terminalState(s); got != want {
			t.Errorf("terminalState(%q)=%v, erwartet %v", s, got, want)
		}
	}
	if terminalState("quatsch") {
		t.Error("ein unbekannter zustand ist nicht terminal")
	}
}

// Jeder terminale Zustand hat eine Spalte auf dem Board, sonst bliebe eine
// erledigte Aufgabe dort liegen, wo sie zuletzt stand (siehe syncStage).
func TestTerminaleZustaendeHabenEineSpalte(t *testing.T) {
	for _, s := range []string{StateDone, StateFailed, StateCancelled} {
		if stateStage[s] == "" {
			t.Errorf("terminaler zustand %q hat keine Ziel-Spalte in stateStage", s)
		}
	}
}
