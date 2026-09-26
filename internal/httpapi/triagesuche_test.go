package httpapi

import (
	"strings"
	"testing"
)

// A name mistyped by a letter is still the colleague (#416): the search
// finds "Vorrauschauend" in the line of Volker Vorausschauend.
func TestOrgSearchToleratesATypo(t *testing.T) {
	org := `## Team (AI colleagues)

These AI agents belong to your organisation.

- Volker Vorausschauend — Delivery Lead — STOPPED (kill switch; takes no work)
- Quirina Prüfgeist — QA — YOUR TEAM (Engineering)`
	got := orgTreffer(org, suchWoerter("Volker Vorrauschauend?"))
	if len(got) != 1 || !strings.Contains(got[0], "Volker Vorausschauend") || !strings.Contains(got[0], "STOPPED") {
		t.Fatalf("hits = %v", got)
	}
	if got := orgTreffer(org, suchWoerter("Prüfgeist")); len(got) != 1 || !strings.Contains(got[0], "Quirina") {
		t.Fatalf("an exact surname: %v", got)
	}
	// Short words match only exactly: "QA" is below the three letters a
	// search word needs, "abc" finds nothing by accident.
	if got := orgTreffer(org, suchWoerter("abc")); len(got) != 0 {
		t.Fatalf("a short word matched by accident: %v", got)
	}
}

func TestAbstand(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"vorrauschauend", "vorausschauend", 2}, {"volker", "volker", 0}, {"volker", "walker", 2}, {"a", "abcdef", 3}} {
		if got := abstand(c.a, c.b); got != c.want {
			t.Errorf("abstand(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}
