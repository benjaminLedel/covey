package target

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Checkouts pile up. Every checkout action creates a directory under
// <home>/repos, and nothing ever removed one again: the home is persistent
// (with warm_sandbox it survives every run), so an agent working on a dozen
// merge requests carries a dozen full working copies around — each of them
// including the dependency caches that preserveDirs deliberately keeps.
//
// That is not a theoretical concern. A QA agent wrote into its own wiki that
// its 40 GB sandbox overlay was running full "through old checkouts (~/repos)
// and scratch folders", and one of its runs ended with `claude exit: signal:
// killed`. Nobody saw it coming, because nothing measured it.
//
// Hence: after every checkout the oldest working copies fall away. The measure
// is the last USE (mtime of the directory), not the creation — a checkout being
// worked in stays.

// keptCheckouts is how many working copies survive per agent. Five is enough
// for a review loop across several merge requests and still leaves room on a
// modest sandbox. Overridable via COVEY_CHECKOUT_KEEP (the daemon's process
// env); 0 or less switches the cleanup off.
func keptCheckouts() int {
	if n, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COVEY_CHECKOUT_KEEP"))); err == nil {
		return n
	}
	return 5
}

// ReposDir is where checkouts live in an agent's home.
func ReposDir(workdir string) string { return filepath.Join(workdir, "repos") }

// PruneOldCheckouts removes the least recently used working copies under
// <workdir>/repos and keeps the newest keptCheckouts() ones. keep is the
// directory that must survive under all circumstances — the checkout just
// created, which at that moment is the newest anyway, but that should not be a
// matter of luck.
//
// Returns the names of the removed directories, for the log and the recording:
// an agent finding its checkout gone should be able to read WHY somewhere.
//
// Best effort — a checkout that cannot be removed is not worth failing the
// action that has already succeeded.
func PruneOldCheckouts(workdir, keep string) []string {
	limit := keptCheckouts()
	if limit <= 0 || workdir == "" {
		return nil
	}
	dir := ReposDir(workdir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	type cand struct {
		name string
		used time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !e.IsDir() || filepath.Join(dir, e.Name()) == filepath.Clean(keep) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		cands = append(cands, cand{e.Name(), info.ModTime()})
	}
	// The kept directory occupies one of the slots.
	free := limit - 1
	if len(cands) <= free {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return cands[i].used.After(cands[j].used) })

	var removed []string
	for _, c := range cands[free:] {
		if err := os.RemoveAll(filepath.Join(dir, c.name)); err == nil {
			removed = append(removed, c.name)
		}
	}
	return removed
}

// CheckoutPruneNote turns the removal into a sentence for the checkout result.
// The agent has to learn about it: it may hold a path from an earlier run that
// no longer exists, and "no such file or directory" is a poor way to find that
// out.
func CheckoutPruneNote(removed []string) string {
	if len(removed) == 0 {
		return ""
	}
	return fmt.Sprintf(" Space in the sandbox: the %d least recently used working copies were removed (%s) — "+
		"check them out again if you need them.", len(removed), strings.Join(removed, ", "))
}
