package engines

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The version npm actually put down is read back rather than believed: a
// registry that answers a pinned request with something else has to be caught
// at install time and not six months later in a run that behaved oddly. What
// that reading does when it cannot read is therefore part of the check.
func TestPackageVersionRefusesWhatItCannotRead(t *testing.T) {
	dir := t.TempDir()

	// Installed where npm puts it, and readable.
	good := filepath.Join(dir, "package.json")
	if err := os.WriteFile(good, []byte(`{"name":"x","version":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := packageVersion(good)
	if err != nil || got != "1.2.3" {
		t.Fatalf("packageVersion = %q, %v", got, err)
	}

	// Not there at all: the message says WHERE it looked, because the usual
	// cause is npm having put it somewhere else.
	_, err = packageVersion(filepath.Join(dir, "gibtesnicht.json"))
	if err == nil {
		t.Fatal("a missing package.json was accepted")
	}
	if !strings.Contains(err.Error(), "gibtesnicht.json") {
		t.Errorf("the error does not name the path: %v", err)
	}

	// There but unreadable, and there but silent — both are refusals, because
	// an empty version would compare equal to nothing and pass the pin check
	// by accident.
	broken := filepath.Join(dir, "kaputt.json")
	os.WriteFile(broken, []byte("kein json"), 0o600)
	if _, err := packageVersion(broken); err == nil {
		t.Error("an unreadable package.json was accepted")
	}
	empty := filepath.Join(dir, "leer.json")
	os.WriteFile(empty, []byte(`{"name":"x"}`), 0o600)
	if _, err := packageVersion(empty); err == nil {
		t.Error("a package.json without a version was accepted")
	}
}

// The marker stores the executable's path RELATIVE to the layer, so a store
// that moves between machines still says where its own binary is.
func TestInnerExecutableIsRelative(t *testing.T) {
	for _, r := range []Release{
		{Package: "x", Version: "1.0.0"},
	} {
		got := r.innerExecutable()
		if strings.HasPrefix(got, "/") {
			t.Errorf("innerExecutable is absolute: %q", got)
		}
	}
}
