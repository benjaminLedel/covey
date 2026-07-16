package secrets

import "testing"

func TestPreview(t *testing.T) {
	cases := []struct {
		name  string
		value string
		want  string
	}{
		{"leer", "", ""},
		{"kurz bleibt maskiert", "short_pin123", ""},   // genau 12 → maskiert
		{"ab 13 Zeichen Präfix", "sk_live_abcdef", "sk_l"}, // > 12 → erste 4
		{"langer Token", "sk-ant-0123456789", "sk-a"},
		{"multibyte sauber", "äöüßabcdefghij", "äöüß"}, // 14 Runen → erste 4 Runen
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Preview(c.value); got != c.want {
				t.Fatalf("Preview(%q) = %q, want %q", c.value, got, c.want)
			}
		})
	}
}
