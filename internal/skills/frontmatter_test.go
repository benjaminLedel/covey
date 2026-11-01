package skills

import (
	"errors"
	"strings"
	"testing"
)

// The specification's optional fields survive; everything else is refused
// rather than dropped — Render rebuilds the block from the columns, so a key
// covey did not keep would be gone after saving, and with a runtime-specific
// key like `hooks:` that changes what the skill does.
func TestParseEntryKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		bad     []string
	}{
		{"name and description", "---\nname: a\ndescription: b\n---\nText", nil},
		{"all fields of the specification", "---\nname: a\ndescription: b\nlicense: MIT\ncompatibility: needs git\nallowed-tools: Bash Read\nmetadata:\n  author: x\n---\n", nil},
		{"a runtime-specific key", "---\nname: a\nmodel: opus\n---\n", []string{"model"}},
		{"several", "---\nname: a\nhooks: x\nmodel: opus\n---\n", []string{"hooks", "model"}},
		{"comments and blank lines", "---\n# ein Kommentar\n\nname: a\n---\n", nil},
		{"no frontmatter at all", "Nur Text\n", nil},
		{"unterminated block is body", "---\nmodel: a\nkeine Schlusszeile\n", nil},
		{"leading BOM and blank lines", "\ufeff\n---\nmodel: opus\n---\n", []string{"model"}},
	} {
		_, _, err := ParseEntry(tc.content)
		if len(tc.bad) == 0 {
			if err != nil {
				t.Errorf("%s: unexpected error %v", tc.name, err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: expected ErrInvalid, got %v", tc.name, err)
			continue
		}
		for _, k := range tc.bad {
			if !strings.Contains(err.Error(), k) {
				t.Errorf("%s: error does not name %q: %v", tc.name, k, err)
			}
		}
	}
}

func TestParseEntryValues(t *testing.T) {
	const in = `---
name: pdf-processing
description: >
  Extract text from PDFs.
  Use when: a PDF arrives.
license: Proprietary. LICENSE.txt has complete terms
allowed-tools:
  - Bash(python:*)
  - Read
metadata:
  version: 1.0
  author: example-org
---
# PDF
`
	f, body, err := ParseEntry(in)
	if err != nil {
		t.Fatal(err)
	}
	if f.Description != "Extract text from PDFs. Use when: a PDF arrives." {
		t.Errorf("folded description read wrong: %q", f.Description)
	}
	// A YAML list is joined into the specification's space-separated form.
	if f.AllowedTools != "Bash(python:*) Read" {
		t.Errorf("allowed-tools list read wrong: %q", f.AllowedTools)
	}
	// A number in metadata stays the string it was written as.
	if f.Metadata["version"] != "1.0" {
		t.Errorf("metadata number read wrong: %q", f.Metadata["version"])
	}
	if body != "# PDF\n" {
		t.Errorf("body wrong: %q", body)
	}

	for _, bad := range []string{
		"---\nname: [a, b]\n---\n",
		"---\nmetadata: just text\n---\n",
		"---\nmetadata:\n  nested:\n    deep: x\n---\n",
		"---\nname: a\n  bad indent: x\n---\n",
		"---\n- a list\n---\n",
	} {
		if _, _, err := ParseEntry(bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q must be refused, got %v", bad, err)
		}
	}
}

// Values YAML would read as something else must come back as written.
func TestRenderQuotesNonStrings(t *testing.T) {
	out := Render("x", "true", Meta{Metadata: map[string]string{"version": "1.0", "on": "yes"}}, "b")
	f, _, err := ParseEntry(out)
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if f.Description != "true" || f.Metadata["version"] != "1.0" || f.Metadata["on"] != "yes" {
		t.Fatalf("scalars changed in the round trip: %+v\n%s", f, out)
	}
}

func TestAssembleFormWinsOverFrontmatter(t *testing.T) {
	files := []File{{Path: EntryFile, Content: "---\nname: from-file\ndescription: file\nlicense: MIT\n---\nBody"}}
	spec, err := Assemble(Front{Description: "form"}, nil, files)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "from-file" || spec.Description != "form" || spec.License != "MIT" {
		t.Fatalf("merge wrong: %+v", spec)
	}
	if spec.Files[0].Content != "Body" {
		t.Fatalf("frontmatter not stripped: %q", spec.Files[0].Content)
	}
}
