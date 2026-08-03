package sandboxfs

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
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

// Die Vorschau-Art entscheidet, wie die Oberfläche eine Datei zeigt — und die
// Inline-Allowlist, was der Server überhaupt inline ausliefern darf. Beides
// hängt an einer Stelle, damit sich Anzeige und Auslieferung nicht auseinander
// entwickeln.
func TestVorschauArtUndInlineTypen(t *testing.T) {
	für := map[string]string{
		"notiz.md":        PreviewMarkdown,
		"README.MARKDOWN": PreviewMarkdown,
		"daten.csv":       PreviewCSV,
		"daten.TSV":       PreviewCSV,
		"bild.png":        PreviewImage,
		"foto.JPEG":       PreviewImage,
		"logo.svg":        PreviewImage,
		"handbuch.pdf":    PreviewPDF,
		"skript.sh":       "", // aus dem Namen nicht zu erkennen
		"ohne-endung":     "",
	}
	for name, want := range für {
		if got := PreviewKind(name); got != want {
			t.Errorf("PreviewKind(%q) = %q, erwartet %q", name, got, want)
		}
	}

	// Inline darf nur, was auf der Allowlist steht — HTML gehört bewusst nicht
	// dazu: es liefe als Dokument auf der Covey-Origin.
	for _, name := range []string{"seite.html", "skript.js", "notiz.md", "daten.csv", "archiv.zip"} {
		if got := InlineType(name); got != "" {
			t.Errorf("InlineType(%q) = %q, erwartet leer", name, got)
		}
	}
	if got := InlineType("bild.PNG"); got != "image/png" {
		t.Errorf("InlineType(bild.PNG) = %q", got)
	}
	if got := InlineType("handbuch.pdf"); got != "application/pdf" {
		t.Errorf("InlineType(handbuch.pdf) = %q", got)
	}
}

// Read trägt die Vorschau-Art mit — und überträgt Bilder nicht als Text: die
// Bytes holt die Oberfläche über den preview-Endpunkt.
func TestReadLiefertVorschauArt(t *testing.T) {
	fs, root := newTestFS(t)
	if err := os.WriteFile(filepath.Join(root, "notiz.md"), []byte("# Titel"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Ein Bild, das zufällig gültiges UTF-8 wäre: die Endung entscheidet, nicht
	// der Inhalt — sonst landete es im Editor.
	if err := os.WriteFile(filepath.Join(root, "bild.png"), []byte("nicht wirklich png"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "programm"), []byte{0x7f, 'E', 'L', 'F', 0x00}, 0o644); err != nil {
		t.Fatal(err)
	}

	md, err := fs.Read("notiz.md")
	if err != nil || md.Preview != PreviewMarkdown || md.Content != "# Titel" {
		t.Fatalf("markdown: %+v, %v", md, err)
	}
	img, err := fs.Read("bild.png")
	if err != nil || img.Preview != PreviewImage || img.Content != "" || !img.Binary {
		t.Fatalf("bild: %+v, %v", img, err)
	}
	bin, err := fs.Read("programm")
	if err != nil || bin.Preview != PreviewBinary {
		t.Fatalf("binär: %+v, %v", bin, err)
	}

	// In der Auflistung trägt jede Datei ihre Art — daran hängt das Symbol.
	list, err := fs.List("")
	if err != nil {
		t.Fatal(err)
	}
	arten := map[string]string{}
	for _, e := range list.Entries {
		arten[e.Name] = e.Preview
	}
	if arten["notiz.md"] != PreviewMarkdown || arten["bild.png"] != PreviewImage || arten["programm"] != "" {
		t.Errorf("arten in der auflistung: %v", arten)
	}
}

// Sammel-Download: mehrere Pfade und ganze Ordner in einem Archiv, benannt
// relativ zum gewählten Elternteil — wer „notizen" wählt, bekommt „notizen/…"
// und nicht dessen ausgeschütteten Inhalt.
func TestZipMehrerePfadeUndOrdner(t *testing.T) {
	fs, _ := newTestFS(t)
	for p, inhalt := range map[string]string{
		"notizen/a.md":      "A",
		"notizen/tief/b.md": "B",
		"lose.txt":          "lose",
		"anderes/egal.txt":  "egal",
	} {
		if _, err := fs.Write(p, strings.NewReader(inhalt)); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := fs.PlanZip([]string{"notizen", "lose.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 3 {
		t.Fatalf("erwartet 3 dateien im archiv, got %d", plan.Files)
	}
	if plan.Name != "dateien.zip" {
		t.Errorf("name bei mehreren pfaden: %q", plan.Name)
	}

	var buf bytes.Buffer
	if err := fs.WriteZip(&buf, plan); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	inhalte := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			inhalte[f.Name] = "<dir>"
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		inhalte[f.Name] = string(b)
	}
	for name, want := range map[string]string{
		"notizen/":          "<dir>",
		"notizen/a.md":      "A",
		"notizen/tief/b.md": "B",
		"lose.txt":          "lose",
	} {
		if inhalte[name] != want {
			t.Errorf("archiv-eintrag %q = %q, erwartet %q (alle: %v)", name, inhalte[name], want, inhalte)
		}
	}
	// Was nicht gewählt war, ist auch nicht drin.
	if _, drin := inhalte["anderes/egal.txt"]; drin {
		t.Errorf("nicht gewählter pfad im archiv: %v", inhalte)
	}

	// Ein einzelner Ordner benennt das Archiv nach sich.
	einzeln, err := fs.PlanZip([]string{"notizen"})
	if err != nil || einzeln.Name != "notizen.zip" {
		t.Errorf("einzelner ordner: name=%q, %v", einzeln.Name, err)
	}
	// Das ganze Home lässt sich am Stück ziehen.
	ganz, err := fs.PlanZip([]string{""})
	if err != nil || ganz.Files != 4 || ganz.Name != "home.zip" {
		t.Errorf("ganzes home: %d dateien, name=%q, %v", ganz.Files, ganz.Name, err)
	}
}

// Doppelt gewählt (Ordner UND eine Datei darin) darf nicht doppelt im Archiv
// landen — ZIP-Einträge mit gleichem Namen entpacken je nach Werkzeug anders.
func TestZipKeineDoppeltenEintraege(t *testing.T) {
	fs, _ := newTestFS(t)
	if _, err := fs.Write("ordner/datei.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	plan, err := fs.PlanZip([]string{"ordner", "ordner/datei.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 {
		t.Errorf("erwartet 1 datei, got %d", plan.Files)
	}
}

// Ein Symlink aus dem Home heraus gehört nicht ins Archiv: sonst packte der
// Download Dateien des Hosts mit ein.
func TestZipUeberspringtAusbrechendeSymlinks(t *testing.T) {
	fs, root := newTestFS(t)
	draußen := filepath.Join(filepath.Dir(root), "aussen")
	if err := os.MkdirAll(draußen, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(draußen, "beute.txt"), []byte("beute"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write("ordner/eigen.txt", strings.NewReader("eigen")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(draußen, filepath.Join(root, "ordner", "raus")); err != nil {
		t.Skipf("symlinks nicht verfügbar: %v", err)
	}

	plan, err := fs.PlanZip([]string{"ordner"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 {
		t.Fatalf("nur die eigene datei gehört ins archiv, got %d", plan.Files)
	}
	var buf bytes.Buffer
	if err := fs.WriteZip(&buf, plan); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("beute")) {
		t.Error("inhalt von ausserhalb des homes im archiv")
	}
}

// Grenzen greifen VOR dem ersten Byte — ein abgebrochener Strom hinterließe
// ein Archiv, dem man nicht ansieht, dass es unfertig ist.
func TestZipGrenzen(t *testing.T) {
	fs, _ := newTestFS(t)
	if _, err := fs.PlanZip([]string{"gibtsnicht"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unbekannter pfad: %v, erwartet ErrNotFound", err)
	}
	if _, err := fs.PlanZip([]string{"../draussen"}); err == nil {
		t.Error("pfad aus dem home heraus muss scheitern")
	}
}
