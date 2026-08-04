package secrets

import "testing"

func TestPreview(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"empty", "", ""},
		{"short stays masked", "short_pin123", ""},              // exactly 12 → masked
		{"prefix from 13 characters", "sk_live_abcdef", "sk_l"}, // > 12 → first 4
		{"long token", "sk-ant-0123456789", "sk-a"},
		{"multibyte clean", "äöüßabcdefghij", "äöüß"}, // 14 runes → first 4 runes
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Preview(c.value); got != c.want {
				t.Fatalf("Preview(%q) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}
