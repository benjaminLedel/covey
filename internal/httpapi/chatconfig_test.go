package httpapi

import (
	"strings"
	"testing"

	"covey/internal/chat"
)

// TestAufzaehlen: the names of those who may accept a proposal go into the
// agent's line in the language of the request (#491).
func TestAufzaehlen(t *testing.T) {
	faelle := []struct {
		namen []string
		lang  string
		will  string
	}{
		{[]string{"Ada"}, "en", "Ada"},
		{[]string{"Ada", "Bernd"}, "de-DE,de;q=0.9", "Ada oder Bernd"},
		{[]string{"Ada", "Bernd", "Carla"}, "en-GB", "Ada, Bernd or Carla"},
		{[]string{"Ada", "Bernd"}, "fr", "Ada ou Bernd"},
		{[]string{"Ada", "Bernd"}, "xx", "Ada or Bernd"},
		{nil, "de", "ein Admin"},
		{nil, "en", "an admin"},
	}
	for _, f := range faelle {
		if got := aufzaehlen(f.namen, f.lang); got != f.will {
			t.Errorf("aufzaehlen(%v, %q) = %q, want %q", f.namen, f.lang, got, f.will)
		}
	}
}

// TestKonfigZeilen: the fallback lines keep the placeholder and the reason.
func TestKonfigZeilen(t *testing.T) {
	for _, lang := range []string{"de", "en"} {
		if !strings.Contains(konfigStandard(lang), chat.ApproversPlatzhalter) {
			t.Errorf("%s: the standard line must name who may accept", lang)
		}
		if !strings.Contains(konfigFehlschlag(lang), "%s") {
			t.Errorf("%s: the failure line must carry the reason", lang)
		}
	}
}
