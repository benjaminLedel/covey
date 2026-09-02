package marketplace

import (
	"strconv"
	"strings"
)

// The catalogue may say which covey a plugin needs, and until now that was a
// field nobody read.
//
// It matters most at exactly one moment, which is the one this repository is
// living through: a plugin that used to be compiled in becomes a catalogue
// entry. The entry appears before the release that drops the built-in — that
// order is deliberate, so nobody upgrades into a missing target system — and in
// the window between the two, an older instance can see an artefact it cannot
// run. Installing it there does not fail cleanly: the store lists compiled
// plugins and stored ones under the same name, so the built-in shadows the
// installed row and the operator is left with a plugin that is present, listed
// once, and not the thing they installed.
//
// A declared constraint that is never checked is worse than no constraint: the
// entry says 0.6.0, the operator reads it, and the button works anyway.

// MeetsMinVersion reports whether a covey of version have satisfies want.
//
// known is false when the running version is not a release — a dev build, a
// source checkout, a commit behind a tag. Then there is nothing to compare and
// the caller decides; the decision is to allow, because refusing would make it
// impossible to test a catalogue entry before the release it names exists.
func MeetsMinVersion(have, want string) (ok bool, known bool) {
	if strings.TrimSpace(want) == "" {
		return true, true
	}
	h, okH := parseVersion(have)
	w, okW := parseVersion(want)
	if !okH || !okW {
		return true, false
	}
	for i := range 3 {
		switch {
		case h[i] > w[i]:
			return true, true
		case h[i] < w[i]:
			return false, true
		}
	}
	return true, true
}

// parseVersion reads major.minor.patch out of a version string, tolerating a
// leading v and anything git appends behind it. "v0.6.0-12-gabc1234" is a build
// past 0.6.0 and counts as 0.6.0 — it HAS whatever 0.6.0 has, which is the only
// question being asked. A tree marked dirty is treated the same: whoever builds
// from a modified checkout is not protected by a version number anyway.
func parseVersion(s string) ([3]int, bool) {
	var out [3]int
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	if s == "" {
		return out, false
	}
	// Cut anything git appended: -12-gabc1234, -dirty, -rc1.
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) == 0 || len(parts) > 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}
