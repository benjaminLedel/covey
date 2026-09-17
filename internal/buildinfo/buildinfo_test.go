package buildinfo

import "testing"

// The source as a target-system address: what stands as the link in the
// footer is the same detail that covey Doctor reads as its default (spec/21).
func TestSourceRepo(t *testing.T) {
	system, project := SourceRepo()
	if system != "github" || project != "benjaminLedel/covey" {
		t.Fatalf("SourceRepo() = %q,%q — SourceURL ist %q", system, project, SourceURL)
	}
}

// The anchor of a report. `git describe` returns something like
// "v0.4.0-56-gea0485c" at every state behind the tag — a name the repository
// does not have and that would run empty as a ref. Then the commit counts.
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
			// Get is a OnceValue over the running binary; the test sets it for
			// its run and restores it afterwards.
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

// String is what the log line, the CLI and the footer show. Its job is to stay
// readable when parts are missing — a container build without .git has no
// commit, and a bare `go build` has no date.
func TestInfoString(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   Info
		want string
	}{
		{"complete", Info{Version: "v0.1.0", Commit: "abc1234", BuiltAt: "2026-07-30T12:00:00Z", Go: "go1.26"},
			"v0.1.0 (abc1234, 2026-07-30T12:00:00Z, go1.26)"},
		{"dirty tree", Info{Version: "v0.1.0", Commit: "abc1234", Dirty: true, Go: "go1.26"},
			"v0.1.0 (abc1234-dirty, go1.26)"},
		{"no provenance at all", Info{Version: "dev"}, "dev"},
		{"only the toolchain", Info{Version: "dev", Go: "go1.26"}, "dev (go1.26)"},
		// Dirty without a commit has nothing to attach itself to and must not
		// produce a stray "-dirty".
		{"dirty without a commit", Info{Version: "dev", Dirty: true, Go: "go1.26"}, "dev (go1.26)"},
	} {
		if got := tc.in.String(); got != tc.want {
			t.Errorf("%s: String() = %q, expected %q", tc.name, got, tc.want)
		}
	}
}

// The package-level String is the running binary's — whatever this test binary
// was built from, it has to say something rather than nothing.
func TestPackageStringAnswers(t *testing.T) {
	if String() == "" {
		t.Error("String() is empty — the log line would then say nothing at all")
	}
	if got := Get().Go; got == "" {
		t.Error("the toolchain version is missing")
	}
	if got := Get().Source; got != SourceURL {
		t.Errorf("Source = %q, expected %q — a fork shows its own address", got, SourceURL)
	}
	if got := Get().Version; got == "" {
		t.Error(`Version is empty — "dev" is the answer for an unknown one`)
	}
}
