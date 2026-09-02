package skills

import (
	"strings"
	"testing"
)

// ValidatePath is a security boundary: the path is appended to the skill
// directory in the agent home while materializing. If it breaks out, covey
// writes into other parts of the home — .claude/settings.json, say.
func TestValidatePathRejectsTraversal(t *testing.T) {
	ok := []string{"SKILL.md", "reference.md", "templates/mail.md", "a/b/c.txt", "SKILL.md.bak"}
	for _, p := range ok {
		if err := ValidatePath(p); err != nil {
			t.Errorf("%q must be allowed: %v", p, err)
		}
	}
	bad := []string{
		"",
		"..",
		"../SKILL.md",
		"a/../../b.md",
		"/etc/passwd",
		"/SKILL.md",
		"./SKILL.md",      // not normalized
		"a//b.md",         // empty segment
		"a/./b.md",        // not normalized
		"a/",              // directory instead of file
		`..\..\x.md`,      // Windows separator
		"C:/x.md",         // drive letter
		"SKILL\x00.md",    // NUL
		"templates/../..", // ends up outside
	}
	for _, p := range bad {
		if err := ValidatePath(p); err == nil {
			t.Errorf("%q must be rejected", p)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, n := range []string{"deploy", "run-tests", "a", "skill-2", "x" + strings.Repeat("y", 62)} {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q must be allowed: %v", n, err)
		}
	}
	for _, n := range []string{"", "Deploy", "with space", "-start", "über", "x/y", "..", strings.Repeat("z", 64)} {
		if err := ValidateName(n); err == nil {
			t.Errorf("%q must be rejected", n)
		}
	}
}

// Without SKILL.md Claude Code does not recognize the directory as a skill —
// the agent would silently get nothing at all. That has to surface on save,
// not during a run.
func TestValidateFilesRequiresEntry(t *testing.T) {
	if err := validateFiles([]File{{Path: "reference.md", Content: "x"}}); err == nil {
		t.Fatal("without SKILL.md the check must fail")
	}
	if err := validateFiles([]File{{Path: EntryFile, Content: "x"}}); err != nil {
		t.Fatalf("with SKILL.md it must pass: %v", err)
	}
	if err := validateFiles(nil); err == nil {
		t.Fatal("an empty file set must fail")
	}
	dup := []File{{Path: EntryFile, Content: "a"}, {Path: EntryFile, Content: "b"}}
	if err := validateFiles(dup); err == nil {
		t.Fatal("a duplicate path must fail")
	}
	big := []File{{Path: EntryFile, Content: strings.Repeat("x", maxFileBytes+1)}}
	if err := validateFiles(big); err == nil {
		t.Fatal("an oversized file must fail")
	}
	many := make([]File, 0, maxFiles+1)
	many = append(many, File{Path: EntryFile, Content: "x"})
	for i := 0; i < maxFiles; i++ {
		many = append(many, File{Path: string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".md", Content: "x"})
	}
	if err := validateFiles(many); err == nil {
		t.Fatal("too many files must fail")
	}
}

// The frontmatter has to stay valid YAML even when the description contains
// colons — which it almost always does ("Use this when: …"). Unquoted that
// would be a YAML map and Claude Code would not read the skill.
func TestRenderQuotesRiskyScalars(t *testing.T) {
	out := Render("deploy", "Use this when: the release is due", "# Deploy\n\nStep 1.")
	if !strings.Contains(out, `description: "Use this when: the release is due"`) {
		t.Fatalf("the colon must be quoted:\n%s", out)
	}
	if !strings.HasPrefix(out, "---\nname: deploy\n") {
		t.Fatalf("frontmatter built wrong:\n%s", out)
	}
	if !strings.Contains(out, "---\n\n# Deploy") {
		t.Fatalf("the body must come after the block:\n%s", out)
	}
	// Quotes in the description must not blow up the block.
	quoted := Render("x", `says "hello" and \end`, "b")
	if !strings.Contains(quoted, `description: "says \"hello\" and \\end"`) {
		t.Fatalf("quotes/backslash escaped wrong:\n%s", quoted)
	}
	// A multi-line description would blow up the block — it must be folded.
	multi := Render("x", "first\nsecond", "b")
	if strings.Count(multi[:strings.Index(multi, "---\n\n")], "\n") != 3 {
		t.Fatalf("the description must be folded onto one line:\n%s", multi)
	}
}

func TestSplitEntry(t *testing.T) {
	name, desc, body := SplitEntry("---\nname: deploy\ndescription: \"Use this when: X\"\n---\n\n# Body\n")
	if name != "deploy" || desc != "Use this when: X" || body != "# Body\n" {
		t.Fatalf("frontmatter split wrong: %q %q %q", name, desc, body)
	}

	// Without frontmatter everything is body and the values stay empty.
	name, desc, body = SplitEntry("# Body only\n")
	if name != "" || desc != "" || body != "# Body only\n" {
		t.Fatalf("without frontmatter nothing may be cut off: %q %q %q", name, desc, body)
	}

	// Unterminated block: do not guess, otherwise the import swallows content.
	_, _, body = SplitEntry("---\nname: x\n\n# Body without end\n")
	if !strings.Contains(body, "# Body without end") || !strings.HasPrefix(body, "---") {
		t.Fatalf("an unterminated block must stay untouched: %q", body)
	}

	// A --- in the body (horizontal rule) must not disturb the first block.
	_, desc, body = SplitEntry("---\ndescription: A\n---\n\ntext\n\n---\n\nmore\n")
	if desc != "A" || !strings.Contains(body, "more") || strings.HasPrefix(body, "description") {
		t.Fatalf("second --- handled wrong: %q / %q", desc, body)
	}

	// Round trip: what Render writes, SplitEntry must take apart again.
	in := `Use this when: "X" & Y`
	n2, d2, b2 := SplitEntry(Render("round-trip", in, "# Body\n\nText"))
	if n2 != "round-trip" || d2 != in || !strings.HasPrefix(b2, "# Body") {
		t.Fatalf("round trip lost: %q %q %q", n2, d2, b2)
	}
}
