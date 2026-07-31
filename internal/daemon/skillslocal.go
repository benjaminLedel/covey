package daemon

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Skills im Agenten-Home materialisieren.
//
// Claude Code lädt persönliche Skills aus ~/.claude/skills/<name>/SKILL.md.
// Der Daemon startet den Lauf mit HOME=<Agenten-Home> (runtime_claudecode.go),
// also wird ein dort geschriebenes Verzeichnis zum persönlichen Skill genau
// dieses Agenten — ohne dass Covey am Prompt etwas ändern müsste.
//
// Der Gewinn liegt in der Ladezeit: Die description steht immer im Kontext,
// Rumpf und Zusatzdateien liest die Runtime erst, wenn der Skill zieht. Genau
// deshalb gehören Prozeduren hierher und nicht in PLAYBOOKS.md, das jeder Lauf
// vollständig mitschleppt.
//
// Anders als beim Wiki gibt es keinen Rückweg in die Control Plane. Skills sind
// zentral verwaltete Config; ein Lauf, der sich selbst neue Fähigkeiten
// schreiben könnte, würde die Kontrolle aushebeln, die das Feature erst
// rechtfertigt.

// skillsDirName ist das Verzeichnis, in dem Claude Code persönliche Skills sucht.
const skillsDirName = ".claude/skills"

// skillsRequestTimeout ist bewusst kürzer als die 30 s der übrigen Broker-
// Aufrufe. Die Anfrage sitzt VOR jedem Lauf im kritischen Pfad, und ihre
// Antwort ist verzichtbar. Kennt die Control Plane request_skills nicht (älter
// als dieses Feature — coveyd steckt im Sandbox-Image und hinkt beim Rollout
// regelmäßig hinterher), käme nie eine Antwort; ohne enge Grenze verlöre dann
// JEDER Lauf eine halbe Minute, bevor er überhaupt beginnt.
const skillsRequestTimeout = 10 * time.Second

// requestSkills holt die Skills des Agenten aus der Control Plane.
func (c *Client) requestSkills(ctx context.Context) (InjectSkills, error) {
	req := RequestSkills{RequestID: uuid.NewString()}
	reqCtx, cancel := context.WithTimeout(ctx, skillsRequestTimeout)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestSkills, req.RequestID, req)
	if err != nil {
		return InjectSkills{}, err
	}
	return DecodePayload[InjectSkills](msg)
}

// materializeSkills schreibt die Skills des Agenten nach <home>/.claude/skills/.
//
// Best-effort für den LAUF: Scheitert es, läuft die Aufgabe ohne Skills weiter
// statt gar nicht. Ein fehlender Skill kostet Qualität, ein abgebrochener Lauf
// kostet die Aufgabe.
//
// Nicht best-effort ist dagegen das Aufräumen. Bleibt die Antwort aus (Timeout,
// Transportfehler, eine Control Plane, die request_skills noch nicht kennt),
// wird das Verzeichnis GELEERT statt unangetastet gelassen. Denn das Home
// überlebt den Lauf: Ohne diesen Schritt liefe die Aufgabe nicht „ohne Skills",
// sondern mit den ALTEN — und ein Skill, der gerade entzogen wurde, wirkte
// weiter. „Ich weiß nicht, welche gelten" muss dasselbe bewirken wie „keine";
// die Control Plane hält dieselbe Zusage, indem sie selbst bei DB-Fehlern mit
// einer leeren Liste antwortet (orchestrator.skillsFor).
func (c *Client) materializeSkills(ctx context.Context) {
	dir := filepath.Join(c.homeDir, filepath.FromSlash(skillsDirName))
	res, err := c.requestSkills(ctx)
	if err != nil || !res.OK {
		reason := "unbekannt"
		if err != nil {
			reason = err.Error()
		} else if res.Error != "" {
			reason = res.Error
		}
		c.log.Warn("skills nicht abrufbar — lauf ohne skills", "err", reason)
		if err := clearSkillDirs(dir); err != nil {
			c.log.Warn("alte skills nicht abräumbar", "err", err)
		}
		return
	}
	if err := writeSkillDirs(dir, res.Skills); err != nil {
		c.log.Warn("skills nicht materialisierbar — lauf ohne skills", "err", err)
	}
}

// clearSkillDirs entfernt alles, was Covey früher unter dir abgelegt hat, ohne
// etwas Neues zu schreiben. Ein fehlendes Verzeichnis ist kein Fehler — dann
// gab es nie Skills.
func clearSkillDirs(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

// writeSkillDirs legt die Skill-Verzeichnisse an und entfernt vorher alles, was
// Covey dort früher hingeschrieben hat.
//
// Das Aufräumen ist der Grund, warum hier nicht einfach geschrieben wird: Das
// Home überlebt den Lauf (warm_sandbox, persistentes /home). Ein Skill, der in
// der Control Plane gelöscht oder von einem Agenten abgehängt wurde, bliebe
// sonst für immer wirksam — der Entzug einer Fähigkeit würde nicht greifen.
func writeSkillDirs(dir string, skills []SkillDir) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	keep := make(map[string]bool, len(skills))
	for _, s := range skills {
		if strings.TrimSpace(s.Name) != "" {
			keep[s.Name] = true
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
				return err
			}
		}
	}

	for _, s := range skills {
		name := strings.TrimSpace(s.Name)
		if name == "" || !safeSkillName(name) {
			continue // die Control Plane validiert; hier nur die zweite Tür
		}
		skillDir := filepath.Join(dir, name)
		// Erst weg, dann neu: So verschwinden auch Dateien, die der Skill in
		// einer früheren Fassung hatte und jetzt nicht mehr.
		if err := os.RemoveAll(skillDir); err != nil {
			return err
		}
		if err := os.MkdirAll(skillDir, 0o700); err != nil {
			return err
		}
		for rel, content := range s.Files {
			if !safeSkillPath(rel) {
				continue // Traversal — die Control Plane lässt das nicht zu, wir auch nicht
			}
			if rel == skillEntryFile {
				// Frontmatter entsteht hier, damit Name und Beschreibung nur an
				// einer Stelle gepflegt werden (siehe skills.Render).
				content = renderSkillEntry(name, s.Description, content)
			}
			target := filepath.Join(skillDir, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(target, []byte(content), 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

const skillEntryFile = "SKILL.md"

// safeSkillName/safeSkillPath sind die zweite Tür vor dem Dateisystem. Die
// Control Plane validiert bereits (internal/skills), aber der Daemon schreibt —
// und was schreibt, prüft selbst. Bewusst dupliziert: Das Paket daemon darf
// internal/skills nicht importieren (Schichtentrennung, der Daemon läuft in der
// Sandbox und kennt die Control Plane nicht).
func safeSkillName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 63 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

func safeSkillPath(rel string) bool {
	if rel == "" || strings.ContainsRune(rel, 0) || strings.HasPrefix(rel, "/") ||
		strings.Contains(rel, `\`) || strings.Contains(rel, ":") {
		return false
	}
	cleaned := path.Clean(rel)
	if cleaned != rel || cleaned == "." || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return false
	}
	for _, seg := range strings.Split(cleaned, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

// renderSkillEntry baut die SKILL.md samt Frontmatter. Gleiche Regeln wie
// skills.Render in der Control Plane — insbesondere das Quotieren, weil fast
// jede Beschreibung einen Doppelpunkt enthält und der Block sonst als YAML-Map
// gelesen würde.
func renderSkillEntry(name, description, body string) string {
	var b strings.Builder
	b.WriteString("---\nname: " + skillScalar(name) + "\n")
	b.WriteString("description: " + skillScalar(description) + "\n---\n\n")
	b.WriteString(strings.TrimLeft(body, "\n"))
	if !strings.HasSuffix(b.String(), "\n") {
		b.WriteString("\n")
	}
	return b.String()
}

func skillScalar(v string) string {
	v = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(v, "\r", " "), "\n", " "))
	if v == "" {
		return `""`
	}
	risky := strings.ContainsAny(v, `:#"'{}[]&*!|>%@`+"`") ||
		strings.HasPrefix(v, "-") || strings.HasPrefix(v, " ") || strings.HasSuffix(v, " ")
	if !risky {
		return v
	}
	return `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
}
