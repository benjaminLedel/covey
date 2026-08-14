package buildinfo

import "testing"

// Die Quelle als Zielsystem-Adresse: Was in der Fusszeile als Link steht, ist
// dieselbe Angabe, die Covey Doctor als Voreinstellung ausliest (spec/21).
func TestSourceRepo(t *testing.T) {
	system, project := SourceRepo()
	if system != "github" || project != "benjaminLedel/covey" {
		t.Fatalf("SourceRepo() = %q,%q — SourceURL ist %q", system, project, SourceURL)
	}
}

// Der Anker eines Berichts. `git describe` liefert bei jedem Stand hinter dem
// Tag etwas wie „v0.4.0-56-gea0485c" — ein Name, den es im Repository nicht
// gibt und der als ref ins Leere liefe. Dann zaehlt der Commit.
func TestRefTagOderCommit(t *testing.T) {
	faelle := []struct {
		name      string
		info      Info
		wantRef   string
		wantIsTag bool
	}{
		{"sauberer Tag", Info{Version: "v0.4.0", Commit: "abc1234"}, "v0.4.0", true},
		{"hinter dem Tag", Info{Version: "v0.4.0-56-gea0485c", Commit: "abc1234"}, "abc1234", false},
		{"hinter dem Tag, schmutzig", Info{Version: "v0.4.0-56-gea0485c-dirty", Commit: "abc1234"}, "abc1234", false},
		{"Tag, aber schmutziger Baum", Info{Version: "v0.4.0", Commit: "abc1234", Dirty: true}, "abc1234", false},
		{"ohne Provenance", Info{Version: "dev"}, "", false},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			// Get ist eine OnceValue über das laufende Binary; der Test setzt
			// sie für seinen Lauf und stellt sie danach zurück.
			alt := Get
			Get = func() Info { return f.info }
			defer func() { Get = alt }()

			ref, isTag := Ref()
			if ref != f.wantRef || isTag != f.wantIsTag {
				t.Fatalf("Ref() = %q,%v — erwartet %q,%v", ref, isTag, f.wantRef, f.wantIsTag)
			}
		})
	}
}
