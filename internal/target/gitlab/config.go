package gitlab

import (
	"os"
	"strconv"
	"strings"
)

// Betriebs-Konfiguration des GitLab-Plugins aus ENV (12-Factor, wie beim
// Zammad-Plugin). Alles hat sichere Defaults — ein nicht gesetztes Feld
// schränkt nichts ein bzw. behält das Standardverhalten bei.

// intakeProjects liefert die Allowlist der GitLab-Projekte, aus denen Issues
// überhaupt eine Aufgabe auslösen dürfen. Einträge sind Projektpfade
// (path_with_namespace) oder numerische Projekt-ids. Format:
//
//	COVEY_GITLAB_INTAKE_PROJECTS="gruppe/support, 42"
//
// Leer/ungesetzt → keine Einschränkung (alle Projekte). Vergleich
// case-insensitiv, führende/abschließende Leerzeichen werden ignoriert.
func intakeProjects() map[string]bool {
	return parseSet(os.Getenv("COVEY_GITLAB_INTAKE_PROJECTS"))
}

// projectInScope prüft ein Projekt (numerische id + Pfad) gegen die Allowlist
// aus COVEY_GITLAB_INTAKE_PROJECTS. Leere Allowlist → alles im Scope. Genutzt
// von den Discovery-Aktionen (list_projects, list_issues) und vom
// nur-wenn:-Vorabcheck (HasWork) — GitLab nimmt Arbeit rein per Polling auf.
func projectInScope(projectID int, path string) bool {
	projects := intakeProjects()
	if len(projects) == 0 {
		return true
	}
	return projects[strings.ToLower(strings.TrimSpace(path))] || projects[strconv.Itoa(projectID)]
}

// parseSet zerlegt eine kommaseparierte ENV-Liste in ein Set kleingeschriebener,
// getrimmter Werte. Leere Einträge werden verworfen.
func parseSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.ToLower(strings.TrimSpace(part))
		if v != "" {
			out[v] = true
		}
	}
	return out
}
