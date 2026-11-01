package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillsValidate(t *testing.T) {
	root := t.TempDir()
	good := filepath.Join(root, "pdf")
	os.MkdirAll(good, 0o755)
	os.WriteFile(filepath.Join(good, "SKILL.md"), []byte("---\nname: pdf\ndescription: PDFs.\nlicense: MIT\n---\n# PDF\n"), 0o644)

	var out bytes.Buffer
	if err := runSkillsValidate(&out, []string{root}); err != nil {
		t.Fatalf("a valid skill must pass: %v\n%s", err, out.String())
	}
	// The skill directory itself as the argument: its name is checked
	// against the directory.
	if err := runSkillsValidate(&out, []string{good}); err != nil {
		t.Fatalf("the skill directory itself must pass: %v\n%s", err, out.String())
	}

	bad := filepath.Join(root, "Bad--Name")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "SKILL.md"), []byte("---\nname: other\nmodel: opus\n---\n"), 0o644)
	out.Reset()
	err := runSkillsValidate(&out, []string{root})
	if err == nil {
		t.Fatalf("an invalid skill must fail:\n%s", out.String())
	}
	for _, want := range []string{"ok   pdf", "FAIL", "model", "description missing"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}
