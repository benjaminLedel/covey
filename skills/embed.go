// Package skills bettet die mitgelieferten Claude-Code-Skills ins Binary ein,
// damit eine laufende Covey-Instanz sie zum Download anbieten kann — auch für
// Nutzer ohne Git-Zugriff. Quelle der Wahrheit sind die Dateien hier unter
// skills/<name>/; die Kopie unter .claude/skills/<name>/ (für Claude Code im
// Repo selbst) wird von `make build` daraus synchronisiert.
package skills

import "embed"

//go:embed covey-agent
var FS embed.FS
