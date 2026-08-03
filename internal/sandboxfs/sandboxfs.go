// Package sandboxfs öffnet das persistente Home eines Agenten als Dateibaum:
// durchsehen, lesen, hochladen, ändern, löschen. Es ist der Arbeitsplatz des
// Agenten (spec/02) — Compute ist ephemer, das Home überlebt. Weil es auf dem
// Host liegt und nur in die Sandbox gemountet wird, funktioniert der Zugriff
// unabhängig davon, ob die Sandbox gerade läuft; ein schlafender Agent ist
// kein blinder Fleck.
//
// Die gesamte Fläche ist Pfad-Arbeit auf fremdem Input, deshalb liegt das
// ganze Paket um eine Regel herum: **kein Weg zeigt aus der Wurzel heraus.**
// Jeder Pfad wird normalisiert (`..` fällt weg) und anschließend gegen den
// tiefsten existierenden Vorfahren geprüft — ein Symlink, der aus dem Home
// zeigt, ist ein Ausbruchsversuch, kein Abkürzung.
package sandboxfs

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// Grenzen. Sie sind bewusst großzügig, aber endlich: ein Home ist ein
// Arbeitsplatz, kein Dateiserver.
const (
	// MaxViewBytes ist der Ausschnitt, den die Textansicht überträgt. Größere
	// Dateien kommen abgeschnitten (Truncated) — vollständig gibt es sie über
	// den Download.
	MaxViewBytes = 512 << 10 // 512 KiB
	// MaxWriteBytes begrenzt eine einzelne hochgeladene oder gespeicherte Datei.
	MaxWriteBytes = 64 << 20 // 64 MiB
	// MaxEntries begrenzt die Einträge einer Auflistung. Ein Verzeichnis mit
	// mehr Einträgen kommt gekürzt zurück, statt die UI zu erschlagen.
	MaxEntries = 2000
	// MaxZipFiles/MaxZipBytes begrenzen ein Sammel-Download. Beides wird VOR
	// dem ersten Byte geprüft: ein Strom, der auf halber Strecke abbricht,
	// hinterlässt ein kaputtes Archiv, dem man nicht ansieht, dass es unfertig
	// ist — die Grenze muss eine Fehlermeldung sein, kein Torso.
	MaxZipFiles = 20000
	MaxZipBytes = 2 << 30 // 2 GiB unkomprimiert
	// sniffBytes ist der Ausschnitt, an dem Text von Binär unterschieden wird.
	sniffBytes = 8000
)

var (
	// ErrNotFound: der Pfad existiert nicht.
	ErrNotFound = errors.New("pfad nicht gefunden")
	// ErrInvalidPath: der Pfad zeigt aus dem Home heraus oder ist unbrauchbar.
	ErrInvalidPath = errors.New("ungültiger pfad")
	// ErrTooLarge: die Datei überschreitet die Grenze.
	ErrTooLarge = errors.New("datei zu groß")
	// ErrNotDir / ErrIsDir: die Operation passt nicht zum Typ des Ziels.
	ErrNotDir = errors.New("kein verzeichnis")
	ErrIsDir  = errors.New("ist ein verzeichnis")
	// ErrExists: das Ziel ist belegt.
	ErrExists = errors.New("existiert bereits")
	// ErrTooMany: der Sammel-Download umfasst zu viele Dateien.
	ErrTooMany = errors.New("zu viele dateien")
)

// Vorschau-Arten. Welche Art eine Datei hat, entscheidet hier *eine* Stelle —
// die Oberfläche wählt danach ihre Darstellung, statt selbst noch einmal
// Endungen zu raten und irgendwann etwas anderes zu meinen als der Server,
// der die Bytes ausliefert.
const (
	PreviewText     = "text"     // im Editor bearbeitbar
	PreviewMarkdown = "markdown" // gerendert oder als Quelltext
	PreviewImage    = "image"    // inline über den preview-Endpunkt
	PreviewPDF      = "pdf"      // inline, eingebettet
	PreviewCSV      = "csv"      // als Tabelle oder Quelltext
	PreviewBinary   = "binary"   // nur herunterladen
)

// inlineTypes ist die Allowlist der Typen, die *inline* ausgeliefert werden
// dürfen — alles andere geht ausschließlich als Anhang raus. Bewusst kurz und
// fail-closed: Inline-Auslieferung aus einem Agenten-Home heißt, fremde Bytes
// auf der Covey-Origin darzustellen. Bilder und PDF sind es wert, HTML nicht.
//
// SVG ist dabei, weil es im <img>-Kontext kein Skript ausführt; gegen den
// direkten Aufruf der URL sichert der Handler zusätzlich mit einer
// sandbox-CSP ab (files.go).
var inlineTypes = map[string]string{
	".png":  "image/png",
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".gif":  "image/gif",
	".webp": "image/webp",
	".avif": "image/avif",
	".bmp":  "image/bmp",
	".ico":  "image/x-icon",
	".svg":  "image/svg+xml",
	".pdf":  "application/pdf",
}

// markdownExts/csvExts sind die Text-Arten mit eigener Darstellung.
var markdownExts = map[string]bool{".md": true, ".markdown": true, ".mdx": true}

var csvExts = map[string]bool{".csv": true, ".tsv": true}

// PreviewKind bestimmt die Vorschau-Art aus dem Dateinamen. Leeres Ergebnis =
// aus dem Namen nicht zu erkennen; dann entscheidet der Inhalt (Text oder
// binär), siehe Read.
func PreviewKind(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case markdownExts[ext]:
		return PreviewMarkdown
	case csvExts[ext]:
		return PreviewCSV
	case ext == ".pdf":
		return PreviewPDF
	case inlineTypes[ext] != "":
		return PreviewImage
	}
	return ""
}

// InlineType liefert den Content-Type, unter dem eine Datei inline
// ausgeliefert werden darf — leer heißt: nur als Anhang.
func InlineType(name string) string {
	return inlineTypes[strings.ToLower(filepath.Ext(name))]
}

// FS ist der Dateibaum eines Agenten-Homes.
//
// UID/GID sind der Besitzer *in der Sandbox* (der User `agent` im Image). Die
// Control Plane läuft im Deployment als root; ohne das Nachziehen der
// Ownership gehörten hochgeladene Dateien root, und der Agent könnte seine
// eigenen Dateien nicht mehr ändern. -1 = nicht setzen (lokale Entwicklung,
// wo Docker Desktop die Ownership ohnehin mappt).
type FS struct {
	root string
	UID  int
	GID  int
}

// New öffnet root als Dateibaum. Das Verzeichnis muss nicht existieren — ein
// Agent, der noch nie geweckt wurde, hat noch kein Home; die Auflistung ist
// dann leer statt ein Fehler.
func New(root string, uid, gid int) (*FS, error) {
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("sandboxfs: leere wurzel")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return &FS{root: abs, UID: uid, GID: gid}, nil
}

// Root ist der Host-Pfad des Homes — für Anzeige und Diagnose.
func (f *FS) Root() string { return f.root }

// Entry ist ein Eintrag einer Auflistung.
type Entry struct {
	Name string `json:"name"`
	// Path ist der Pfad relativ zum Home, mit „/" als Trenner.
	Path    string `json:"path"`
	IsDir   bool   `json:"is_dir"`
	Size    int64  `json:"size"`
	Mode    string `json:"mode"`
	ModTime string `json:"mod_time"`
	// Symlink trägt das Ziel, wenn der Eintrag ein Link ist. Ein Link, der aus
	// dem Home zeigt, wird angezeigt, aber nicht geöffnet (Outside).
	Symlink string `json:"symlink,omitempty"`
	Outside bool   `json:"outside,omitempty"`
	// Preview ist die Vorschau-Art nach Dateiname (leer = erst beim Öffnen
	// entscheidbar). In der Liste trägt sie das Symbol der Zeile.
	Preview string `json:"preview,omitempty"`
}

// Listing ist eine Verzeichnisauflistung.
type Listing struct {
	Path string `json:"path"`
	// Exists=false heißt: das Home wurde noch nie angelegt (Agent nie geweckt).
	Exists    bool    `json:"exists"`
	Truncated bool    `json:"truncated"`
	Entries   []Entry `json:"entries"`
}

// File ist der Inhalt einer Datei für die Ansicht im Browser.
type File struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Mode      string `json:"mode"`
	ModTime   string `json:"mod_time"`
	Binary    bool   `json:"binary"`
	Truncated bool   `json:"truncated"`
	// Preview sagt der Oberfläche, wie die Datei zu zeigen ist: text,
	// markdown, csv (Content trägt den Text), image, pdf (Content ist leer,
	// die Bytes kommen über den preview-Endpunkt) oder binary.
	Preview string `json:"preview"`
	Content string `json:"content"`
}

// List liefert die Einträge eines Verzeichnisses, Verzeichnisse zuerst und
// innerhalb dessen alphabetisch — die Sortierung, die ein Dateibrowser
// erwartet.
func (f *FS) List(rel string) (Listing, error) {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return Listing{}, err
	}
	out := Listing{Path: clean, Entries: []Entry{}}

	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		// Nur die Wurzel darf fehlen: das Home entsteht beim ersten Wake.
		if clean == "" {
			return out, nil
		}
		return Listing{}, ErrNotFound
	}
	if err != nil {
		return Listing{}, err
	}
	if !info.IsDir() {
		return Listing{}, ErrNotDir
	}
	out.Exists = true

	des, err := os.ReadDir(abs)
	if err != nil {
		return Listing{}, err
	}
	sort.Slice(des, func(i, j int) bool {
		if des[i].IsDir() != des[j].IsDir() {
			return des[i].IsDir()
		}
		return strings.ToLower(des[i].Name()) < strings.ToLower(des[j].Name())
	})
	if len(des) > MaxEntries {
		des = des[:MaxEntries]
		out.Truncated = true
	}
	for _, de := range des {
		out.Entries = append(out.Entries, f.entry(abs, clean, de.Name()))
	}
	return out, nil
}

// entry baut den Eintrag zu einem Namen; Symlinks werden als solche gezeigt
// (samt Ziel und dem Hinweis, ob sie aus dem Home hinauszeigen).
func (f *FS) entry(dirAbs, dirRel, name string) Entry {
	full := filepath.Join(dirAbs, name)
	e := Entry{Name: name, Path: path.Join(dirRel, name)}

	li, err := os.Lstat(full)
	if err != nil {
		return e
	}
	if li.Mode()&os.ModeSymlink != 0 {
		if target, err := os.Readlink(full); err == nil {
			e.Symlink = target
		}
		e.Outside = f.ensureInside(full) != nil
		// Für Größe/Typ auf das Ziel schauen — es sei denn, es zeigt hinaus.
		if !e.Outside {
			if si, err := os.Stat(full); err == nil {
				li = si
			}
		}
	}
	e.IsDir = li.IsDir()
	e.Size = li.Size()
	e.Mode = li.Mode().String()
	e.ModTime = li.ModTime().UTC().Format("2006-01-02T15:04:05Z")
	if !e.IsDir {
		e.Preview = PreviewKind(name)
	}
	return e
}

// Read liefert eine Datei für die Ansicht: Text bis MaxViewBytes (darüber
// abgeschnitten), Binäres nur als Metadaten — Bytes im JSON zu transportieren
// hilft niemandem, dafür gibt es Open (Download).
func (f *FS) Read(rel string) (File, error) {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return File{}, err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return File{}, ErrNotFound
	}
	if err != nil {
		return File{}, err
	}
	if info.IsDir() {
		return File{}, ErrIsDir
	}
	out := File{
		Path:    clean,
		Size:    info.Size(),
		Mode:    info.Mode().String(),
		ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
		Preview: PreviewKind(info.Name()),
	}
	// Bild und PDF werden nicht als Text übertragen: ihre Bytes holt sich die
	// Oberfläche über den preview-Endpunkt. Ein Base64-Umweg durchs JSON
	// verdreifachte nur das Volumen.
	if out.Preview == PreviewImage || out.Preview == PreviewPDF {
		out.Binary = true
		return out, nil
	}

	fh, err := os.Open(abs)
	if err != nil {
		return File{}, err
	}
	defer fh.Close()

	buf := make([]byte, MaxViewBytes+1)
	n, err := io.ReadFull(fh, buf)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return File{}, err
	}
	data := buf[:n]
	if len(data) > MaxViewBytes {
		data = data[:MaxViewBytes]
		out.Truncated = true
	}
	if isBinary(data) {
		out.Binary = true
		out.Preview = PreviewBinary
		return out, nil
	}
	// Abgeschnitten wird auf Byte-Ebene; ein halbes UTF-8-Zeichen am Ende
	// würde als Ersatzzeichen durchschlagen, deshalb weg damit.
	if out.Truncated {
		for len(data) > 0 && !utf8.Valid(data) {
			data = data[:len(data)-1]
		}
	}
	if out.Preview == "" {
		out.Preview = PreviewText
	}
	out.Content = string(data)
	return out, nil
}

// Open liefert die Datei zum Herunterladen — unverändert, in voller Länge.
func (f *FS) Open(rel string) (io.ReadCloser, os.FileInfo, error) {
	abs, _, err := f.resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	info, err := os.Stat(abs)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, err
	}
	if info.IsDir() {
		return nil, nil, ErrIsDir
	}
	fh, err := os.Open(abs)
	if err != nil {
		return nil, nil, err
	}
	return fh, info, nil
}

// ZipPlan ist das fertig ausgemessene Sammel-Archiv: was hineinkommt, wie viel
// es ist und wie es heißen soll. Getrennt vom Schreiben, damit Grenzen und
// Fehler (nichts gefunden, zu groß) noch als HTTP-Status gehen können — sind
// die ersten Bytes raus, bleibt nur noch ein Abbruch mitten im Download.
type ZipPlan struct {
	// Name ist der vorgeschlagene Dateiname des Archivs (ohne Pfad).
	Name string
	// Files/Bytes ist der Umfang: Dateien und unkomprimierte Gesamtgröße.
	Files int
	Bytes int64
	items []zipItem
}

// zipItem ist ein Eintrag im Archiv: Quelle auf der Platte, Name im Archiv.
type zipItem struct {
	abs  string
	name string // Pfad im Archiv, „/" als Trenner
	dir  bool
	size int64
	mod  time.Time
}

// PlanZip sammelt, was ein Archiv über diese Pfade enthielte. Verzeichnisse
// kommen mitsamt Inhalt hinein, benannt relativ zu ihrem Elternteil — wer
// „notizen" auswählt, bekommt ein Archiv mit einem Ordner „notizen" darin und
// nicht dessen ausgeschüttete Einzelteile.
func (f *FS) PlanZip(rels []string) (ZipPlan, error) {
	var plan ZipPlan
	seen := map[string]bool{}

	for _, rel := range rels {
		abs, clean, err := f.resolve(rel)
		if err != nil {
			return ZipPlan{}, err
		}
		info, err := os.Stat(abs)
		if errors.Is(err, os.ErrNotExist) {
			return ZipPlan{}, ErrNotFound
		}
		if err != nil {
			return ZipPlan{}, err
		}
		// Der Name im Archiv ist relativ zum Elternteil des gewählten Pfads;
		// bei der Wurzel selbst relativ zur Wurzel.
		base := path.Dir(clean)
		if clean == "" || base == "." {
			base = ""
		}
		if err := f.walkZip(abs, clean, base, info, seen, &plan); err != nil {
			return ZipPlan{}, err
		}
	}
	if plan.Files == 0 && len(plan.items) == 0 {
		return ZipPlan{}, ErrNotFound
	}
	plan.Name = zipName(rels)
	return plan, nil
}

// walkZip nimmt einen Pfad (Datei oder Verzeichnis) rekursiv ins Archiv auf.
func (f *FS) walkZip(abs, clean, base string, info os.FileInfo, seen map[string]bool, plan *ZipPlan) error {
	if seen[abs] {
		return nil // doppelte Auswahl (Ordner + Datei darin) nur einmal
	}
	seen[abs] = true

	name := clean
	if base != "" {
		name = strings.TrimPrefix(clean, base+"/")
	}
	if name == "" {
		name = path.Base(clean)
	}

	if !info.IsDir() {
		if plan.Files+1 > MaxZipFiles {
			return ErrTooMany
		}
		if plan.Bytes+info.Size() > MaxZipBytes {
			return ErrTooLarge
		}
		plan.items = append(plan.items, zipItem{abs: abs, name: name, size: info.Size(), mod: info.ModTime()})
		plan.Files++
		plan.Bytes += info.Size()
		return nil
	}

	plan.items = append(plan.items, zipItem{abs: abs, name: name + "/", dir: true, mod: info.ModTime()})
	des, err := os.ReadDir(abs)
	if err != nil {
		return err
	}
	for _, de := range des {
		childAbs := filepath.Join(abs, de.Name())
		// Ein Symlink, der aus dem Home zeigt, gehört nicht ins Archiv: sonst
		// packte der Download Dateien des Hosts mit ein.
		if err := f.ensureInside(childAbs); err != nil {
			continue
		}
		childInfo, err := os.Stat(childAbs)
		if err != nil {
			continue // kaputter Link o. Ä. — überspringen, nicht abbrechen
		}
		if err := f.walkZip(childAbs, path.Join(clean, de.Name()), base, childInfo, seen, plan); err != nil {
			return err
		}
	}
	return nil
}

// WriteZip schreibt das geplante Archiv als Strom.
func (f *FS) WriteZip(w io.Writer, plan ZipPlan) error {
	zw := zip.NewWriter(w)
	for _, it := range plan.items {
		hdr := &zip.FileHeader{Name: it.name, Modified: it.mod}
		if it.dir {
			hdr.SetMode(0o755 | os.ModeDir)
			if _, err := zw.CreateHeader(hdr); err != nil {
				return err
			}
			continue
		}
		hdr.Method = zip.Deflate
		hdr.SetMode(0o644)
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			return err
		}
		src, err := os.Open(it.abs)
		if err != nil {
			// Zwischen Planen und Schreiben verschwunden — der Agent arbeitet
			// weiter. Kein Grund, das ganze Archiv fallenzulassen.
			continue
		}
		_, err = io.Copy(dst, src)
		src.Close()
		if err != nil {
			return err
		}
	}
	return zw.Close()
}

// zipName leitet den Dateinamen des Archivs aus der Auswahl ab: bei genau
// einem Pfad dessen Name, sonst ein Sammelname.
func zipName(rels []string) string {
	if len(rels) == 1 {
		clean := strings.TrimPrefix(path.Clean("/"+rels[0]), "/")
		if clean != "" && clean != "." {
			return path.Base(clean) + ".zip"
		}
		return "home.zip"
	}
	return "dateien.zip"
}

// Write legt eine Datei an oder ersetzt sie. Fehlende Elternverzeichnisse
// entstehen mit — ein Upload nach `projekt/neu/` soll nicht daran scheitern,
// dass es `neu` noch nicht gibt.
func (f *FS) Write(rel string, r io.Reader) (Entry, error) {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return Entry{}, err
	}
	if clean == "" {
		return Entry{}, ErrInvalidPath
	}
	if info, err := os.Stat(abs); err == nil && info.IsDir() {
		return Entry{}, ErrIsDir
	}
	if err := f.mkdirAll(filepath.Dir(abs)); err != nil {
		return Entry{}, err
	}

	// Erst in eine Nachbardatei schreiben, dann umbenennen: ein abgebrochener
	// Upload hinterlässt sonst eine halbe Datei unter dem richtigen Namen —
	// und der Agent arbeitet mit ihr weiter.
	tmp, err := os.CreateTemp(filepath.Dir(abs), ".covey-upload-*")
	if err != nil {
		return Entry{}, err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op nach erfolgreichem Rename

	n, err := io.Copy(tmp, io.LimitReader(r, MaxWriteBytes+1))
	if err != nil {
		tmp.Close()
		return Entry{}, err
	}
	if n > MaxWriteBytes {
		tmp.Close()
		return Entry{}, ErrTooLarge
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return Entry{}, err
	}
	f.chown(tmpName)
	if err := os.Rename(tmpName, abs); err != nil {
		return Entry{}, err
	}
	return f.entry(filepath.Dir(abs), path.Dir(clean), path.Base(clean)), nil
}

// Mkdir legt ein Verzeichnis an (samt fehlender Eltern).
func (f *FS) Mkdir(rel string) (Entry, error) {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return Entry{}, err
	}
	if clean == "" {
		return Entry{}, ErrInvalidPath
	}
	if _, err := os.Lstat(abs); err == nil {
		return Entry{}, ErrExists
	}
	if err := f.mkdirAll(abs); err != nil {
		return Entry{}, err
	}
	return f.entry(filepath.Dir(abs), path.Dir(clean), path.Base(clean)), nil
}

// Remove löscht eine Datei oder ein Verzeichnis samt Inhalt. Die Wurzel selbst
// ist tabu: das Home eines Agenten löscht man nicht aus dem Dateibrowser.
func (f *FS) Remove(rel string) error {
	abs, clean, err := f.resolve(rel)
	if err != nil {
		return err
	}
	if clean == "" {
		return ErrInvalidPath
	}
	if _, err := os.Lstat(abs); errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	return os.RemoveAll(abs)
}

// Move benennt um bzw. verschiebt. Ein belegtes Ziel wird nicht überschrieben —
// ein Verschieben, das still etwas anderes löscht, ist eine Falle.
func (f *FS) Move(fromRel, toRel string) (Entry, error) {
	fromAbs, fromClean, err := f.resolve(fromRel)
	if err != nil {
		return Entry{}, err
	}
	toAbs, toClean, err := f.resolve(toRel)
	if err != nil {
		return Entry{}, err
	}
	if fromClean == "" || toClean == "" {
		return Entry{}, ErrInvalidPath
	}
	if _, err := os.Lstat(fromAbs); errors.Is(err, os.ErrNotExist) {
		return Entry{}, ErrNotFound
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return Entry{}, ErrExists
	}
	if err := f.mkdirAll(filepath.Dir(toAbs)); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(fromAbs, toAbs); err != nil {
		return Entry{}, err
	}
	return f.entry(filepath.Dir(toAbs), path.Dir(toClean), path.Base(toClean)), nil
}

// mkdirAll legt die Kette an und zieht die Ownership auf jedem neu
// entstandenen Verzeichnis nach (MkdirAll allein gäbe sie der Control Plane).
func (f *FS) mkdirAll(abs string) error {
	if !strings.HasPrefix(abs, f.root) {
		return ErrInvalidPath
	}
	var missing []string
	for p := abs; len(p) >= len(f.root); p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		if p == f.root {
			break
		}
	}
	if err := os.MkdirAll(abs, 0o755); err != nil {
		return err
	}
	for _, p := range missing {
		f.chown(p)
	}
	return nil
}

// chown zieht die Sandbox-Ownership nach — best effort: als normaler User
// (lokale Entwicklung) scheitert es, und das ist dort auch egal.
func (f *FS) chown(abs string) {
	if f.UID < 0 || f.GID < 0 {
		return
	}
	_ = os.Chown(abs, f.UID, f.GID)
}

// resolve normalisiert einen vom Client kommenden Pfad und stellt sicher, dass
// er im Home bleibt. Rückgabe: absoluter Host-Pfad und der bereinigte
// Relativpfad („" = Wurzel).
func (f *FS) resolve(rel string) (abs, clean string, err error) {
	rel = strings.TrimSpace(rel)
	if strings.ContainsRune(rel, 0) {
		return "", "", ErrInvalidPath
	}
	// Über den Umweg über „/" fällt jedes `..` weg, auch ein führendes.
	clean = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	if clean == "." {
		clean = ""
	}
	abs = f.root
	if clean != "" {
		abs = filepath.Join(f.root, filepath.FromSlash(clean))
	}
	if err := f.ensureInside(abs); err != nil {
		return "", "", err
	}
	return abs, clean, nil
}

// ensureInside prüft den Pfad gegen Symlink-Ausbrüche: der tiefste
// *existierende* Vorfahre wird aufgelöst und muss in der Wurzel liegen. Noch
// nicht existierende Endstücke können keine Links sein, deshalb reicht das.
func (f *FS) ensureInside(abs string) error {
	rootReal, err := filepath.EvalSymlinks(f.root)
	if err != nil {
		// Home noch nicht angelegt: dann gibt es auch keinen Link, über den
		// jemand ausbrechen könnte — der rein textuelle Vergleich genügt.
		if errors.Is(err, os.ErrNotExist) {
			if within(f.root, abs) {
				return nil
			}
			return ErrInvalidPath
		}
		return err
	}
	probe := abs
	for {
		real, err := filepath.EvalSymlinks(probe)
		if err == nil {
			if !within(rootReal, real) {
				return ErrInvalidPath
			}
			return nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return ErrInvalidPath
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return ErrInvalidPath
		}
		probe = parent
	}
}

func within(root, p string) bool {
	return p == root || strings.HasPrefix(p, root+string(os.PathSeparator))
}

// isBinary entscheidet an einer Stichprobe, ob der Inhalt als Text taugt: ein
// NUL-Byte oder kaputtes UTF-8 heißt binär. Bewusst grob — die Frage ist nur,
// ob der Editor die Datei zeigen darf.
func isBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	sniff := data
	if len(sniff) > sniffBytes {
		sniff = sniff[:sniffBytes]
	}
	for _, b := range sniff {
		if b == 0 {
			return true
		}
	}
	// Am Rand des Ausschnitts kann ein Zeichen zerschnitten sein — bis zu drei
	// Bytes zurücknehmen, bevor das als „binär" zählt.
	for i := 0; i < 3 && len(sniff) > 0 && !utf8.Valid(sniff); i++ {
		sniff = sniff[:len(sniff)-1]
	}
	return !utf8.Valid(sniff)
}
