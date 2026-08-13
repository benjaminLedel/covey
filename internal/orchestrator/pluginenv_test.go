package orchestrator

import "testing"

// TestPluginEnvCarriesIntakeScope pins the defect this replaces: the intake
// allowlists were set on the control plane but read by plugins running in the
// sandbox, where an absent value means "no restriction" rather than a stricter
// default. The scope therefore inverted into the widest setting instead of
// failing closed.
func TestPluginEnvCarriesIntakeScope(t *testing.T) {
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "group/a, 42")
	t.Setenv("COVEY_ZAMMAD_INTAKE_GROUPS", "Support")

	env := pluginEnv()
	if env["COVEY_GITLAB_INTAKE_PROJECTS"] != "group/a, 42" {
		t.Errorf("the GitLab intake scope has to reach the sandbox, got %q",
			env["COVEY_GITLAB_INTAKE_PROJECTS"])
	}
	if env["COVEY_ZAMMAD_INTAKE_GROUPS"] != "Support" {
		t.Error("every intake allowlist travels, not just GitLab's")
	}
}

// TestPluginEnvLeavesUnsetAbsent: an empty string is a meaningful value for the
// allowlists ("no restriction"), so an unset variable must not be written in as
// one — that would turn "inherit the default" into an explicit widest setting.
func TestPluginEnvLeavesUnsetAbsent(t *testing.T) {
	t.Setenv("COVEY_GITLAB_INTAKE_PROJECTS", "group/a")
	if _, ok := pluginEnv()["COVEY_GITHUB_INTAKE_REPOS"]; ok {
		t.Error("an unset variable must stay absent rather than arrive empty")
	}
}

// TestPluginEnvNeverCarriesConnectionOrImageVars: the daemon token, WS URL and
// agent id are assigned by the caller AFTER this map and must not be
// pass-through candidates; the browser paths belong to the sandbox image.
func TestPluginEnvNeverCarriesConnectionOrImageVars(t *testing.T) {
	t.Setenv("COVEY_WS_URL", "ws://attacker/ws")
	t.Setenv("COVEY_DAEMON_TOKEN", "forged")
	t.Setenv("COVEY_AGENT_ID", "00000000-0000-0000-0000-000000000000")
	for _, k := range pluginEnvExcluded {
		t.Setenv(k, "host-specific")
	}

	env := pluginEnv()
	for _, k := range []string{"COVEY_WS_URL", "COVEY_DAEMON_TOKEN", "COVEY_AGENT_ID"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s must not be a pass-through — the caller owns it", k)
		}
	}
	for _, k := range pluginEnvExcluded {
		if _, ok := env[k]; ok {
			t.Errorf("%s belongs to the sandbox image, not to the control plane's env", k)
		}
	}
}

// TestPluginEnvPassthroughAndExcludedAreDisjoint: a variable in both lists would
// make the intent unreadable and the outcome depend on evaluation order.
func TestPluginEnvPassthroughAndExcludedAreDisjoint(t *testing.T) {
	excluded := map[string]bool{}
	for _, k := range pluginEnvExcluded {
		excluded[k] = true
	}
	for _, k := range pluginEnvPassthrough {
		if excluded[k] {
			t.Errorf("%s is both passed through and excluded", k)
		}
	}
}

// TestPluginEnvCarriesTheSizeLimits: the attachment ceilings travel too.
//
// They were missing from the first version of the list, and the omission is
// instructive rather than embarrassing: an intake allowlist that does not
// arrive fails loudly enough to be noticed (agents work on the wrong project),
// while a size limit that does not arrive just falls back to its built-in
// default. Nobody sees 25 MB and thinks "that was supposed to be 5".
func TestPluginEnvCarriesTheSizeLimits(t *testing.T) {
	for _, key := range []string{"COVEY_EMAIL_ATTACHMENT_MAX_MB", "COVEY_TEAMS_ATTACHMENT_MAX_MB"} {
		t.Setenv(key, "5")
	}
	env := pluginEnv()
	for _, key := range []string{"COVEY_EMAIL_ATTACHMENT_MAX_MB", "COVEY_TEAMS_ATTACHMENT_MAX_MB"} {
		if env[key] != "5" {
			t.Errorf("%s = %q, want 5 — a configured limit has to reach the sandbox, "+
				"or it silently stays at the plugin's default", key, env[key])
		}
	}
}
