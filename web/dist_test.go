package web

import (
	"io/fs"
	"regexp"
	"testing"
)

// The website handler hands out the files under assets/ with a cache lifetime
// of a year and immutable (internal/httpapi/spa.go). That is only allowed
// because Vite writes the content hash into the name: a changed file gets a new
// address, so the old one can never be wrong.
//
// This test guards that assumption where it is made — in the build output. A
// file that lands there without a hash would not be broken by the check in the
// handler (it falls back to a day), but it would be a sign that the build
// changed and the assumption needs re-reading.
var gehasht = regexp.MustCompile(`-[A-Za-z0-9_-]{8}\.[A-Za-z0-9]+$`)

func TestAssetsTragenEinenHash(t *testing.T) {
	dist, err := Dist()
	if err != nil {
		t.Fatalf("Dist(): %v", err)
	}
	eintraege, err := fs.ReadDir(dist, "assets")
	if err != nil {
		// A checkout without a frontend build — nothing to say here.
		t.Skipf("no dist/assets: %v", err)
	}
	if len(eintraege) == 0 {
		t.Skip("dist/assets is empty")
	}
	for _, e := range eintraege {
		if e.IsDir() || gehasht.MatchString(e.Name()) {
			continue
		}
		t.Errorf("assets/%s carries no hash in its name — the handler would hand it "+
			"out as immutable for a year", e.Name())
	}
}
