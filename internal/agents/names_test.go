package agents

import (
	"strings"
	"testing"
)

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"Renate Büroklammer":    "renate-bueroklammer",
		"Dr. Dr. Egon Rastlos":  "dr-dr-egon-rastlos",
		"Reg of Clipboard":      "reg-of-clipboard",
		"Käthe Groß-Schön":      "kaethe-gross-schoen",
		"  --Hans-Peter--  ":    "hans-peter",
		"Wuselbert Wibbelzahn":  "wuselbert-wibbelzahn",
		"Heinz-Rüdiger W. Möpp": "heinz-ruediger-w-moepp",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

// A rolled name has to be usable as-is: a display name with a first and a last
// part, and a slug that survives being put into a URL. The generator is random,
// so this checks the properties rather than a value.
func TestRollName(t *testing.T) {
	for _, lang := range []string{"de", "en", "fr"} {
		for i := 0; i < 300; i++ {
			n := RollName(lang)
			if !strings.Contains(strings.TrimSpace(n.Name), " ") {
				t.Fatalf("%s: %q has no surname", lang, n.Name)
			}
			if n.Slug == "" || n.Slug != Slugify(n.Name) {
				t.Fatalf("%s: slug %q does not fit the name %q", lang, n.Slug, n.Name)
			}
			if strings.HasPrefix(n.Slug, "-") || strings.HasSuffix(n.Slug, "-") ||
				strings.Contains(n.Slug, "--") {
				t.Fatalf("%s: unusable slug %q", lang, n.Slug)
			}
		}
	}
}

// The seam of two made-up syllables must not produce three identical letters.
func TestCollapse(t *testing.T) {
	cases := map[string]string{
		"Knufffix":  "Knuffix",
		"Schlonnnz": "Schlonnz",
		"Wusel":     "Wusel",
		"":          "",
	}
	for in, want := range cases {
		if got := collapse(in); got != want {
			t.Errorf("collapse(%q) = %q, want %q", in, got, want)
		}
	}
}
