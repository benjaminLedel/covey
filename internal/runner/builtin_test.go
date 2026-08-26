package runner

import "testing"

// What the mode still decides — and what it no longer does. The existence of a
// registered runner used to be half of this answer: "off" meant "off once the
// organisation has one", and even the default let the built-in runner stand
// down by itself. Both are gone. A rule that infers an intention from a fact
// was wrong often enough to be expensive, and pausing the runner says the same
// thing where a person can see it.
func TestBuiltinAllowed(t *testing.T) {
	cases := []struct {
		name string
		mode string
		want bool
	}{
		{"the normal case: the control plane may carry sandboxes", "auto", true},
		{"off means off, whether or not another host exists", "off", false},
		{"an unset variable is auto", "", true},
		{"whitespace and case are not a second mode", " OFF ", false},
		{"anything unrecognised stays permissive", "vielleicht", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuiltinAllowed(c.mode); got != c.want {
				t.Errorf("BuiltinAllowed(%q) = %v, expected %v", c.mode, got, c.want)
			}
		})
	}
}
