package skills

import (
	"strings"
	"testing"
)

// SplitEntry cuts the whole block off and Render rebuilds it from name and
// description. So a key that is neither is silently gone after saving — and
// with `allowed-tools:` the materialised SKILL.md then runs with MORE
// permissions than its author wrote. That is why the caller rejects instead
// of storing.
func TestUnsupportedFrontmatterKeys(t *testing.T) {
	for _, tc := range []struct {
		name    string
		content string
		want    []string
	}{
		{"only what survives", "---\nname: a\ndescription: b\n---\nText", nil},
		{"a restriction that would be lost", "---\nname: a\nallowed-tools: Bash\n---\n", []string{"allowed-tools"}},
		{"several", "---\nname: a\nmodel: opus\nlicense: MIT\n---\n", []string{"model", "license"}},
		// Continuation lines of a list belong to the key above, which is
		// already reported — naming them again would send the author looking
		// for a key that is not there.
		{"list items are not keys", "---\nallowed-tools:\n  - Bash\n  - Read\n---\n", []string{"allowed-tools"}},
		{"comments and blank lines", "---\n# ein Kommentar\n\nname: a\n---\n", nil},
		{"no frontmatter at all", "Nur Text\n", nil},
		// An unterminated block is body, exactly as SplitEntry reads it —
		// otherwise the two would disagree about the same file.
		{"unterminated block", "---\nname: a\nkeine Schlusszeile\n", nil},
		{"leading BOM and blank lines", "\ufeff\n---\nmodel: opus\n---\n", []string{"model"}},
		{"line without a colon", "---\nname: a\neinfach text\n---\n", nil},
	} {
		got := UnsupportedFrontmatterKeys(tc.content)
		if len(got) != len(tc.want) {
			t.Errorf("%s: got %v, expected %v", tc.name, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("%s: [%d] = %q, expected %q", tc.name, i, got[i], tc.want[i])
			}
		}
	}
}

// The two have to read the same file the same way: what SplitEntry treats as
// body must not be reported as a lost key, and vice versa.
func TestUnsupportedKeysAndSplitEntryAgreeOnWhatIsFrontmatter(t *testing.T) {
	const unterminated = "---\nallowed-tools: Bash\nnoch Text\n"
	if keys := UnsupportedFrontmatterKeys(unterminated); keys != nil {
		t.Errorf("keys reported in an unterminated block: %v", keys)
	}
	_, _, body := SplitEntry(unterminated)
	if !strings.Contains(body, "allowed-tools") {
		t.Error("SplitEntry treated the unterminated block as frontmatter — the two disagree")
	}
}
