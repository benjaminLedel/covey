package templates

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	"covey/examples"
)

// The bundled templates are shared across organisations and read-only. They
// are projected onto the same type as an organisation's own, so the interface
// shows one list — which only works if the projection stays complete.
func TestBuiltinTemplatesAreCompleteAndMarked(t *testing.T) {
	got := builtinTemplates("de")
	if len(got) == 0 {
		t.Fatal("no bundled templates — the library would be empty in a fresh installation")
	}
	if len(got) != len(examples.Builtins()) {
		t.Fatalf("got %d templates, the examples carry %d", len(got), len(examples.Builtins()))
	}
	seen := map[uuid.UUID]bool{}
	for _, tpl := range got {
		if !tpl.Builtin {
			t.Errorf("%q is not marked as bundled — it would then look deletable", tpl.Name)
		}
		if tpl.OrgID != uuid.Nil {
			t.Errorf("%q carries an organisation (%s) although it belongs to all of them", tpl.Name, tpl.OrgID)
		}
		if tpl.Name == "" || tpl.Description == "" {
			t.Errorf("%s: name or description empty (%q / %q)", tpl.ID, tpl.Name, tpl.Description)
		}
		if !json.Valid(tpl.Bundle) {
			t.Errorf("%q carries no readable bundle", tpl.Name)
		}
		if seen[tpl.ID] {
			t.Errorf("id %s appears twice — Get would answer the first one for both", tpl.ID)
		}
		seen[tpl.ID] = true
	}
}

// The language decides name and description. Ten catalogues sit behind the
// interface; a library that answered English everywhere would be the one place
// that does not follow.
func TestBuiltinTemplatesFollowTheLanguage(t *testing.T) {
	de, en := builtinTemplates("de"), builtinTemplates("en")
	if len(de) != len(en) {
		t.Fatalf("the two languages carry different numbers of templates (%d/%d)", len(de), len(en))
	}
	differs := false
	for i := range de {
		if de[i].ID != en[i].ID {
			t.Fatalf("the order differs between languages at %d", i)
		}
		if de[i].Name != en[i].Name || de[i].Description != en[i].Description {
			differs = true
		}
	}
	if !differs {
		t.Error("German and English are identical everywhere — the localisation does not arrive")
	}
}

func TestBuiltinByID(t *testing.T) {
	all := builtinTemplates("de")
	if len(all) == 0 {
		t.Skip("no bundled templates")
	}
	want := all[0]
	got, ok := builtinByID(want.ID, "de")
	if !ok {
		t.Fatalf("%s was not found", want.ID)
	}
	if got.Name != want.Name || got.ID != want.ID {
		t.Errorf("got %q/%s, expected %q/%s", got.Name, got.ID, want.Name, want.ID)
	}
	if _, ok := builtinByID(uuid.New(), "de"); ok {
		t.Error("an unknown id was answered with a template")
	}
}

// Delete checks the bundled ones without a language, because the language is
// irrelevant to the question — it must find them anyway.
func TestBuiltinByIDFindsWithoutALanguage(t *testing.T) {
	all := builtinTemplates("")
	if len(all) == 0 {
		t.Skip("no bundled templates")
	}
	if _, ok := builtinByID(all[0].ID, ""); !ok {
		t.Error("without a language the bundled template was not found — Delete would then delete it")
	}
}
