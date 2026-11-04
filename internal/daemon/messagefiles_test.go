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

// TestReadMessageFilesRefusesPseudoFilesystems (#678): the daemon reads the
// attachment, so a path into what describes the daemon itself is refused —
// directly, and through a symlink in the home.
func TestReadMessageFilesRefusesPseudoFilesystems(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{"/proc/self/status", "/dev/null", "/sys/kernel"} {
		if _, err := os.Lstat(p); err != nil {
			continue // not every test host has every one of them
		}
		if _, err := readMessageFiles(home, []string{p}); err == nil || !strings.Contains(err.Error(), "no files of your work") {
			t.Errorf("%s: want a refusal, got %v", p, err)
		}
		link := filepath.Join(home, "link-"+filepath.Base(p))
		if err := os.Symlink(p, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readMessageFiles(home, []string{"~/" + filepath.Base(link)}); err == nil || !strings.Contains(err.Error(), "no files of your work") {
			t.Errorf("%s through a symlink: want a refusal, got %v", p, err)
		}
	}

	// A symlink to a real file of the work still works, and the name shown is
	// the target's.
	if err := os.WriteFile(filepath.Join(home, "report.pdf"), []byte("%PDF"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(home, "report.pdf"), filepath.Join(home, "latest")); err != nil {
		t.Fatal(err)
	}
	files, err := readMessageFiles(home, []string{"latest"})
	if err != nil || len(files) != 1 || string(files[0].Data) != "%PDF" {
		t.Fatalf("a symlink inside the work: %+v %v", files, err)
	}
}

func TestPseudoRoot(t *testing.T) {
	for p, want := range map[string]string{
		"/proc": "/proc", "/proc/1/environ": "/proc", "/dev/null": "/dev", "/sys/x": "/sys",
		"/process/x": "", "/devices": "", "/home/agent/proc/x": "", "/tmp/a.png": "",
	} {
		if got := pseudoRoot(p); got != want {
			t.Errorf("%s: got %q, want %q", p, got, want)
		}
	}
}
