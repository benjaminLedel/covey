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
			"SKILL.md":          "# Deploy\n\nSchritt 1.",
			"checkliste.md":     "- [ ] Backup",
			"vorlagen/mail.md":  "Betreff: …",
			"../../boeser-pfad": "darf nicht rausbrechen",
			"/absolut":          "auch nicht",
		},
	}})
	if err != nil {
		t.Fatalf("writeSkillDirs: %v", err)
	}

	entry, err := os.ReadFile(filepath.Join(dir, "deploy", "SKILL.md"))
	if err != nil {
		t.Fatalf("SKILL.md fehlt: %v", err)
	}
	// Der Frontmatter entsteht beim Schreiben — er darf nicht aus dem
	// gespeicherten Rumpf kommen, sonst stünde die Beschreibung doppelt.
	if !strings.HasPrefix(string(entry), "---\nname: deploy\n") {
		t.Fatalf("Frontmatter fehlt oder falsch:\n%s", entry)
	}
	if !strings.Contains(string(entry), `description: "Nutze dies, wenn: das Release ansteht"`) {
		t.Fatalf("Beschreibung muss quotiert im Frontmatter stehen:\n%s", entry)
	}
	if !strings.Contains(string(entry), "# Deploy") {
		t.Fatalf("Rumpf fehlt:\n%s", entry)
	}

	// Zusatzdateien, auch in Unterverzeichnissen — das ist der Kern des
	// Features: schweres Material, das nur bei Bedarf gelesen wird.
	if _, err := os.Stat(filepath.Join(dir, "deploy", "checkliste.md")); err != nil {
		t.Fatalf("Zusatzdatei fehlt: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "vorlagen", "mail.md")); err != nil {
		t.Fatalf("Datei im Unterverzeichnis fehlt: %v", err)
	}

	// Traversal darf nichts außerhalb angelegt haben.
	if _, err := os.Stat(filepath.Join(filepath.Dir(filepath.Dir(dir)), "boeser-pfad")); err == nil {
		t.Fatal("Pfad-Traversal hat außerhalb geschrieben")
	}
	if _, err := os.Stat("/absolut"); err == nil {
		t.Fatal("absoluter Pfad wurde geschrieben")
	}
}

// Der wichtigste Test des Features: Das Home überlebt den Lauf (warm_sandbox,
// persistentes /home). Wird ein Skill in der Control Plane gelöscht oder einem
// Agenten abgehängt, MUSS er aus dem Home verschwinden — sonst bliebe eine
// entzogene Fähigkeit für immer wirksam, und die zentrale Verwaltung wäre eine
// Fiktion.
func TestWriteSkillDirsRemovesWithdrawnSkills(t *testing.T) {
	dir := t.TempDir()
	first := []SkillDir{
		{Name: "deploy", Description: "d", Files: map[string]string{"SKILL.md": "a", "alt.md": "weg damit"}},
		{Name: "triage", Description: "t", Files: map[string]string{"SKILL.md": "b"}},
	}
	if err := writeSkillDirs(dir, first); err != nil {
		t.Fatalf("erster Lauf: %v", err)
	}

	// Zweiter Lauf: triage ist abgehängt, deploy hat alt.md verloren.
	second := []SkillDir{
		{Name: "deploy", Description: "d", Files: map[string]string{"SKILL.md": "a2"}},
	}
	if err := writeSkillDirs(dir, second); err != nil {
		t.Fatalf("zweiter Lauf: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "triage")); !os.IsNotExist(err) {
		t.Fatal("abgehängter Skill muss aus dem Home verschwinden")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "alt.md")); !os.IsNotExist(err) {
		t.Fatal("entfernte Datei eines Skills muss verschwinden")
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "SKILL.md")); err != nil {
		t.Fatalf("verbliebener Skill muss erhalten bleiben: %v", err)
	}

	// Ohne Skills bleibt ein leeres, aber vorhandenes Verzeichnis zurück.
	if err := writeSkillDirs(dir, nil); err != nil {
		t.Fatalf("leerer Lauf: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("Verzeichnis muss leer sein: %v %d", err, len(entries))
	}
}

// Fremde Dateien im Skills-Verzeichnis räumt Covey mit ab. Das Verzeichnis
// gehört der Control Plane — was der Agent dort ablegt, überlebt bewusst nicht.
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
		t.Fatalf("nur der gültige Name darf angelegt werden, bekam: %v", got)
	}
}

// Bleibt die Antwort der Control Plane aus, muss das Verzeichnis GELEERT
// werden. Das Home überlebt den Lauf: Ohne diesen Schritt liefe die Aufgabe
// nicht „ohne Skills", sondern mit den alten — ein gerade entzogener Skill
// wirkte weiter, und der Entzug wäre fail-open.
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
		t.Fatalf("nach dem Abräumen darf nichts übrig sein, bekam: %+v", entries)
	}
	// Nie materialisiert: kein Verzeichnis, kein Fehler.
	if err := clearSkillDirs(filepath.Join(t.TempDir(), "gibtsnicht")); err != nil {
		t.Fatalf("fehlendes Verzeichnis ist kein Fehler: %v", err)
	}
}
