package installer

import "strings"

import "testing"

// The heart of the matter: an instance may only prescribe a fixed version to
// the script when a release for it can exist. A development build is called
// "dev" or carries a describe suffix — neither ever exists on GitHub, and the
// script would run into a 404 instead of simply taking the latest release.
func TestVersionFuerRelease(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"v0.1.0", "v0.1.0"},
		{"v1.2.3", "v1.2.3"},
		{"v2.0.0-rc1", "v2.0.0-rc1"}, // pre-releases do get published
		{"dev", ""},
		{"", ""},
		{"v0.1.0-3-gabc1234", ""},       // git describe after the tag
		{"v0.1.0-dirty", ""},            // working tree with changes
		{"v0.1.0-3-gabc1234-dirty", ""}, // both
		{"0.1.0", ""},                   // without a v it is no tag of this project
	}
	for _, tc := range cases {
		if got := VersionFuerRelease(tc.in); got != tc.want {
			t.Errorf("VersionFuerRelease(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestRenderSetztVorspann(t *testing.T) {
	out := Render("v0.1.0", "runner")
	for _, want := range []string{
		"COVEY_INSTALL_VERSION=\"v0.1.0\"",
		"COVEY_INSTALL_DEFAULT=\"runner\"",
		"export COVEY_INSTALL_VERSION",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("preamble does not contain %q", want)
		}
	}
	// The body has to be in there unchanged — otherwise the instance serves
	// something other than what the repository holds.
	if !strings.Contains(out, Script) {
		t.Error("the embedded script is missing from the result")
	}
	// Exactly one shebang, and in the first line: the preamble brings one, the
	// body does too. Were the second one in the middle it would only be a
	// comment — but the first line has to be right.
	if !strings.HasPrefix(out, "#!/bin/sh\n") {
		t.Error("the result does not start with a shebang")
	}
}

// Without a version no VERSION preamble may be produced: the script should then
// determine the latest release itself.
func TestRenderOhneVersion(t *testing.T) {
	out := Render("", "")
	if strings.Contains(out, "COVEY_INSTALL_VERSION=") {
		t.Error("without a version none may be prescribed")
	}
	if !strings.Contains(out, "COVEY_INSTALL_DEFAULT=\"server\"") {
		t.Error("without an argument the default must be \"server\"")
	}
}
