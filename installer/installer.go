// Package installer carries the installation script and serves it the way the
// running instance needs it.
//
// Why an instance serves its own installation script: it knows its version. A
// runner installed through it therefore matches the server it registers with —
// the protocol drift from spec/16 cannot arise in the first place. The same
// holds for "one more node like this one".
//
// The binaries still come from the GitHub releases, not from the instance.
// That is deliberate: the trust anchor for executable code stays the project
// source, even when the script comes from elsewhere. The instance decides the
// version, not the content.
package installer

import (
	_ "embed"
	"fmt"
	"strings"
)

// Script is the script exactly as it also sits on GitHub
// (installer/install.sh). A second copy in the repo would not exist without
// drift — hence this very file, embedded.
//
//go:embed install.sh
var Script string

// Render prepends a preamble to the embedded version that carries the
// instance's defaults. The script reads them through `:=` defaults, so it stays
// runnable unchanged when it comes straight from GitHub.
//
// An empty version = no preamble for the version: the script then determines
// the latest release itself. That is the right fallback for a development
// build, whose "dev" will never exist on GitHub.
func Render(version, standard string) string {
	if standard == "" {
		standard = "server"
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString("# Preamble of this covey instance — the body is, unchanged, the script\n")
	b.WriteString("# from the repository (installer/install.sh).\n")
	if version != "" {
		fmt.Fprintf(&b, "COVEY_INSTALL_VERSION=%q\n", version)
		b.WriteString("export COVEY_INSTALL_VERSION\n")
	}
	fmt.Fprintf(&b, "COVEY_INSTALL_DEFAULT=%q\n", standard)
	b.WriteString("export COVEY_INSTALL_DEFAULT\n\n")
	b.WriteString(Script)
	return b.String()
}

// VersionFuerRelease filters out versions for which no release can exist. A
// binary from `make build` carries "dev" or a `git describe` form with a commit
// suffix; if the instance prescribed that as a fixed version, the script would
// run into a 404 instead of into the latest release.
func VersionFuerRelease(v string) string {
	if !strings.HasPrefix(v, "v") || strings.Contains(v, "dirty") {
		return ""
	}
	// For commits after the tag `git describe` appends "-<n>-g<hash>". That is
	// not a release tag either.
	if strings.Count(v, "-") > 0 && !strings.Contains(v, "-rc") {
		return ""
	}
	return v
}
