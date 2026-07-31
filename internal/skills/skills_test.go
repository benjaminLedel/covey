package skills

import (
	"strings"
	"testing"
)

// ValidatePath ist eine Sicherheitsgrenze: Der Pfad wird beim Materialisieren
// an das Skill-Verzeichnis im Agenten-Home gehängt. Bricht er aus, schreibt
// Covey in fremde Teile des Home — etwa in .claude/settings.json.
func TestValidatePathRejectsTraversal(t *testing.T) {
	ok := []string{"SKILL.md", "reference.md", "vorlagen/mail.md", "a/b/c.txt", "SKILL.md.bak"}
	for _, p := range ok {
		if err := ValidatePath(p); err != nil {
			t.Errorf("%q muss erlaubt sein: %v", p, err)
		}
	}
	bad := []string{
		"",
		"..",
		"../SKILL.md",
		"a/../../b.md",
		"/etc/passwd",
		"/SKILL.md",
		"./SKILL.md",     // nicht normalisiert
		"a//b.md",        // leeres Segment
		"a/./b.md",       // nicht normalisiert
		"a/",             // Verzeichnis statt Datei
		`..\..\x.md`,     // Windows-Trenner
		"C:/x.md",        // Laufwerksbuchstabe
		"SKILL\x00.md",   // NUL
		"vorlagen/../..", // endet außerhalb
	}
	for _, p := range bad {
		if err := ValidatePath(p); err == nil {
			t.Errorf("%q muss abgelehnt werden", p)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, n := range []string{"deploy", "run-tests", "a", "skill-2", "x" + strings.Repeat("y", 62)} {
		if err := ValidateName(n); err != nil {
			t.Errorf("%q muss erlaubt sein: %v", n, err)
		}
	}
	for _, n := range []string{"", "Deploy", "mit leer", "-start", "über", "x/y", "..", strings.Repeat("z", 64)} {
		if err := ValidateName(n); err == nil {
			t.Errorf("%q muss abgelehnt werden", n)
		}
	}
}

// Ohne SKILL.md erkennt Claude Code das Verzeichnis nicht als Skill — der
// Agent bekäme still gar nichts. Das muss beim Speichern auffallen, nicht im
// Lauf.
func TestValidateFilesRequiresEntry(t *testing.T) {
	if err := validateFiles([]File{{Path: "reference.md", Content: "x"}}); err == nil {
		t.Fatal("ohne SKILL.md muss die Prüfung fehlschlagen")
	}
	if err := validateFiles([]File{{Path: EntryFile, Content: "x"}}); err != nil {
		t.Fatalf("mit SKILL.md muss sie durchgehen: %v", err)
	}
	if err := validateFiles(nil); err == nil {
		t.Fatal("leerer Dateisatz muss fehlschlagen")
	}
	dup := []File{{Path: EntryFile, Content: "a"}, {Path: EntryFile, Content: "b"}}
	if err := validateFiles(dup); err == nil {
		t.Fatal("doppelter Pfad muss fehlschlagen")
	}
	big := []File{{Path: EntryFile, Content: strings.Repeat("x", maxFileBytes+1)}}
	if err := validateFiles(big); err == nil {
		t.Fatal("zu große Datei muss fehlschlagen")
	}
	many := make([]File, 0, maxFiles+1)
	many = append(many, File{Path: EntryFile, Content: "x"})
	for i := 0; i < maxFiles; i++ {
		many = append(many, File{Path: string(rune('a'+i%26)) + string(rune('a'+i/26)) + ".md", Content: "x"})
	}
	if err := validateFiles(many); err == nil {
		t.Fatal("zu viele Dateien müssen fehlschlagen")
	}
}

// Der Frontmatter muss YAML-gültig bleiben, auch wenn die Beschreibung
// Doppelpunkte enthält — was sie fast immer tut („Nutze dies, wenn: …").
// Unquoted wäre das eine YAML-Map und Claude Code läse den Skill nicht.
func TestRenderQuotesRiskyScalars(t *testing.T) {
	out := Render("deploy", "Nutze dies, wenn: das Release ansteht", "# Deploy\n\nSchritt 1.")
	if !strings.Contains(out, `description: "Nutze dies, wenn: das Release ansteht"`) {
		t.Fatalf("Doppelpunkt muss quotiert werden:\n%s", out)
	}
	if !strings.HasPrefix(out, "---\nname: deploy\n") {
		t.Fatalf("Frontmatter falsch aufgebaut:\n%s", out)
	}
	if !strings.Contains(out, "---\n\n# Deploy") {
		t.Fatalf("Rumpf muss hinter dem Block stehen:\n%s", out)
	}
	// Anführungszeichen in der Beschreibung dürfen den Block nicht sprengen.
	quoted := Render("x", `sagt "hallo" und \ende`, "b")
	if !strings.Contains(quoted, `description: "sagt \"hallo\" und \\ende"`) {
		t.Fatalf("Anführungszeichen/Backslash falsch escaped:\n%s", quoted)
	}
	// Mehrzeilige Beschreibung würde den Block sprengen — muss einzeilig werden.
	multi := Render("x", "erste\nzweite", "b")
	if strings.Count(multi[:strings.Index(multi, "---\n\n")], "\n") != 3 {
		t.Fatalf("Beschreibung muss auf eine Zeile gefaltet werden:\n%s", multi)
	}
}

func TestSplitEntry(t *testing.T) {
	name, desc, body := SplitEntry("---\nname: deploy\ndescription: \"Nutze dies, wenn: X\"\n---\n\n# Rumpf\n")
	if name != "deploy" || desc != "Nutze dies, wenn: X" || body != "# Rumpf\n" {
		t.Fatalf("Frontmatter falsch getrennt: %q %q %q", name, desc, body)
	}

	// Ohne Frontmatter ist alles Rumpf, die Werte bleiben leer.
	name, desc, body = SplitEntry("# Nur Rumpf\n")
	if name != "" || desc != "" || body != "# Nur Rumpf\n" {
		t.Fatalf("ohne Frontmatter darf nichts abgeschnitten werden: %q %q %q", name, desc, body)
	}

	// Unabgeschlossener Block: nicht raten, sonst verschluckt der Import den Inhalt.
	_, _, body = SplitEntry("---\nname: x\n\n# Rumpf ohne Ende\n")
	if !strings.Contains(body, "# Rumpf ohne Ende") || !strings.HasPrefix(body, "---") {
		t.Fatalf("unabgeschlossener Block muss unangetastet bleiben: %q", body)
	}

	// Ein --- im Rumpf (horizontale Linie) darf den ersten Block nicht stören.
	_, desc, body = SplitEntry("---\ndescription: A\n---\n\ntext\n\n---\n\nmehr\n")
	if desc != "A" || !strings.Contains(body, "mehr") || strings.HasPrefix(body, "description") {
		t.Fatalf("zweites --- falsch behandelt: %q / %q", desc, body)
	}

	// Round-Trip: Was Render schreibt, muss SplitEntry wieder auseinandernehmen.
	in := `Nutze dies, wenn: "X" & Y`
	n2, d2, b2 := SplitEntry(Render("round-trip", in, "# Body\n\nText"))
	if n2 != "round-trip" || d2 != in || !strings.HasPrefix(b2, "# Body") {
		t.Fatalf("Round-Trip verloren: %q %q %q", n2, d2, b2)
	}
}
