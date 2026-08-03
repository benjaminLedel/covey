package target

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Der Anlass für den gemeinsamen Helfer: Zwei Mails mit je `rechnung.pdf`
// landeten nacheinander auf demselben Pfad, die zweite überschrieb die erste
// stillschweigend. Ein Agent, der sich den Pfad gemerkt hatte, las danach ein
// fremdes Dokument — ohne dass irgendwo etwas schieflief (GitHub #2, Punkt 1).
func TestDateiAblegenKollision(t *testing.T) {
	work := t.TempDir()

	erste, err := DateiAblegen(work, "attachments", "rechnung.pdf", []byte("eins"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(erste.Pfad) != "rechnung.pdf" {
		t.Fatalf("erster Name unerwartet: %s", erste.Pfad)
	}

	// Anderer Inhalt, gleicher Name → eigene Datei, die erste bleibt.
	zweite, err := DateiAblegen(work, "attachments", "rechnung.pdf", []byte("zwei"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if zweite.Pfad == erste.Pfad {
		t.Fatal("zweiter Anhang überschreibt den ersten")
	}
	if filepath.Base(zweite.Pfad) != "rechnung-2.pdf" {
		t.Fatalf("zweiter Name = %s, erwartet rechnung-2.pdf", filepath.Base(zweite.Pfad))
	}
	if inhalt, _ := os.ReadFile(erste.Pfad); string(inhalt) != "eins" {
		t.Fatalf("erste Datei verändert: %q", inhalt)
	}

	// Dritter mit wieder anderem Inhalt zählt weiter.
	dritte, _ := DateiAblegen(work, "attachments", "rechnung.pdf", []byte("drei"), "application/pdf")
	if filepath.Base(dritte.Pfad) != "rechnung-3.pdf" {
		t.Fatalf("dritter Name = %s", filepath.Base(dritte.Pfad))
	}

	// Derselbe Anhang noch einmal abgerufen: byte-gleich, also kein zweites
	// Exemplar — sonst sammelten sich bei jedem Heartbeat Kopien an.
	nochmal, err := DateiAblegen(work, "attachments", "rechnung.pdf", []byte("eins"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if nochmal.Pfad != erste.Pfad {
		t.Fatalf("gleicher Inhalt legt eine Kopie an: %s", nochmal.Pfad)
	}
}

// Der Dateiname kommt vom Absender. Er darf nicht aus der Sandbox führen.
func TestDateiAblegenNameGehaertet(t *testing.T) {
	work := t.TempDir()

	for _, name := range []string{"../../etc/passwd", "/etc/passwd", "..", ".", "   "} {
		datei, err := DateiAblegen(work, "attachments", name, []byte("x"), "")
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		dir := filepath.Join(work, "attachments")
		if filepath.Dir(datei.Pfad) != dir {
			t.Fatalf("%q landet außerhalb: %s", name, datei.Pfad)
		}
		if strings.Contains(filepath.Base(datei.Pfad), "..") {
			t.Fatalf("%q behält Traversal-Anteile: %s", name, datei.Pfad)
		}
	}
}

// Ohne Sandbox gibt es kein Ziel — das muss ein Fehler sein und nicht ein
// Schreibversuch relativ zum Arbeitsverzeichnis des Servers.
func TestDateiAblegenOhneWorkdir(t *testing.T) {
	if _, err := DateiAblegen("", "attachments", "x.txt", []byte("x"), ""); err == nil {
		t.Fatal("ohne Arbeitsverzeichnis kein Fehler")
	}
}

func TestStromAblegen(t *testing.T) {
	work := t.TempDir()

	datei, err := StromAblegen(work, "uploads", "bild.png", strings.NewReader("inhalt"), 1<<20, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if datei.Bytes != 6 {
		t.Fatalf("Bytes = %d", datei.Bytes)
	}
	if !strings.Contains(datei.Hinweis, "Vision") {
		t.Fatalf("Bild-Hinweis fehlt: %s", datei.Hinweis)
	}

	// Zweiter Abruf: Der Strom lässt sich nicht vergleichen, also eine eigene
	// Datei — aber keinesfalls die erste überschreiben.
	zweite, err := StromAblegen(work, "uploads", "bild.png", strings.NewReader("anders"), 1<<20, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if zweite.Pfad == datei.Pfad {
		t.Fatal("zweiter Upload überschreibt den ersten")
	}

	// Über dem Limit: Abbruch, und nichts bleibt liegen.
	zuGross, err := StromAblegen(work, "uploads", "gross.bin", strings.NewReader(strings.Repeat("x", 100)), 10, "")
	if err == nil {
		t.Fatal("Limit nicht durchgesetzt")
	}
	if zuGross.Pfad != "" {
		t.Fatalf("Ergebnis trotz Fehler: %+v", zuGross)
	}
	if _, err := os.Stat(filepath.Join(work, "uploads", "gross.bin")); !os.IsNotExist(err) {
		t.Fatal("abgebrochene Datei bleibt liegen")
	}
}

func TestMaxBytesAusEnv(t *testing.T) {
	const env = "COVEY_TEST_MAX_MB"

	t.Setenv(env, "")
	if got := MaxBytesAusEnv(env, 25, 1024); got != 25<<20 {
		t.Errorf("Default: %d", got)
	}
	t.Setenv(env, "2")
	if got := MaxBytesAusEnv(env, 25, 1024); got != 2<<20 {
		t.Errorf("Übernahme: %d", got)
	}
	// Geklemmt statt still auf den Default zurück (GitHub #2, Punkt 3).
	t.Setenv(env, "2048")
	if got := MaxBytesAusEnv(env, 25, 1024); got != 1024<<20 {
		t.Errorf("Klemmen: %d", got)
	}
	// Der Überlauf-Schutz bleibt: auch absurde Werte enden bei maxMB.
	t.Setenv(env, "8796093022208")
	if got := MaxBytesAusEnv(env, 25, 1024); got != 1024<<20 {
		t.Errorf("Überlauf: %d", got)
	}
	for _, v := range []string{"0", "-1", "viel"} {
		t.Setenv(env, v)
		if got := MaxBytesAusEnv(env, 25, 1024); got != 25<<20 {
			t.Errorf("wert %q: %d, erwartet Default", v, got)
		}
	}
}

func TestHinweis(t *testing.T) {
	if h := Hinweis("/w/a.png", "image/png"); !strings.Contains(h, "Bild liegt") {
		t.Errorf("Bild: %s", h)
	}
	if h := Hinweis("/w/a.pdf", "application/pdf"); !strings.Contains(h, "Content-Type application/pdf") {
		t.Errorf("Datei: %s", h)
	}
	// Ohne Content-Type kein leeres Klammerpaar.
	if h := Hinweis("/w/a.bin", ""); strings.Contains(h, "()") {
		t.Errorf("leere Klammer: %s", h)
	}
}
