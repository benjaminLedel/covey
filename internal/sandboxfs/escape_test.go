package sandboxfs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The home is a host directory mounted into the sandbox as writable
// (orchestrator/sandbox_docker.go). The agent has a shell in it via `dev exec`
// — so it can create symlinks that point out of the home. These tests pin down
// that no operation follows them: otherwise the file browser of an admin reads
// or writes on the HOST outside the home.
//
// They check behaviour, not implementation — which is why they hold unchanged
// for the old check (resolve/ensureInside) as for os.Root.

// aufbau creates a home with a secret next to it and returns both.
func aufbau(t *testing.T) (fs *FS, home, geheimnis string) {
	t.Helper()
	basis := t.TempDir()
	home = filepath.Join(basis, "home")
	if err := os.MkdirAll(filepath.Join(home, "unterordner"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Sits NEXT TO the home, not inside it — exactly what must never be reachable.
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

// A symlink to a file outside must not be readable.
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

// A symlink to a directory outside must not be listable — and above all one
// must not reach deeper THROUGH it.
func TestKeinDurchgriffDurchVerzeichnis(t *testing.T) {
	fs, home, geheimnis := aufbau(t)
	link(t, filepath.Dir(geheimnis), filepath.Join(home, "aussen"))

	if _, err := fs.List("aussen"); err == nil {
		t.Error("List folgte dem Verzeichnis-Link")
	}
	// The actual attack: the link is only the bridge, the target lies
	// behind it.
	if datei, err := fs.Read("aussen/geheim.txt"); err == nil {
		t.Fatalf("Read griff durch den Link hindurch: %q", datei.Content)
	}
}

// Writing out through a link would be the worse case: the agent would get the
// admin to overwrite a host file.
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

// Deleting and moving must not use the link as a way out either.
// The link ITSELF may go — that is an entry inside the home.
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

// An archive must not pack files from outside.
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

// Textual traversal attempts fall away already at normalisation.
func TestTextuelleTraversalWirdNormalisiert(t *testing.T) {
	fs, _, _ := aufbau(t)
	for _, p := range []string{"../geheim.txt", "unterordner/../../geheim.txt", "/../geheim.txt"} {
		if datei, err := fs.Read(p); err == nil {
			t.Errorf("%q wurde gelesen: %q", p, datei.Content)
		}
	}
}

// A RELATIVE link inside the home must stay usable — that is the form
// toolchains actually create (`.claude/skills -> ../.agents/skills`,
// node_modules/.bin). A fix that sweeps those away too makes the file browser
// useless.
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
	// Also across several levels with ..
	if err := os.MkdirAll(filepath.Join(home, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	link(t, "../../unterordner/echt.txt", filepath.Join(home, "a", "b", "hoch.txt"))
	if datei, err := fs.Read("a/b/hoch.txt"); err != nil || datei.Content != "hallo" {
		t.Errorf("relativer Link über .. : %v / %q", err, datei.Content)
	}
}

// ABSOLUTE links are not followed, even when their target would lie in the
// home. That is a property of os.Root and has no consequence in this
// environment: whatever is linked absolutely in the sandbox points at
// /home/agent/... — a path the host does not have at all. Such links were dead
// before, the entry stays visible and is marked as outside.
func TestAbsoluterLinkWirdNichtVerfolgt(t *testing.T) {
	fs, home, _ := aufbau(t)
	if err := os.WriteFile(filepath.Join(home, "unterordner", "echt.txt"), []byte("hallo"), 0o644); err != nil {
		t.Fatal(err)
	}
	link(t, filepath.Join(home, "unterordner", "echt.txt"), filepath.Join(home, "absolut.txt"))

	if _, err := fs.Read("absolut.txt"); err == nil {
		t.Error("einem absoluten Link wurde gefolgt")
	}
	// It still has to stay visible — otherwise an admin looks for a file
	// that lies in the directory and is missing from the list.
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
