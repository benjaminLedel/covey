// Package buildinfo carries the provenance of the running binary: version,
// commit hash and build time. Both binaries (covey, coveyd) use the same
// package so that "which build is running here?" has the same answer
// everywhere — in the log, on the CLI, in the API and in the UI footer.
//
// The build sets the values via -ldflags (see Makefile/Dockerfile/CI):
//
//	go build -ldflags "-X covey/internal/buildinfo.version=v0.1.0 \
//	                   -X covey/internal/buildinfo.commit=abc1234 \
//	                   -X covey/internal/buildinfo.date=2026-07-30T12:00:00Z"
//
// If they are missing — for instance in a bare `go build ./...` — the package
// falls back to the VCS data Go embeds itself. In the container build there is
// none (`.git` is in .dockerignore), which is why the Makefile/CI passes the
// values through there.
package buildinfo

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
)

// SourceURL is the public source of this program. Covey is licensed under the
// AGPL-3.0 and runs as a network service: whoever offers a modified version to
// others owes them the source. The address therefore belongs where it can be
// found without searching — the CLI and the UI footer. Whoever forks and
// operates it enters their own address here.
const SourceURL = "https://github.com/benjaminLedel/covey"

// Set via -ldflags. Lowercase: access goes through Get().
var (
	version string
	commit  string
	date    string
)

// Info is the binary's provenance, the way the API and the CLI show it.
type Info struct {
	// Version is the git tag resp. `git describe`; "dev" when unknown.
	Version string `json:"version"`
	// Commit is the short commit hash; empty when unknown.
	Commit string `json:"commit"`
	// BuiltAt is the build time in RFC3339 (UTC); empty when unknown.
	BuiltAt string `json:"built_at"`
	// Dirty marks a build from a working tree with uncommitted changes (only
	// detectable when Go has embedded the VCS info).
	Dirty bool `json:"dirty"`
	// Go is the toolchain version the binary was built with.
	Go string `json:"go"`
	// Source is the public source (SourceURL). It travels along over the API
	// so that the UI does not hardcode it — a fork thus shows its own address,
	// not the origin's.
	Source string `json:"source"`
	// SourceSystem/SourceProject are the same address as a target-system
	// address (SourceRepo): the default Covey Doctor reports to as long as the
	// organisation has not named a repository of its own. The interface shows
	// it where that setting sits, instead of leaving "not set up" standing over
	// something that is set up.
	SourceSystem  string `json:"source_system,omitempty"`
	SourceProject string `json:"source_project,omitempty"`
}

// Get returns the binary's provenance (determined once, then cached).
var Get = sync.OnceValue(func() Info {
	i := Info{Version: version, Commit: commit, BuiltAt: date, Go: runtime.Version(), Source: SourceURL}

	// Fill gaps from the embedded VCS data (local build in a git working
	// tree). Values set via -ldflags always win.
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			switch s.Key {
			case "vcs.revision":
				if i.Commit == "" {
					i.Commit = s.Value
				}
			case "vcs.time":
				if i.BuiltAt == "" {
					i.BuiltAt = s.Value
				}
			case "vcs.modified":
				i.Dirty = s.Value == "true"
			}
		}
	}

	if len(i.Commit) > 12 {
		i.Commit = i.Commit[:12]
	}
	if i.Version == "" {
		i.Version = "dev"
	}
	i.SourceSystem, i.SourceProject = SourceRepo()
	return i
})

// String is the one-line rendering for log and CLI:
// "v0.1.0 (abc1234, 2026-07-30T12:00:00Z, go1.26)".
func (i Info) String() string {
	var parts []string
	if i.Commit != "" {
		c := i.Commit
		if i.Dirty {
			c += "-dirty"
		}
		parts = append(parts, c)
	}
	if i.BuiltAt != "" {
		parts = append(parts, i.BuiltAt)
	}
	if i.Go != "" {
		parts = append(parts, i.Go)
	}
	if len(parts) == 0 {
		return i.Version
	}
	return fmt.Sprintf("%s (%s)", i.Version, strings.Join(parts, ", "))
}

// String is the short rendering of the running binary — for log lines.
func String() string { return Get().String() }

// SourceRepo derives from SourceURL which target-system plugin the source lives
// on and under which project — the address Covey Doctor reads and files its
// issues at when an organisation has not named one of its own (spec/21).
//
// Derived, not configured a second time: the program knows where it comes from,
// and a fork that changes SourceURL takes its own tracker along instead of the
// origin's. Empty when the address is not one of the plugins that can check out
// a repository — then there is no default, and the organisation names one or
// the layer does not exist.
func SourceRepo() (system, project string) {
	url := strings.TrimSuffix(strings.TrimSpace(SourceURL), ".git")
	for host, plugin := range map[string]string{"github.com": "github", "gitlab.com": "gitlab"} {
		for _, prefix := range []string{"https://" + host + "/", "http://" + host + "/", host + "/"} {
			if rest, ok := strings.CutPrefix(url, prefix); ok {
				rest = strings.Trim(rest, "/")
				// Exactly group/project — a deeper path is a page inside the
				// repository, not the address of one.
				if rest != "" && strings.Count(rest, "/") >= 1 {
					return plugin, rest
				}
			}
		}
	}
	return "", ""
}

// Ref is the anchor of a report: what tells the reader which state of this
// instance they are looking at.
//
// The tag when this build sits exactly on one — that is the statement a
// maintainer can act on ("v0.4.0 is affected"). Otherwise `git describe` yields
// something like "v0.4.0-56-gea0485c": a name the repository does not know and
// that would run into nothing as a ref. Then the commit, which it does know.
//
// isTag tells the caller which of the two they are holding, so the prompt can
// call it by its name instead of saying "ref" and leaving the rest open.
func Ref() (ref string, isTag bool) {
	i := Get()
	// A clean tag: no commits behind it (describe's -N-g<hash>), no dirty tree,
	// and not "dev" for want of provenance.
	if v := i.Version; v != "" && v != "dev" && !i.Dirty && !describeSuffix.MatchString(v) {
		return v, true
	}
	return i.Commit, false
}

// -<commits>-g<hash> at the end, `git describe`'s mark for "behind the tag" —
// with the optional -dirty the Makefile appends.
var describeSuffix = regexp.MustCompile(`-\d+-g[0-9a-f]{7,}(-dirty)?$`)
