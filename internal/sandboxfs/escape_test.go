package sandboxfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Das Home ist ein Host-Verzeichnis, das schreibbar in die Sandbox gemountet
// wird (orchestrator/sandbox_docker.go). Der Agent hat darin per `dev exec`
// eine Shell — er kann also Symlinks anlegen, die aus dem Home hinauszeigen.
// Diese Tests halten fest, dass keine Operation ihnen folgt: sonst liest oder
// schreibt der Datei-Browser eines Admins auf dem HOST außerhalb des Homes.
//
// Sie prüfen Verhalten, nicht Implementierung — deshalb gelten sie unverändert
// für die alte Prüfung (resolve/ensureInside) wie für os.Root.

// aufbau legt ein Home mit einem Geheimnis daneben an und gibt beides zurück.
func aufbau(t *testing.T) (fs *FS, home, geheimnis string) {
	t.Helper()
	basis := t.TempDir()
	home = filepath.Join(basis, "home")
	if err := os.MkdirAll(filepath.Join(home, "unterordner"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Liegt NEBEN dem Home, nicht darin — genau das darf nie erreichbar werden.
	geheimnis = filepath.Join(basis, "geheim.txt")
	if err := os.WriteFile(geheimnis, []byte("streng geheim"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs, err := New(home, -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	return fs, home, geheimnis
}

func link(t *testing.T, ziel, ort string) {
	t.Helper()
	if err := os.Symlink(ziel, ort); err != nil {
		t.Skipf("Symlinks nicht möglich: %v", err)
	}
}

// Ein Symlink auf eine Datei außerhalb darf nicht lesbar sein.
func TestKeinLesenDurchSymlink(t *testing.T) {
	fs, home, geheimnis := aufbau(t)
	link(t, geheimnis, filepath.Join(home, "raus.txt"))

	if datei, err := fs.Read("raus.txt"); err == nil {
		t.Fatalf("Read folgte dem Link: %q", datei.Content)
	}
	if r, _, err := fs.Open("raus.txt"); err == nil {
		r.Close()
		t.Fatal("Open folgte dem Link")
	}
}

// Ein Symlink auf ein Verzeichnis außerhalb darf nicht auflistbar sein — und
// vor allem darf man nicht DURCH ihn hindurch tiefer greifen.
func TestKeinDurchgriffDurchVerzeichnis(t *testing.T) {
	fs, home, geheimnis := aufbau(t)
	link(t, filepath.Dir(geheimnis), filepath.Join(home, "aussen"))

	if _, err := fs.List("aussen"); err == nil {
		t.Error("List folgte dem Verzeichnis-Link")
	}
	// Der eigentliche Angriff: der Link ist nur die Brücke, das Ziel liegt
	// dahinter.
	if datei, err := fs.Read("aussen/geheim.txt"); err == nil {
		t.Fatalf("Read griff durch den Link hindurch: %q", datei.Content)
	}
}

// Schreiben durch einen Link hinaus wäre der schlimmere Fall: der Agent würde
// den Admin dazu bringen, eine Host-Datei zu überschreiben.
func TestKeinSchreibenDurchSymlink(t *testing.T) {
	fs, home, geheimnis := aufbau(t)
	link(t, filepath.Dir(geheimnis), filepath.Join(home, "aussen"))

	if _, err := fs.Write("aussen/geheim.txt", strings.NewReader("überschrieben")); err == nil {
		t.Error("Write schrieb durch den Link")
	}
	inhalt, err := os.ReadFile(geheimnis)
	if err != nil {
		t.Fatal(err)
	}
	if string(inhalt) != "streng geheim" {
		t.Fatalf("die Datei außerhalb wurde verändert: %q", inhalt)
	}
}

// Löschen und Verschieben dürfen den Link ebenfalls nicht als Weg benutzen.
// Der Link SELBST darf weg — das ist ein Eintrag im Home.
func TestKeinLoeschenUndVerschiebenNachAussen(t *testing.T) {
	fs, home, geheimnis := aufbau(t)
	link(t, filepath.Dir(geheimnis), filepath.Join(home, "aussen"))

	if err := fs.Remove("aussen/geheim.txt"); err == nil {
		t.Error("Remove löschte durch den Link")
	}
	if _, err := os.Stat(geheimnis); err != nil {
		t.Fatalf("die Datei außerhalb ist weg: %v", err)
	}

	if _, err := fs.Move("unterordner", "aussen/verschoben"); err == nil {
		t.Error("Move schob aus dem Home hinaus")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(geheimnis), "verschoben")); err == nil {
		t.Fatal("das Ziel liegt außerhalb des Homes")
	}
}

// Ein Archiv darf keine Dateien von außerhalb einpacken.
func TestZipPacktNichtNachAussen(t *testing.T) {
	fs, home, geheimnis := aufbau(t)
	link(t, geheimnis, filepath.Join(home, "unterordner", "raus.txt"))

	plan, err := fs.PlanZip([]string{"unterordner"})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range plan.items {
		if strings.Contains(it.name, "raus") {
			t.Errorf("der Link nach außen liegt im Archiv: %+v", it)
		}
	}
}

// Textuelle Traversal-Versuche fallen schon beim Normalisieren weg.
func TestTextuelleTraversalWirdNormalisiert(t *testing.T) {
	fs, _, _ := aufbau(t)
	for _, p := range []string{"../geheim.txt", "unterordner/../../geheim.txt", "/../geheim.txt"} {
		if datei, err := fs.Read(p); err == nil {
			t.Errorf("%q wurde gelesen: %q", p, datei.Content)
		}
	}
}

// Ein RELATIVER Link innerhalb des Homes muss benutzbar bleiben — das ist die
// Form, die Toolchains tatsächlich anlegen (`.claude/skills -> ../.agents/skills`,
// node_modules/.bin). Ein Fix, der die mit abräumt, macht den Datei-Browser
// unbrauchbar.
func TestRelativerLinkInnerhalbBleibtBenutzbar(t *testing.T) {
	fs, home, _ := aufbau(t)
	if err := os.WriteFile(filepath.Join(home, "unterordner", "echt.txt"), []byte("hallo"), 0o644); err != nil {
		t.Fatal(err)
	}
	link(t, "unterordner/echt.txt", filepath.Join(home, "verweis.txt"))

	datei, err := fs.Read("verweis.txt")
	if err != nil {
		t.Fatalf("ein relativer Link INNERHALB des Homes muss lesbar bleiben: %v", err)
	}
	if datei.Content != "hallo" {
		t.Errorf("Inhalt = %q", datei.Content)
	}
	// Auch über mehrere Ebenen mit ..
	if err := os.MkdirAll(filepath.Join(home, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, "../../unterordner/echt.txt", filepath.Join(home, "a", "b", "hoch.txt"))
	if datei, err := fs.Read("a/b/hoch.txt"); err != nil || datei.Content != "hallo" {
		t.Errorf("relativer Link über .. : %v / %q", err, datei.Content)
	}
}

// ABSOLUTE Links werden nicht verfolgt, auch wenn ihr Ziel im Home läge.
// Das ist eine Eigenschaft von os.Root und in dieser Umgebung folgenlos: was in
// der Sandbox absolut verlinkt wird, zeigt auf /home/agent/... — einen Pfad,
// den es auf dem Host gar nicht gibt. Solche Links waren schon vorher tot, der
// Eintrag bleibt sichtbar und wird als „außerhalb" markiert.
func TestAbsoluterLinkWirdNichtVerfolgt(t *testing.T) {
	fs, home, _ := aufbau(t)
	if err := os.WriteFile(filepath.Join(home, "unterordner", "echt.txt"), []byte("hallo"), 0o644); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(home, "unterordner", "echt.txt"), filepath.Join(home, "absolut.txt"))

	if _, err := fs.Read("absolut.txt"); err == nil {
		t.Error("einem absoluten Link wurde gefolgt")
	}
	// Sichtbar bleiben muss er trotzdem — sonst sucht ein Admin eine Datei,
	// die im Verzeichnis liegt und in der Liste fehlt.
	listing, err := fs.List("")
	if err != nil {
		t.Fatal(err)
	}
	var gefunden bool
	for _, e := range listing.Entries {
		if e.Name == "absolut.txt" {
			gefunden = true
			if e.Symlink == "" {
				t.Error("das Linkziel fehlt im Eintrag")
			}
			if !e.Outside {
				t.Error("der Eintrag müsste als außerhalb markiert sein")
			}
		}
	}
	if !gefunden {
		t.Error("der Link fehlt in der Auflistung")
	}
}
