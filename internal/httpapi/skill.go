package httpapi

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	// Alias, weil in diesem Paket auch covey/internal/skills vorkommt (die
	// Fähigkeiten der Agenten, siehe agentskills.go) — das hier sind die
	// mitgelieferten Claude-Code-Skills für Menschen.
	embedskills "covey/skills"
)

// handleDownloadSkill liefert einen mitgelieferten Claude-Code-Skill als ZIP —
// für Nutzer, die nicht auf das Git-Repo zugreifen können. Das Archiv enthält
// den Ordner covey-agent/ (SKILL.md + reference.md); entpackt nach
// ~/.claude/skills/ ist der Skill in Claude Code sofort nutzbar.
//
// Damit der geladene Skill direkt gegen DIESE Instanz arbeitet, wird die
// Basis-URL der Instanz oben in die SKILL.md eingespritzt (kein manuelles
// COVEY_URL nötig). Der Server-Binary selbst wird NICHT mitgeliefert — nur die
// Skill-Anleitung samt Ziel-URL.
func (s *Server) handleDownloadSkill(w http.ResponseWriter, r *http.Request) {
	const skillDir = "covey-agent"

	// Dieselbe Ableitung wie für Website und Webhook-URLs (seo.go): Der Skill
	// wird auf einem fremden Rechner ausgeführt und muss die Instanz von außen
	// erreichen — nicht unter der Adresse, über die Sandboxen zurückverbinden.
	base := s.origin(r)
	header := fmt.Sprintf("> **Diese Covey-Instanz:** `COVEY_URL=%s`\n"+
		"> Dieser Skill wurde von obiger Instanz geladen — nutze diese URL als Standard-Ziel\n"+
		"> für „live anlegen\"/Update (Workflow D). Auth (Token/Session) erfragst du beim Nutzer.\n\n",
		base)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="covey-agent-skill.zip"`)

	zw := zip.NewWriter(w)
	defer zw.Close()

	err := fs.WalkDir(embedskills.FS, skillDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := embedskills.FS.ReadFile(path)
		if err != nil {
			return err
		}
		// Instanz-URL in die SKILL.md voranstellen (nach der Frontmatter).
		if strings.HasSuffix(path, "SKILL.md") {
			data = injectInstanceURL(data, header)
		}
		f, err := zw.Create(path) // path beginnt mit "covey-agent/"
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	})
	if err != nil && s.Log != nil {
		// Header sind evtl. schon raus — best effort, Fehler still ins Log.
		s.Log.Warn("skill-download fehlgeschlagen", "err", err)
	}
}

// injectInstanceURL setzt den Instanz-Hinweis hinter die YAML-Frontmatter der
// SKILL.md (der zweite `---`-Marker), sonst an den Anfang.
func injectInstanceURL(md []byte, header string) []byte {
	s := string(md)
	if strings.HasPrefix(s, "---\n") {
		if i := strings.Index(s[4:], "\n---\n"); i >= 0 {
			cut := 4 + i + len("\n---\n")
			return []byte(s[:cut] + "\n" + header + s[cut:])
		}
	}
	return []byte(header + s)
}
