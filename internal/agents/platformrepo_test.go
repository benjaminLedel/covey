package agents

import (
	"strings"
	"testing"

	"covey/internal/buildinfo"
)

// Wohin covey Doctor meldet, wenn niemand es ihm gesagt hat: in das Projekt,
// aus dem dieses Programm stammt. Die Adresse stand vorher als Frage in den
// Stammdaten — dabei weiss die Plattform sie ueber sich selbst (spec/21).
func TestPlatformRepoVoreinstellung(t *testing.T) {
	standardSystem, standardProjekt := buildinfo.SourceRepo()
	if standardSystem == "" || standardProjekt == "" {
		t.Fatal("SourceURL laesst sich nicht als Zielsystem-Adresse lesen — dann gibt es keine Voreinstellung")
	}

	faelle := []struct {
		name            string
		system, projekt string
		wantS, wantP    string
	}{
		{"nichts gespeichert → das eigene Projekt", "", "", standardSystem, standardProjekt},
		{"halbe Adresse zaehlt nicht", "gitlab", "", standardSystem, standardProjekt},
		{"eigenes Repository gewinnt", "gitlab", "gruppe/covey", "gitlab", "gruppe/covey"},
		{"abgeschaltet heisst gar nicht", RepoOff, "", "", ""},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			s, p := PlatformRepo(f.system, f.projekt)
			if s != f.wantS || p != f.wantP {
				t.Fatalf("PlatformRepo(%q,%q) = %q,%q — erwartet %q,%q", f.system, f.projekt, s, p, f.wantS, f.wantP)
			}
		})
	}
}

// The anchor carries the section: an agent reading the default branch reports
// against code this instance does not execute. The anchor only matters where
// the agent may read the source at all — hence mayRead=true here.
func TestPlatformRepoDocAnchor(t *testing.T) {
	withTag := PlatformRepoDoc("github", "benjaminLedel/covey", "v0.4.0", true, true, true)
	if !strings.Contains(withTag, "`ref: v0.4.0`") || !strings.Contains(withTag, "release") {
		t.Fatalf("the tag should be named as the shipped version:\n%s", withTag)
	}

	withCommit := PlatformRepoDoc("github", "benjaminLedel/covey", "abc1234", false, true, true)
	if !strings.Contains(withCommit, "`ref: abc1234`") || !strings.Contains(withCommit, "commit") {
		t.Fatalf("without a tag the commit stays the anchor:\n%s", withCommit)
	}

	// Without provenance no claim: the prompt then says the attribution is
	// missing instead of passing the default branch off as "the running state".
	without := PlatformRepoDoc("github", "benjaminLedel/covey", "", false, true, true)
	if !strings.Contains(without, "default branch") || !strings.Contains(without, "no version information") {
		t.Fatalf("without an anchor the honest answer is missing:\n%s", without)
	}

	// No address, no section — otherwise the prompt would carry a capability
	// the broker refuses right afterwards.
	if PlatformRepoDoc("", "", "v0.4.0", true, true, true) != "" {
		t.Fatal("without an address no section may be produced")
	}
}

// Filing and reading hang on different conditions since covey#200: whoever may
// review may file — the control plane writes the issue with the organisation's
// own account — while reading the source still needs the target system in
// ACCESS.md. An agent without that access must not read "check it out" in its
// prompt, and must still be told how to file.
func TestPlatformRepoDocFilesWithoutASeat(t *testing.T) {
	without := PlatformRepoDoc("github", "benjaminLedel/covey", "v0.4.0", true, true, false)
	if !strings.Contains(without, "covey/create_issue") {
		t.Fatalf("filing works without a seat and has to stand there:\n%s", without)
	}
	if strings.Contains(without, "check it out") {
		t.Fatalf("without access to the system nothing may promise a checkout:\n%s", without)
	}
	if !strings.Contains(without, "could not read the code") {
		t.Fatalf("the report has to say what it could not see:\n%s", without)
	}

	with := PlatformRepoDoc("github", "benjaminLedel/covey", "v0.4.0", true, true, true)
	if !strings.Contains(with, "covey/create_issue") || !strings.Contains(with, "check it out") {
		t.Fatalf("with access both halves stand there:\n%s", with)
	}

	// Neither half: nothing at all. Without an account stored for the system
	// the platform cannot file, and without the access line the agent cannot
	// read — a section about a repository nobody can reach helps no one.
	if PlatformRepoDoc("github", "benjaminLedel/covey", "v0.4.0", true, false, false) != "" {
		t.Fatal("without filing and without reading no section may be produced")
	}

	// Filing off, reading on: the section stands, and it says plainly that
	// nothing here files.
	readOnly := PlatformRepoDoc("github", "benjaminLedel/covey", "v0.4.0", true, false, true)
	if strings.Contains(readOnly, "covey/create_issue") || !strings.Contains(readOnly, "Nothing here files issues") {
		t.Fatalf("without a stored account nothing may promise filing:\n%s", readOnly)
	}
}
