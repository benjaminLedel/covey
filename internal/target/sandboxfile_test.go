package target

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The reason for the shared helper: two mails with a `rechnung.pdf` each ended
// up on the same path one after the other, the second silently overwrote the
// first. An agent that had remembered the path then read a foreign document —
// without anything going visibly wrong anywhere (GitHub #2, point 1).
func TestDateiAblegenKollision(t *testing.T) {
	work := t.TempDir()

	first, err := StoreFile(work, "attachments", "rechnung.pdf", []byte("eins"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(first.Path) != "rechnung.pdf" {
		t.Fatalf("unexpected first name: %s", first.Path)
	}

	// Different content, same name → its own file, the first one stays.
	second, err := StoreFile(work, "attachments", "rechnung.pdf", []byte("zwei"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if second.Path == first.Path {
		t.Fatal("second attachment overwrites the first")
	}
	if filepath.Base(second.Path) != "rechnung-2.pdf" {
		t.Fatalf("second name = %s, expected rechnung-2.pdf", filepath.Base(second.Path))
	}
	if inhalt, _ := os.ReadFile(first.Path); string(inhalt) != "eins" {
		t.Fatalf("first file altered: %q", inhalt)
	}

	// A third one with yet another content keeps counting.
	third, _ := StoreFile(work, "attachments", "rechnung.pdf", []byte("drei"), "application/pdf")
	if filepath.Base(third.Path) != "rechnung-3.pdf" {
		t.Fatalf("third name = %s", filepath.Base(third.Path))
	}

	// The same attachment fetched once more: byte-identical, so no second copy
	// — otherwise copies would pile up with every heartbeat.
	again, err := StoreFile(work, "attachments", "rechnung.pdf", []byte("eins"), "application/pdf")
	if err != nil {
		t.Fatal(err)
	}
	if again.Path != first.Path {
		t.Fatalf("identical content creates a copy: %s", again.Path)
	}
}

// The file name comes from the sender. It must not lead out of the sandbox.
func TestDateiAblegenNameGehaertet(t *testing.T) {
	work := t.TempDir()

	for _, name := range []string{"../../etc/passwd", "/etc/passwd", "..", ".", "   "} {
		file, err := StoreFile(work, "attachments", name, []byte("x"), "")
		if err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		dir := filepath.Join(work, "attachments")
		if filepath.Dir(file.Path) != dir {
			t.Fatalf("%q ends up outside: %s", name, file.Path)
		}
		if strings.Contains(filepath.Base(file.Path), "..") {
			t.Fatalf("%q keeps traversal parts: %s", name, file.Path)
		}
	}
}

// Without a sandbox there is no target — that must be an error and not a write
// attempt relative to the server's working directory.
func TestDateiAblegenOhneWorkdir(t *testing.T) {
	if _, err := StoreFile("", "attachments", "x.txt", []byte("x"), ""); err == nil {
		t.Fatal("no error without a working directory")
	}
}

func TestStromAblegen(t *testing.T) {
	work := t.TempDir()

	file, err := StoreStream(work, "uploads", "bild.png", strings.NewReader("inhalt"), 1<<20, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if file.Bytes != 6 {
		t.Fatalf("Bytes = %d", file.Bytes)
	}
	if !strings.Contains(file.Hint, "vision") {
		t.Fatalf("image hint missing: %s", file.Hint)
	}

	// Second fetch: the stream cannot be compared, so it gets its own file —
	// but must under no circumstances overwrite the first.
	second, err := StoreStream(work, "uploads", "bild.png", strings.NewReader("anders"), 1<<20, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if second.Path == file.Path {
		t.Fatal("second upload overwrites the first")
	}

	// Above the limit: abort, and nothing is left behind.
	tooLarge, err := StoreStream(work, "uploads", "gross.bin", strings.NewReader(strings.Repeat("x", 100)), 10, "")
	if err == nil {
		t.Fatal("limit not enforced")
	}
	if tooLarge.Path != "" {
		t.Fatalf("result despite error: %+v", tooLarge)
	}
	if _, err := os.Stat(filepath.Join(work, "uploads", "gross.bin")); !os.IsNotExist(err) {
		t.Fatal("aborted file is left behind")
	}
}

func TestMaxBytesAusEnv(t *testing.T) {
	const env = "COVEY_TEST_MAX_MB"

	t.Setenv(env, "")
	if got := MaxBytesFromEnv(env, 25, 1024); got != 25<<20 {
		t.Errorf("default: %d", got)
	}
	t.Setenv(env, "2")
	if got := MaxBytesFromEnv(env, 25, 1024); got != 2<<20 {
		t.Errorf("adoption: %d", got)
	}
	// Clamped instead of silently back to the default (GitHub #2, point 3).
	t.Setenv(env, "2048")
	if got := MaxBytesFromEnv(env, 25, 1024); got != 1024<<20 {
		t.Errorf("clamping: %d", got)
	}
	// The overflow protection stays: absurd values also end up at maxMB.
	t.Setenv(env, "8796093022208")
	if got := MaxBytesFromEnv(env, 25, 1024); got != 1024<<20 {
		t.Errorf("overflow: %d", got)
	}
	for _, v := range []string{"0", "-1", "viel"} {
		t.Setenv(env, v)
		if got := MaxBytesFromEnv(env, 25, 1024); got != 25<<20 {
			t.Errorf("value %q: %d, expected the default", v, got)
		}
	}
}

func TestHinweis(t *testing.T) {
	if h := Hint("/w/a.png", "image/png"); !strings.Contains(h, "Image is stored") {
		t.Errorf("image: %s", h)
	}
	if h := Hint("/w/a.pdf", "application/pdf"); !strings.Contains(h, "content type application/pdf") {
		t.Errorf("file: %s", h)
	}
	// Without a content type no empty pair of parentheses.
	if h := Hint("/w/a.bin", ""); strings.Contains(h, "()") {
		t.Errorf("empty parentheses: %s", h)
	}
}
