package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteSkillDirsLayout(t *testing.T) {
	dir := t.TempDir()
	err := writeSkillDirs(dir, []SkillDir{{
		Name:        "deploy",
		Description: "Nutze dies, wenn: das Release ansteht",
		Files: map[string]string{
			"SKILL.md":         "# Deploy\n\nSchritt 1.",
			"checkliste.md":    "- [ ] Backup",
			"vorlagen/mail.md": "Betreff: …",
			"../../evil-path":  "darf nicht rausbrechen",
			"/absolut":         "auch nicht",
		},
	}})
	if err != nil {
		t.Fatalf("writeSkillDirs: %v", err)
	}

	entry, err := os.ReadFile(filepath.Join(dir, "deploy", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md missing: %v", err)
	}
	// The frontmatter is produced while writing — it must not come out of the
	// stored body, otherwise the description would appear twice.
	if !strings.HasPrefix(string(entry), "---\nname: deploy\n") {
		t.Fatalf("frontmatter missing or wrong:\n%s", entry)
	}
	if !strings.Contains(string(entry), `description: "Nutze dies, wenn: das Release ansteht"`) {
		t.Fatalf("the description must appear quoted in the frontmatter:\n%s", entry)
	}
	if !strings.Contains(string(entry), "# Deploy") {
		t.Fatalf("body missing:\n%s", entry)
	}

	// Additional files, including in subdirectories — that is the core of the
	// feature: heavy material that is only read when needed.
	if _, err := os.Stat(filepath.Join(dir, "deploy", "checkliste.md")); err != nil {
		t.Fatalf("additional file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "vorlagen", "mail.md")); err != nil {
		t.Fatalf("file in a subdirectory missing: %v", err)
	}

	// Traversal must not have created anything outside.
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), "evil-path")); err == nil {
		t.Fatal("path traversal wrote outside")
	}
	if _, err := os.Stat("/absolut"); err == nil {
		t.Fatal("an absolute path was written")
	}
}

// The most important test of the feature: the home outlives the run
// (warm_sandbox, persistent /home). If a skill is deleted in the control plane
// or detached from an agent, it MUST disappear from the home — otherwise a
// withdrawn capability would stay effective forever, and central management
// would be a fiction.
func TestWriteSkillDirsRemovesWithdrawnSkills(t *testing.T) {
	dir := t.TempDir()
	first := []SkillDir{
		{Name: "deploy", Description: "d", Files: map[string]string{"SKILL.md": "a", "alt.md": "weg damit"}},
		{Name: "triage", Description: "t", Files: map[string]string{"SKILL.md": "b"}},
	}
	if err := writeSkillDirs(dir, first); err != nil {
		t.Fatalf("first run: %v", err)
	}

	// Second run: triage is detached, deploy has lost alt.md.
	second := []SkillDir{
		{Name: "deploy", Description: "d", Files: map[string]string{"SKILL.md": "a2"}},
	}
	if err := writeSkillDirs(dir, second); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "triage")); !os.IsNotExist(err) {
		t.Fatal("a detached skill must disappear from the home")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "alt.md")); !os.IsNotExist(err) {
		t.Fatal("a removed file of a skill must disappear")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "SKILL.md")); err != nil {
		t.Fatalf("a remaining skill must be preserved: %v", err)
	}

	// Without skills an empty but existing directory remains.
	if err := writeSkillDirs(dir, nil); err != nil {
		t.Fatalf("empty run: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("the directory must be empty: %v %d", err, len(entries))
	}
}

// covey also clears away foreign files in the skills directory. The directory
// belongs to the control plane — whatever the agent puts there deliberately does
// not survive.
func TestWriteSkillDirsRejectsUnsafeNames(t *testing.T) {
	dir := t.TempDir()
	err := writeSkillDirs(dir, []SkillDir{
		{Name: "..", Description: "x", Files: map[string]string{"SKILL.md": "a"}},
		{Name: "Groß", Description: "x", Files: map[string]string{"SKILL.md": "a"}},
		{Name: "mit/slash", Description: "x", Files: map[string]string{"SKILL.md": "a"}},
		{Name: "gut", Description: "x", Files: map[string]string{"SKILL.md": "a"}},
	})
	if err != nil {
		t.Fatalf("writeSkillDirs: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "gut" {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}
		t.Fatalf("only the valid name may be created, got: %v", got)
	}
}

// If the control plane's answer fails to arrive, the directory must be EMPTIED.
// The home outlives the run: without this step the task would not run "without
// skills" but with the old ones — a skill just withdrawn would keep working, and
// the withdrawal would be fail-open.
func TestClearSkillDirsRemovesEverything(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".claude", "skills")
	if err := writeSkillDirs(dir, []SkillDir{
		{Name: "alt", Description: "x", Files: map[string]string{"SKILL.md": "a", "ref.md": "b"}},
	}); err != nil {
		t.Fatal(err)
	}
	if err := clearSkillDirs(dir); err != nil {
		t.Fatalf("clearSkillDirs: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("after clearing nothing may be left, got: %+v", entries)
	}
	// Never materialized: no directory, no error.
	if err := clearSkillDirs(filepath.Join(t.TempDir(), "gibtsnicht")); err != nil {
		t.Fatalf("a missing directory is not an error: %v", err)
	}
}
