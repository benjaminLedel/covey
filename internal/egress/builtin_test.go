package egress

import "testing"

// The catalogue must be internally consistent: unique slugs and names, and
// every host pattern passes NormalizePattern unchanged — otherwise importing
// would store something other than what the catalogue promises.
func TestBuiltinsConsistent(t *testing.T) {
	slugs := map[string]bool{}
	names := map[string]bool{}
	for _, b := range Builtins {
		if b.Slug == "" || b.Name == "" || b.Description == "" {
			t.Errorf("builtin %q: slug, name and description must be set", b.Slug)
		}
		if slugs[b.Slug] {
			t.Errorf("slug %q is duplicated", b.Slug)
		}
		slugs[b.Slug] = true
		if names[b.Name] {
			t.Errorf("name %q is duplicated", b.Name)
		}
		names[b.Name] = true
		if len(b.Hosts) == 0 {
			t.Errorf("builtin %q: no hosts", b.Slug)
		}
		seen := map[string]bool{}
		for _, h := range b.Hosts {
			norm, err := NormalizePattern(h.Pattern)
			if err != nil {
				t.Errorf("builtin %q: pattern %q invalid: %v", b.Slug, h.Pattern, err)
				continue
			}
			if norm != h.Pattern {
				t.Errorf("builtin %q: pattern %q not in normal form (want %q)", b.Slug, h.Pattern, norm)
			}
			if seen[h.Pattern] {
				t.Errorf("builtin %q: pattern %q is duplicated", b.Slug, h.Pattern)
			}
			seen[h.Pattern] = true
			if h.Note == "" {
				t.Errorf("builtin %q: pattern %q without a note", b.Slug, h.Pattern)
			}
		}
	}
}

func TestBuiltinBySlug(t *testing.T) {
	if _, ok := BuiltinBySlug("github"); !ok {
		t.Fatal("github must be in the catalogue")
	}
	if _, ok := BuiltinBySlug("does-not-exist"); ok {
		t.Fatal("an unknown slug must return nothing")
	}
}
