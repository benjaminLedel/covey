package runner

import "testing"

// The rule that a registered runner switches the built-in one off was right
// about the intent and wrong about the consequence: a host that does not hold
// the workplace image left the organisation with a runner and without a data
// plane. The table is short, and each row is a state that has actually
// occurred on a running instance.
func TestBuiltinAllowed(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		hasRemote bool
		want      bool
	}{
		{"no runner at all — the normal single-machine case", "auto", false, true},
		{"a registered runner that does not fit — the outage", "auto", true, true},
		{"off, and something else can carry it", "off", true, false},
		{"off, but nothing else exists — an organisation that could never work", "off", false, true},
		{"an unset variable is auto", "", true, true},
		{"whitespace and case are not a second mode", " OFF ", true, false},
		{"anything unrecognised stays permissive", "vielleicht", true, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := BuiltinAllowed(c.mode, c.hasRemote); got != c.want {
				t.Errorf("BuiltinAllowed(%q, %v) = %v, expected %v", c.mode, c.hasRemote, got, c.want)
			}
		})
	}
}
