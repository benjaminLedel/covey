package github

import (
	"os"
	"strings"
)

// Operational configuration of the GitHub plugin from ENV (12-factor, as with
// the GitLab and Zammad plugins). Everything has safe defaults — an unset field
// restricts nothing and keeps the standard behaviour.

// intakeRepos returns the allowlist of repositories whose issues may trigger a
// task at all. Entries are full names (owner/name) or a whole owner (owner/*).
// Format:
//
//	COVEY_GITHUB_INTAKE_REPOS="acme/support, acme/*"
//
// Empty/unset → no restriction (all repositories). Comparison is
// case-insensitive, leading/trailing whitespace is ignored.
func intakeRepos() map[string]bool {
	return parseSet(os.Getenv("COVEY_GITHUB_INTAKE_REPOS"))
}

// repoInScope checks a repository (owner/name) against the allowlist from
// COVEY_GITHUB_INTAKE_REPOS. An empty allowlist → everything is in scope. Used
// by the discovery actions (list_repos, list_issues), by the webhook intake and
// by the nur-wenn: pre-check (HasWork).
func repoInScope(full string) bool {
	repos := intakeRepos()
	if len(repos) == 0 {
		return true
	}
	full = strings.ToLower(strings.TrimSpace(full))
	if repos[full] {
		return true
	}
	// owner/* covers every repository of one owner — the usual case when a
	// whole organisation belongs to the scope.
	if owner, _, ok := strings.Cut(full, "/"); ok {
		return repos[owner+"/*"]
	}
	return false
}

// botLogins are accounts whose contributions do NOT count as somebody else's
// message — one's own bot, that is. The webhook intake does not know the
// brokered identity (it runs without credentials in the Control Plane), so it
// is named here:
//
//	COVEY_GITHUB_BOT_LOGINS="covey-bot"
//
// Without it only the detection via sender.type == "Bot" (GitHub Apps) applies.
// An account with a personal access token is an ordinary user to GitHub —
// without this entry its own reply would wake the agent afresh.
func botLogins() map[string]bool {
	return parseSet(os.Getenv("COVEY_GITHUB_BOT_LOGINS"))
}

// parseSet splits a comma-separated ENV list into a set of lower-cased, trimmed
// values. Empty entries are dropped.
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
