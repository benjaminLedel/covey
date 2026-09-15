package orchestrator

import (
	"fmt"
	"strings"
	"testing"

	"covey/internal/homestore"
)

func TestRootEntriesCountsOnlyTheTopLevel(t *testing.T) {
	m := homestore.Manifest{Entries: []homestore.Entry{
		{Path: "repos", Dir: true},
		{Path: "repos/p40-kasse", Dir: true},
		{Path: "repos/p40-kasse/README.md", Size: 10},
		{Path: ".cache", Dir: true},
		{Path: "shot-3.png", Size: 1},
		{Path: "node_modules", Link: "/opt/x"},
	}}
	got := rootEntries(m)
	if strings.Join(got, ",") != "repos,.cache,shot-3.png,node_modules" {
		t.Fatalf("root entries %v", got)
	}
}

// The names from the home that led to #272, in their proportions.
func TestNamePatternsGroupTheLeftoversOfTickets(t *testing.T) {
	var names []string
	add := func(format string, n int) {
		for i := 0; i < n; i++ {
			names = append(names, fmt.Sprintf(format, 850+i))
		}
	}
	add("gegenprobe-%d", 69)
	add("suite-%d.log", 59)
	add("shot-%d.png", 42)
	add("rsync%d.log", 8)
	add("mr-%d", 2) // two are not a pattern
	names = append(names, ".cache", ".npm", ".claude", "repos", "wiki", "896save")

	got := namePatterns(names, 3)
	if len(got) != 3 {
		t.Fatalf("want the three largest groups, got %+v", got)
	}
	if got[0].Prefix != "gegenprobe" || got[0].Count != 69 || got[0].Example != "gegenprobe-850" {
		t.Fatalf("largest group %+v", got[0])
	}
	if got[1].Prefix != "suite" || got[2].Prefix != "shot" {
		t.Fatalf("order %+v", got)
	}

	all := namePatterns(names, 0)
	for _, p := range all {
		if p.Prefix == "mr" || strings.HasPrefix(p.Prefix, ".") || p.Prefix == "repos" {
			t.Fatalf("%q is not a pattern: %+v", p.Prefix, all)
		}
	}
	if all[len(all)-1].Prefix != "rsync" {
		t.Fatalf("a number glued to the name still groups: %+v", all)
	}
}

// A home that is only scattered is not sent to `du`, and a home that is only
// large is not told about names. The limits stand in both.
func TestTidyBodyFollowsWhatWasMeasured(t *testing.T) {
	var root []string
	for i := 0; i < 250; i++ {
		root = append(root, fmt.Sprintf("shot-%d.png", i))
	}

	scattered := tidyBody(tidyMeasure{total: 800 << 20, root: root, scattered: true})
	for _, want := range []string{"Einträge direkt in ~: 250", "`shot*`: 250", "~/scratch/", "Gedächtnis"} {
		if !strings.Contains(scattered, want) {
			t.Fatalf("scattered home: %q missing:\n%s", want, scattered)
		}
	}
	if strings.Contains(scattered, "du -sh") {
		t.Fatalf("a 800 MB home must not be sent after what is large:\n%s", scattered)
	}

	large := tidyBody(tidyMeasure{total: 19 << 30, bytesUp: 426 << 20, durationMS: 139964, large: true})
	for _, want := range []string{"19.0 GB", "du -sh", "Gedächtnis"} {
		if !strings.Contains(large, want) {
			t.Fatalf("large home: %q missing:\n%s", want, large)
		}
	}
	if strings.Contains(large, "Einträge direkt") || strings.Contains(large, "~/scratch/") {
		t.Fatalf("a home that was not counted must not get a count:\n%s", large)
	}

	both := tidyBody(tidyMeasure{total: 64 << 30, root: root, large: true, scattered: true})
	if !strings.Contains(both, "du -sh") || !strings.Contains(both, "`shot*`") {
		t.Fatalf("a home that is both gets both:\n%s", both)
	}
}
