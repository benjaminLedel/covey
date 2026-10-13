package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadMessageFiles: the paths of a covey/message (#586) are read the way
// the agent writes them, and the limits hold before a byte is read.
func TestReadMessageFiles(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(home, "out"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(rel string, data []byte) string {
		p := filepath.Join(home, rel)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("out/a.png", []byte("A"))
	abs := write("b.txt", []byte("BB"))

	files, err := readMessageFiles(home, []string{"~/out/a.png", "out/a.png", abs})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || files[0].Name != "a.png" || string(files[0].Data) != "A" ||
		files[1].Name != "a.png" || files[2].Name != "b.txt" || string(files[2].Data) != "BB" {
		t.Fatalf("read as written: %+v", files)
	}
	if files, err := readMessageFiles(home, nil); err != nil || files != nil {
		t.Fatalf("no paths, no files: %v %v", files, err)
	}

	for name, tc := range map[string]struct {
		paths []string
		want  string
	}{
		"missing":   {[]string{"~/out/nope.png"}, "not found"},
		"directory": {[]string{"~/out"}, "not a regular file"},
		"empty":     {[]string{" "}, "empty file path"},
		"too many":  {make([]string, MaxMessageFiles+1), "too many files"},
	} {
		if _, err := readMessageFiles(home, tc.paths); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: want %q, got %v", name, tc.want, err)
		}
	}

	// Over the limit together, though each file alone is under it.
	half := make([]byte, MaxMessageFileBytes/2+1)
	write("out/x.bin", half)
	write("out/y.bin", half)
	if _, err := readMessageFiles(home, []string{"out/x.bin", "out/y.bin"}); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("the limit is for the files together: %v", err)
	}
}
