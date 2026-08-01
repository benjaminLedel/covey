package sandboxfs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestFS(t *testing.T) (*FS, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	fs, err := New(root, -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	return fs, root
}

// Der Kern des Pakets: kein Pfad zeigt aus der Wurzel heraus. Alles andere
// wäre ein Dateibrowser auf dem gesamten Host.
func TestAusbruchsversuche(t *testing.T) {
	fs, root := newTestFS(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "geheim.txt"), []byte("nicht für dich"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "unter"), 0o755); err != nil {
		t.Fatal(err)
	}

	// `..` in jeder Schreibweise landet höchstens wieder in der Wurzel, nie
	// darüber — und damit auch nie an der Datei daneben.
	for _, p := range []string{"../geheim.txt", "unter/../../geheim.txt", "/../geheim.txt", "..%2Fgeheim.txt"} {
		if _, err := fs.Read(p); err == nil {
			t.Errorf("Read(%q) hätte scheitern müssen", p)
		}
	}
	// Absolute Pfade werden als relativ zur Wurzel gelesen, nicht als Host-Pfad.
	if _, _, err := fs.resolve("/etc/passwd"); err != nil {
		t.Fatalf("absoluter pfad: %v", err)
	} else if abs, _, _ := fs.resolve("/etc/passwd"); abs != filepath.Join(root, "etc", "passwd") {
		t.Errorf("absoluter pfad landet bei %q, erwartet unterhalb der wurzel", abs)
	}
}

func TestSymlinkAusbruch(t *testing.T) {
	fs, root := newTestFS(t)
	außerhalb := filepath.Join(filepath.Dir(root), "aussen")
	if err := os.MkdirAll(außerhalb, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(außerhalb, "beute.txt"), []byte("beute"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(außerhalb, filepath.Join(root, "raus")); err != nil {
		t.Skipf("symlinks nicht verfügbar: %v", err)
	}

	// Lesen durch den Link hindurch: verboten.
	if _, err := fs.Read("raus/beute.txt"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("lesen durch symlink: %v, erwartet ErrInvalidPath", err)
	}
	// Schreiben durch den Link hindurch ebenfalls — sonst wäre der Link ein
	// Schreibzugriff auf den Host.
	if _, err := fs.Write("raus/neu.txt", strings.NewReader("x")); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("schreiben durch symlink: %v, erwartet ErrInvalidPath", err)
	}
	// Sichtbar bleibt er trotzdem, samt Ziel und Warnung: verstecken hieße,
	// dem Betrachter etwas vorzuenthalten, was im Home tatsächlich liegt.
	list, err := fs.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].Name != "raus" {
		t.Fatalf("auflistung: %+v", list.Entries)
	}
	if !list.Entries[0].Outside || list.Entries[0].Symlink == "" {
		t.Errorf("symlink nach draußen nicht als solcher markiert: %+v", list.Entries[0])
	}
}

func TestListeSortiertVerzeichnisseZuerst(t *testing.T) {
	fs, root := newTestFS(t)
	for _, d := range []string{"zeta", "alpha"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"b.txt", "A.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	list, err := fs.List("")
	if err != nil {
		t.Fatal(err)
	}
	var namen []string
	for _, e := range list.Entries {
		namen = append(namen, e.Name)
	}
	want := []string{"alpha", "zeta", "A.txt", "b.txt"}
	if strings.Join(namen, ",") != strings.Join(want, ",") {
		t.Errorf("reihenfolge %v, erwartet %v", namen, want)
	}
	if !list.Exists {
		t.Error("exists=false, obwohl das home existiert")
	}
}

// Ein nie geweckter Agent hat noch kein Home. Das ist kein Fehler, sondern ein
// leerer Arbeitsplatz — die UI soll ihn zeigen können.
func TestFehlendesHomeIstLeerNichtFehler(t *testing.T) {
	fs, err := New(filepath.Join(t.TempDir(), "nie-geweckt"), -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	list, err := fs.List("")
	if err != nil {
		t.Fatalf("liste: %v", err)
	}
	if list.Exists || len(list.Entries) != 0 {
		t.Errorf("erwartet leeres, nicht existierendes home: %+v", list)
	}
	// Unterverzeichnisse gibt es dann aber wirklich nicht.
	if _, err := fs.List("irgendwas"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unterverzeichnis: %v, erwartet ErrNotFound", err)
	}
	// Ein Upload legt das Home an.
	if _, err := fs.Write("neu/datei.txt", strings.NewReader("inhalt")); err != nil {
		t.Fatalf("schreiben ins fehlende home: %v", err)
	}
	if f, err := fs.Read("neu/datei.txt"); err != nil || f.Content != "inhalt" {
		t.Errorf("lesen nach anlegen: %+v, %v", f, err)
	}
}

func TestSchreibenLesenVerschiebenLoeschen(t *testing.T) {
	fs, _ := newTestFS(t)

	if _, err := fs.Write("a/b/c.txt", strings.NewReader("hallo")); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Read("a/b/c.txt")
	if err != nil || f.Content != "hallo" || f.Binary || f.Truncated {
		t.Fatalf("lesen: %+v, %v", f, err)
	}
	if _, err := fs.Move("a/b/c.txt", "a/b/d.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Read("a/b/c.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("alter pfad noch da: %v", err)
	}
	// Ein belegtes Ziel wird nicht überschrieben.
	if _, err := fs.Write("a/b/e.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Move("a/b/d.txt", "a/b/e.txt"); !errors.Is(err, ErrExists) {
		t.Errorf("verschieben auf belegtes ziel: %v, erwartet ErrExists", err)
	}
	if err := fs.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.List("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("verzeichnis nach löschen: %v", err)
	}
	// Die Wurzel selbst löscht man nicht aus dem Dateibrowser.
	if err := fs.Remove(""); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("wurzel löschen: %v, erwartet ErrInvalidPath", err)
	}
}

func TestVerzeichnisUndDateiVerwechseln(t *testing.T) {
	fs, _ := newTestFS(t)
	if _, err := fs.Mkdir("ordner"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkdir("ordner"); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir doppelt: %v, erwartet ErrExists", err)
	}
	if _, err := fs.Read("ordner"); !errors.Is(err, ErrIsDir) {
		t.Errorf("verzeichnis lesen: %v, erwartet ErrIsDir", err)
	}
	if _, err := fs.Write("ordner", strings.NewReader("x")); !errors.Is(err, ErrIsDir) {
		t.Errorf("verzeichnis überschreiben: %v, erwartet ErrIsDir", err)
	}
	if _, err := fs.Write("datei.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.List("datei.txt"); !errors.Is(err, ErrNotDir) {
		t.Errorf("datei auflisten: %v, erwartet ErrNotDir", err)
	}
}

func TestGrosseUndBinaereDateien(t *testing.T) {
	fs, root := newTestFS(t)

	// Über der Ansichtsgrenze: abgeschnitten, aber lesbar.
	groß := strings.Repeat("z", MaxViewBytes+1000)
	if err := os.WriteFile(filepath.Join(root, "groß.txt"), []byte(groß), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Read("groß.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Truncated || len(f.Content) != MaxViewBytes {
		t.Errorf("abschneiden: truncated=%v len=%d", f.Truncated, len(f.Content))
	}
	if f.Size != int64(len(groß)) {
		t.Errorf("größe %d, erwartet %d", f.Size, len(groß))
	}

	// Binär: keine Bytes im JSON, nur die Metadaten.
	if err := os.WriteFile(filepath.Join(root, "bild.png"), []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a}, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = fs.Read("bild.png")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Binary || f.Content != "" {
		t.Errorf("binärdatei: %+v", f)
	}

	// Umlaute überleben das Abschneiden, statt als Ersatzzeichen zu enden.
	if err := os.WriteFile(filepath.Join(root, "umlaut.txt"), []byte(strings.Repeat("ä", MaxViewBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = fs.Read("umlaut.txt")
	if err != nil {
		t.Fatal(err)
	}
	if f.Binary {
		t.Error("utf-8-text als binär eingestuft")
	}
	if strings.ContainsRune(f.Content, '�') {
		t.Error("abgeschnittenes zeichen am ende")
	}
}

func TestUploadGrenze(t *testing.T) {
	fs, _ := newTestFS(t)
	zuGroß := strings.NewReader(strings.Repeat("x", MaxWriteBytes+1))
	if _, err := fs.Write("dick.bin", zuGroß); !errors.Is(err, ErrTooLarge) {
		t.Errorf("upload über der grenze: %v, erwartet ErrTooLarge", err)
	}
	// Und er hinterlässt keine halbe Datei.
	if _, err := fs.Read("dick.bin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("bruchstück liegengeblieben: %v", err)
	}
}

func TestOpenLiefertVolleDatei(t *testing.T) {
	fs, _ := newTestFS(t)
	inhalt := strings.Repeat("y", MaxViewBytes*2)
	if _, err := fs.Write("download.bin", strings.NewReader(inhalt)); err != nil {
		t.Fatal(err)
	}
	rc, info, err := fs.Open("download.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if info.Size() != int64(len(inhalt)) {
		t.Errorf("größe %d, erwartet %d", info.Size(), len(inhalt))
	}
	buf := make([]byte, 16)
	if n, err := rc.Read(buf); err != nil || n == 0 {
		t.Errorf("lesen: %d, %v", n, err)
	}
}
