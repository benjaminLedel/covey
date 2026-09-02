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

// The core of the package: no path leads out of the root. Anything else would
// be a file browser over the entire host.
func TestEscapeAttempts(t *testing.T) {
	fs, root := newTestFS(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(root), "geheim.txt"), []byte("nicht für dich"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "unter"), 0o755); err != nil {
		t.Fatal(err)
	}

	// `..` in any spelling lands in the root at most, never above it — and thus
	// never at the file next to it.
	for _, p := range []string{"../geheim.txt", "unter/../../geheim.txt", "/../geheim.txt", "..%2Fgeheim.txt"} {
		if _, err := fs.Read(p); err == nil {
			t.Errorf("Read(%q) should have failed", p)
		}
	}
	// Absolute paths are read as relative to the root, not as a host path.
	if c, err := clean("/etc/passwd"); err != nil {
		t.Fatalf("absolute path: %v", err)
	} else if c != "etc/passwd" {
		t.Errorf("absolute path becomes %q, expected etc/passwd (below the root)", c)
	}
	// And it stays a claim about the file below the home, not about the host's.
	if _, err := fs.Read("/etc/passwd"); err == nil {
		t.Error("Read(\"/etc/passwd\") found something — that must not be the host's file")
	}
}

func TestSymlinkEscape(t *testing.T) {
	fs, root := newTestFS(t)
	outside := filepath.Join(filepath.Dir(root), "aussen")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "beute.txt"), []byte("beute"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "raus")); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}

	// Reading through the link: forbidden.
	if _, err := fs.Read("raus/beute.txt"); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("reading through symlink: %v, expected ErrInvalidPath", err)
	}
	// Writing through the link likewise — otherwise the link would be write
	// access to the host.
	if _, err := fs.Write("raus/neu.txt", strings.NewReader("x")); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("writing through symlink: %v, expected ErrInvalidPath", err)
	}
	// It stays visible nonetheless, including target and warning: hiding it
	// would withhold from the viewer something that really does lie in the home.
	list, err := fs.List("")
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Entries) != 1 || list.Entries[0].Name != "raus" {
		t.Fatalf("listing: %+v", list.Entries)
	}
	if !list.Entries[0].Outside || list.Entries[0].Symlink == "" {
		t.Errorf("symlink pointing outward not marked as such: %+v", list.Entries[0])
	}
}

func TestListSortsDirectoriesFirst(t *testing.T) {
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
	var names []string
	for _, e := range list.Entries {
		names = append(names, e.Name)
	}
	want := []string{"alpha", "zeta", "A.txt", "b.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("order %v, expected %v", names, want)
	}
	if !list.Exists {
		t.Error("exists=false although the home exists")
	}
}

// An agent that has never been woken has no home yet. That is not an error but
// an empty workplace — the UI should be able to show it.
func TestMissingHomeIsEmptyNotAnError(t *testing.T) {
	fs, err := New(filepath.Join(t.TempDir(), "nie-geweckt"), -1, -1)
	if err != nil {
		t.Fatal(err)
	}
	list, err := fs.List("")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if list.Exists || len(list.Entries) != 0 {
		t.Errorf("expected an empty, non-existent home: %+v", list)
	}
	// Subdirectories, however, really do not exist then.
	if _, err := fs.List("irgendwas"); !errors.Is(err, ErrNotFound) {
		t.Errorf("subdirectory: %v, expected ErrNotFound", err)
	}
	// An upload creates the home.
	if _, err := fs.Write("neu/file.txt", strings.NewReader("inhalt")); err != nil {
		t.Fatalf("writing into the missing home: %v", err)
	}
	if f, err := fs.Read("neu/file.txt"); err != nil || f.Content != "inhalt" {
		t.Errorf("reading after creation: %+v, %v", f, err)
	}
}

func TestWriteReadMoveDelete(t *testing.T) {
	fs, _ := newTestFS(t)

	if _, err := fs.Write("a/b/c.txt", strings.NewReader("hallo")); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Read("a/b/c.txt")
	if err != nil || f.Content != "hallo" || f.Binary || f.Truncated {
		t.Fatalf("read: %+v, %v", f, err)
	}
	if _, err := fs.Move("a/b/c.txt", "a/b/d.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Read("a/b/c.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("old path still there: %v", err)
	}
	// A taken target is not overwritten.
	if _, err := fs.Write("a/b/e.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Move("a/b/d.txt", "a/b/e.txt"); !errors.Is(err, ErrExists) {
		t.Errorf("moving onto a taken target: %v, expected ErrExists", err)
	}
	if err := fs.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.List("a"); !errors.Is(err, ErrNotFound) {
		t.Errorf("directory after deletion: %v", err)
	}
	// One does not delete the root itself from the file browser.
	if err := fs.Remove(""); !errors.Is(err, ErrInvalidPath) {
		t.Errorf("deleting the root: %v, expected ErrInvalidPath", err)
	}
}

func TestConfusingDirectoryAndFile(t *testing.T) {
	fs, _ := newTestFS(t)
	if _, err := fs.Mkdir("ordner"); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Mkdir("ordner"); !errors.Is(err, ErrExists) {
		t.Errorf("mkdir twice: %v, expected ErrExists", err)
	}
	if _, err := fs.Read("ordner"); !errors.Is(err, ErrIsDir) {
		t.Errorf("reading a directory: %v, expected ErrIsDir", err)
	}
	if _, err := fs.Write("ordner", strings.NewReader("x")); !errors.Is(err, ErrIsDir) {
		t.Errorf("overwriting a directory: %v, expected ErrIsDir", err)
	}
	if _, err := fs.Write("file.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.List("file.txt"); !errors.Is(err, ErrNotDir) {
		t.Errorf("listing a file: %v, expected ErrNotDir", err)
	}
}

func TestLargeAndBinaryFiles(t *testing.T) {
	fs, root := newTestFS(t)

	// Above the view limit: truncated, but readable.
	large := strings.Repeat("z", MaxViewBytes+1000)
	if err := os.WriteFile(filepath.Join(root, "groß.txt"), []byte(large), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := fs.Read("groß.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Truncated || len(f.Content) != MaxViewBytes {
		t.Errorf("truncation: truncated=%v len=%d", f.Truncated, len(f.Content))
	}
	if f.Size != int64(len(large)) {
		t.Errorf("size %d, expected %d", f.Size, len(large))
	}

	// Binary: no bytes in the JSON, only the metadata.
	if err := os.WriteFile(filepath.Join(root, "bild.png"), []byte{0x89, 'P', 'N', 'G', 0x00, 0x1a}, 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = fs.Read("bild.png")
	if err != nil {
		t.Fatal(err)
	}
	if !f.Binary || f.Content != "" {
		t.Errorf("binary file: %+v", f)
	}

	// Umlauts survive the truncation instead of ending up as replacement
	// characters.
	if err := os.WriteFile(filepath.Join(root, "umlaut.txt"), []byte(strings.Repeat("ä", MaxViewBytes)), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err = fs.Read("umlaut.txt")
	if err != nil {
		t.Fatal(err)
	}
	if f.Binary {
		t.Error("utf-8 text classified as binary")
	}
	if strings.ContainsRune(f.Content, '�') {
		t.Error("character cut in half at the end")
	}
}

func TestUploadLimit(t *testing.T) {
	fs, _ := newTestFS(t)
	tooLarge := strings.NewReader(strings.Repeat("x", MaxWriteBytes+1))
	if _, err := fs.Write("dick.bin", tooLarge); !errors.Is(err, ErrTooLarge) {
		t.Errorf("upload above the limit: %v, expected ErrTooLarge", err)
	}
	// And it leaves no half file behind.
	if _, err := fs.Read("dick.bin"); !errors.Is(err, ErrNotFound) {
		t.Errorf("fragment left behind: %v", err)
	}
}

func TestOpenReturnsFullFile(t *testing.T) {
	fs, _ := newTestFS(t)
	content := strings.Repeat("y", MaxViewBytes*2)
	if _, err := fs.Write("download.bin", strings.NewReader(content)); err != nil {
		t.Fatal(err)
	}
	rc, info, err := fs.Open("download.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if info.Size != int64(len(content)) {
		t.Errorf("size %d, expected %d", info.Size, len(content))
	}
	buf := make([]byte, 16)
	if n, err := rc.Read(buf); err != nil || n == 0 {
		t.Errorf("read: %d, %v", n, err)
	}
}

// The preview kind decides how the UI shows a file — and the inline allowlist
// decides what the server may deliver inline at all. Both hang on one place so
// that display and delivery do not drift apart.
func TestPreviewKindAndInlineTypes(t *testing.T) {
	cases := map[string]string{
		"notiz.md":        PreviewMarkdown,
		"README.MARKDOWN": PreviewMarkdown,
		"daten.csv":       PreviewCSV,
		"daten.TSV":       PreviewCSV,
		"bild.png":        PreviewImage,
		"foto.JPEG":       PreviewImage,
		"logo.svg":        PreviewImage,
		"handbuch.pdf":    PreviewPDF,
		"skript.sh":       "", // not recognisable from the name
		"ohne-endung":     "",
	}
	for name, want := range cases {
		if got := PreviewKind(name); got != want {
			t.Errorf("PreviewKind(%q) = %q, expected %q", name, got, want)
		}
	}

	// Only what is on the allowlist may go inline — HTML is deliberately not
	// part of it: it would run as a document on the covey origin.
	for _, name := range []string{"seite.html", "skript.js", "notiz.md", "daten.csv", "archiv.zip"} {
		if got := InlineType(name); got != "" {
			t.Errorf("InlineType(%q) = %q, expected empty", name, got)
		}
	}
	if got := InlineType("bild.PNG"); got != "image/png" {
		t.Errorf("InlineType(bild.PNG) = %q", got)
	}
	if got := InlineType("handbuch.pdf"); got != "application/pdf" {
		t.Errorf("InlineType(handbuch.pdf) = %q", got)
	}
}

// Read carries the preview kind along — and does not transfer images as text:
// the UI fetches those bytes through the preview endpoint.
func TestReadReturnsPreviewKind(t *testing.T) {
	fs, root := newTestFS(t)
	if err := os.WriteFile(filepath.Join(root, "notiz.md"), []byte("# Titel"), 0o644); err != nil {
		t.Fatal(err)
	}
	// An image that happens to be valid UTF-8: the extension decides, not the
	// content — otherwise it would end up in the editor.
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
		t.Fatalf("image: %+v, %v", img, err)
	}
	bin, err := fs.Read("programm")
	if err != nil || bin.Preview != PreviewBinary {
		t.Fatalf("binary: %+v, %v", bin, err)
	}

	// In the listing every file carries its kind — the icon hangs on that.
	list, err := fs.List("")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, e := range list.Entries {
		kinds[e.Name] = e.Preview
	}
	if kinds["notiz.md"] != PreviewMarkdown || kinds["bild.png"] != PreviewImage || kinds["programm"] != "" {
		t.Errorf("kinds in the listing: %v", kinds)
	}
}

// Bulk download: several paths and whole folders in one archive, named relative
// to the chosen parent — whoever selects "notizen" gets "notizen/…" and not its
// spilled-out content.
func TestZipMultiplePathsAndFolders(t *testing.T) {
	fs, _ := newTestFS(t)
	for p, content := range map[string]string{
		"notizen/a.md":      "A",
		"notizen/tief/b.md": "B",
		"lose.txt":          "lose",
		"anderes/egal.txt":  "egal",
	} {
		if _, err := fs.Write(p, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := fs.PlanZip([]string{"notizen", "lose.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 3 {
		t.Fatalf("expected 3 files in the archive, got %d", plan.Files)
	}
	if plan.Name != "files.zip" {
		t.Errorf("name for several paths: %q", plan.Name)
	}

	var buf bytes.Buffer
	if err := fs.WriteZip(&buf, plan); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]string{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			contents[f.Name] = "<dir>"
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		rc.Close()
		contents[f.Name] = string(b)
	}
	for name, want := range map[string]string{
		"notizen/":          "<dir>",
		"notizen/a.md":      "A",
		"notizen/tief/b.md": "B",
		"lose.txt":          "lose",
	} {
		if contents[name] != want {
			t.Errorf("archive entry %q = %q, expected %q (all: %v)", name, contents[name], want, contents)
		}
	}
	// What was not selected is not in there either.
	if _, in := contents["anderes/egal.txt"]; in {
		t.Errorf("unselected path in the archive: %v", contents)
	}

	// A single folder names the archive after itself.
	single, err := fs.PlanZip([]string{"notizen"})
	if err != nil || single.Name != "notizen.zip" {
		t.Errorf("single folder: name=%q, %v", single.Name, err)
	}
	// The whole home can be pulled in one piece.
	whole, err := fs.PlanZip([]string{""})
	if err != nil || whole.Files != 4 || whole.Name != "home.zip" {
		t.Errorf("whole home: %d files, name=%q, %v", whole.Files, whole.Name, err)
	}
}

// Selected twice (folder AND a file within it) must not end up twice in the
// archive — ZIP entries with the same name unpack differently depending on the
// tool.
func TestZipNoDuplicateEntries(t *testing.T) {
	fs, _ := newTestFS(t)
	if _, err := fs.Write("ordner/file.txt", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	plan, err := fs.PlanZip([]string{"ordner", "ordner/file.txt"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 {
		t.Errorf("expected 1 file, got %d", plan.Files)
	}
}

// A symlink pointing out of the home does not belong in the archive: otherwise
// the download would pack up files of the host as well.
func TestZipSkipsEscapingSymlinks(t *testing.T) {
	fs, root := newTestFS(t)
	outside := filepath.Join(filepath.Dir(root), "aussen")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "beute.txt"), []byte("beute"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Write("ordner/eigen.txt", strings.NewReader("eigen")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "ordner", "raus")); err != nil {
		t.Skipf("symlinks not available: %v", err)
	}

	plan, err := fs.PlanZip([]string{"ordner"})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Files != 1 {
		t.Fatalf("only the own file belongs in the archive, got %d", plan.Files)
	}
	var buf bytes.Buffer
	if err := fs.WriteZip(&buf, plan); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("beute")) {
		t.Error("content from outside the home in the archive")
	}
}

// Limits take effect BEFORE the first byte — an aborted stream would leave an
// archive that does not look unfinished.
func TestZipLimits(t *testing.T) {
	fs, _ := newTestFS(t)
	if _, err := fs.PlanZip([]string{"gibtsnicht"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown path: %v, expected ErrNotFound", err)
	}
	if _, err := fs.PlanZip([]string{"../draussen"}); err == nil {
		t.Error("a path leading out of the home must fail")
	}
}
