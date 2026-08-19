package marketplace

import "testing"

func TestMeetsMinVersion(t *testing.T) {
	cases := []struct {
		have, want string
		ok, known  bool
	}{
		// The case this exists for: the entry names the release that drops the
		// built-in, and the instance has not had it yet.
		{"v0.5.0", "0.6.0", false, true},
		{"v0.6.0", "0.6.0", true, true},
		{"v0.7.2", "0.6.0", true, true},
		{"v0.6.1", "0.6.0", true, true},
		{"v1.0.0", "0.9.9", true, true},
		{"v0.9.9", "1.0.0", false, true},
		// Patch and minor are compared, not string-sorted: "0.10.0" is newer
		// than "0.9.0" and a lexical comparison would say the opposite.
		{"v0.10.0", "0.9.0", true, true},
		{"v0.9.0", "0.10.0", false, true},
		// A build past the tag HAS what the tag has.
		{"v0.6.0-12-gabc1234", "0.6.0", true, true},
		{"v0.5.0-99-gdeadbee", "0.6.0", false, true},
		{"v0.6.0-dirty", "0.6.0", true, true},
		// No constraint is not a failed constraint.
		{"v0.1.0", "", true, true},
		// Nothing to compare: allowed, and the caller is told it is a guess.
		{"dev", "0.6.0", true, false},
		{"", "0.6.0", true, false},
		{"v0.6.0", "not-a-version", true, false},
	}
	for _, c := range cases {
		ok, known := MeetsMinVersion(c.have, c.want)
		if ok != c.ok || known != c.known {
			t.Errorf("MeetsMinVersion(%q, %q) = %v,%v — want %v,%v",
				c.have, c.want, ok, known, c.ok, c.known)
		}
	}
}
