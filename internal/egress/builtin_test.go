package egress

import "testing"

// Der Katalog muss in sich konsistent sein: eindeutige Slugs und Namen,
// und jedes Host-Muster passiert NormalizePattern unverändert — sonst würde
// beim Übernehmen etwas anderes gespeichert, als der Katalog verspricht.
func TestBuiltinsConsistent(t *testing.T) {
	slugs := map[string]bool{}
	names := map[string]bool{}
	for _, b := range Builtins {
		if b.Slug == "" || b.Name == "" || b.Description == "" {
			t.Errorf("builtin %q: slug, name und description müssen gesetzt sein", b.Slug)
		}
		if slugs[b.Slug] {
			t.Errorf("slug %q doppelt", b.Slug)
		}
		slugs[b.Slug] = true
		if names[b.Name] {
			t.Errorf("name %q doppelt", b.Name)
		}
		names[b.Name] = true
		if len(b.Hosts) == 0 {
			t.Errorf("builtin %q: keine hosts", b.Slug)
		}
		seen := map[string]bool{}
		for _, h := range b.Hosts {
			norm, err := NormalizePattern(h.Pattern)
			if err != nil {
				t.Errorf("builtin %q: muster %q ungültig: %v", b.Slug, h.Pattern, err)
				continue
			}
			if norm != h.Pattern {
				t.Errorf("builtin %q: muster %q nicht in Normalform (erwartet %q)", b.Slug, h.Pattern, norm)
			}
			if seen[h.Pattern] {
				t.Errorf("builtin %q: muster %q doppelt", b.Slug, h.Pattern)
			}
			seen[h.Pattern] = true
			if h.Note == "" {
				t.Errorf("builtin %q: muster %q ohne notiz", b.Slug, h.Pattern)
			}
		}
	}
}

func TestBuiltinBySlug(t *testing.T) {
	if _, ok := BuiltinBySlug("github"); !ok {
		t.Fatal("github muss im Katalog sein")
	}
	if _, ok := BuiltinBySlug("gibtsnicht"); ok {
		t.Fatal("unbekannter slug darf nichts liefern")
	}
}
