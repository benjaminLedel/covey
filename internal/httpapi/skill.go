package httpapi

import (
	"archive/zip"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	// Aliased because covey/internal/skills also appears in this package (the
	// agents' capabilities, see agentskills.go) — these here are the bundled
	// Claude Code skills for humans.
	embedskills "covey/skills"
)

// handleDownloadSkill serves a bundled Claude Code skill as a ZIP — for users
// who cannot reach the git repo. The archive contains the folder covey-agent/
// (SKILL.md + reference.md); unpacked into ~/.claude/skills/ the skill is
// immediately usable in Claude Code.
//
// So that the downloaded skill works against THIS instance directly, the
// instance's base URL is injected at the top of SKILL.md (no manual COVEY_URL
// needed). The server binary itself is NOT shipped — only the skill
// instructions plus the target URL.
func (s *Server) handleDownloadSkill(w http.ResponseWriter, r *http.Request) {
	const skillDir = "covey-agent"

	// The same derivation as for website and webhook URLs (seo.go): the skill
	// runs on someone else's machine and has to reach the instance from the
	// outside — not under the address sandboxes connect back through.
	base := s.origin(r)
	header := fmt.Sprintf("> **This Covey instance:** `COVEY_URL=%s`\n"+
		"> This skill was downloaded from the instance above — use this URL as the default target\n"+
		"> for \"creating it live\"/update (Workflow D). Ask the user for auth (token/session).\n\n",
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
		// Prepend the instance URL to SKILL.md (after the frontmatter).
		if strings.HasSuffix(path, "SKILL.md") {
			data = injectInstanceURL(data, header)
		}
		f, err := zw.Create(path) // path starts with "covey-agent/"
		if err != nil {
			return err
		}
		_, err = f.Write(data)
		return err
	})
	if err != nil && s.Log != nil {
		// Headers may already be out — best effort, log the error quietly.
		s.Log.Warn("skill download failed", "err", err)
	}
}

// injectInstanceURL places the instance note behind the YAML frontmatter of
// SKILL.md (the second `---` marker), otherwise at the very beginning.
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
