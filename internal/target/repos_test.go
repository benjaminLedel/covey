package target

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Checkouts used to pile up without bound in the persistent home — a QA agent
// ran its 40 GB overlay full that way and one of its runs was killed. The
// newest working copies survive, the rest fall away, and the one just created
// survives under all circumstances.
func TestPruneOldCheckoutsKeepsTheNewestAndTheCurrent(t *testing.T) {
	t.Setenv("COVEY_CHECKOUT_KEEP", "3")
	home := t.TempDir()
	repos := ReposDir(home)
	now := time.Now()
	// Six working copies, p0 the oldest … p5 the newest.
	for i := 0; i < 6; i++ {
		d := filepath.Join(repos, "p"+string(rune('0'+i)))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(d, now, now.Add(-time.Duration(6-i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	// The current checkout is the OLDEST by mtime — it must survive anyway.
	current := filepath.Join(repos, "p0")
	removed := PruneOldCheckouts(home, current)
	if len(removed) != 3 {
		t.Fatalf("expected 3 removals, got %v", removed)
	}
	for _, name := range []string{"p0", "p5", "p4"} {
		if _, err := os.Stat(filepath.Join(repos, name)); err != nil {
			t.Fatalf("%s must survive: %v", name, err)
		}
	}
	for _, name := range []string{"p1", "p2", "p3"} {
		if _, err := os.Stat(filepath.Join(repos, name)); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone, err=%v", name, err)
		}
	}
	if note := CheckoutPruneNote(removed); note == "" {
		t.Fatal("the agent has to learn that its working copy is gone")
	}
}

// Switched off (0) and below the limit nothing is touched.
func TestPruneOldCheckoutsRestraint(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ReposDir(home), "p1"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := PruneOldCheckouts(home, ""); got != nil {
		t.Fatalf("below the limit nothing is removed: %v", got)
	}
	t.Setenv("COVEY_CHECKOUT_KEEP", "0")
	if got := PruneOldCheckouts(home, ""); got != nil {
		t.Fatalf("switched off nothing is removed: %v", got)
	}
	if CheckoutPruneNote(nil) != "" {
		t.Fatal("no removal, no sentence")
	}
}
