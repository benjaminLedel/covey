package gitlab

import (
	"os"
	"strconv"
	"strings"
)

// Operational configuration of the GitLab plugin from ENV (12-factor, as with
// the Zammad plugin). Everything has safe defaults — an unset field restricts
// nothing and keeps the standard behaviour.

// intakeProjects returns the allowlist of GitLab projects whose issues may
// trigger a task at all. Entries are project paths (path_with_namespace) or
// numeric project ids. Format:
//
//	COVEY_GITLAB_INTAKE_PROJECTS="group/support, 42"
//
// Empty/unset → no restriction (all projects). Comparison is
// case-insensitive, leading/trailing whitespace is ignored.
func intakeProjects() map[string]bool {
	return parseSet(os.Getenv("COVEY_GITLAB_INTAKE_PROJECTS"))
}

// projectInScope checks a project (numeric id + path) against the allowlist
// from COVEY_GITLAB_INTAKE_PROJECTS. An empty allowlist → everything is in
// scope. Used by the discovery actions (list_projects, list_issues) and by the
// nur-wenn: pre-check (HasWork) — GitLab takes up work purely by polling.
func projectInScope(projectID int, path string) bool {
	projects := intakeProjects()
	if len(projects) == 0 {
		return true
	}
	return projects[strings.ToLower(strings.TrimSpace(path))] || projects[strconv.Itoa(projectID)]
}

// parseSet splits a comma-separated ENV list into a set of lower-cased,
// trimmed values. Empty entries are dropped.
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
