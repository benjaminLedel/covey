package orchestrator

import "os"

// Operational plugin configuration lives in the control plane's environment
// (12-factor), but the target plugins that read it run in the SANDBOX — the
// action proxy executes them in coveyd, not here. An unset variable there is
// not a stricter default: the intake allowlists read "empty = no restriction"
// and the size limits fall back to their built-in maximum. So a variable that
// never reaches the sandbox does not merely lose effect, it inverts into the
// widest possible setting, silently.
//
// That is how COVEY_GITLAB_INTAKE_PROJECTS came to be set on the control plane
// and enforced only in the wake-up gate (HasWork, which does run here) while
// list_issues/list_projects in the sandbox saw every project the shared token
// could reach — observed in this installation as agents of one product
// triaging and commenting on the backlog of an unrelated one.
//
// pluginEnvPassthrough is therefore an explicit allowlist rather than "copy
// everything COVEY_*": the connection variables below must not be overwritable
// from the outside, and a few settings belong to the image rather than to
// policy (see pluginEnvExcluded).
var pluginEnvPassthrough = []string{
	// Intake scope — which items may reach an agent at all.
	"COVEY_GITLAB_INTAKE_PROJECTS",
	"COVEY_GITHUB_INTAKE_REPOS",
	"COVEY_ZAMMAD_INTAKE_GROUPS",
	"COVEY_EMAIL_INTAKE_ADDRESSES",
	"COVEY_TEAMS_INTAKE_TENANTS",
	// Outbound scope — where an agent may write to.
	"COVEY_EMAIL_SEND_DOMAINS",
	"COVEY_ZAMMAD_REPLY_TYPE",
	// Identity of the shared bot account, for the plugins' own author checks.
	"COVEY_GITHUB_BOT_LOGINS",
	// Resource ceilings. Unset means "the built-in maximum", so these have to
	// travel too for a configured limit to hold where the work happens.
	"COVEY_GITLAB_CHECKOUT_MAX_MB",
	"COVEY_GITHUB_CHECKOUT_MAX_MB",
	"COVEY_NEXTCLOUD_UPLOAD_MAX_MB",
	"COVEY_SHAREPOINT_UPLOAD_MAX_MB",
	"COVEY_CHECKOUT_KEEP",
	"COVEY_BROWSER_TIMEOUT_SECS",
	// Endpoints the control plane owns.
	"COVEY_TEAMS_TOKEN_URL",
	"COVEY_IOS_BRIDGE_URL",
}

// pluginEnvExcluded documents what deliberately does NOT travel, so the next
// reader does not "complete" the list above:
//
//   - COVEY_BROWSER_CHROME_PATH — the path is a property of the sandbox image
//     (Dockerfile.sandbox sets it to /usr/bin/chromium). The control plane's
//     value, if it had one, would point into a different filesystem.
//   - COVEY_BROWSER_HEADFUL — a headful browser needs a display the sandbox
//     container does not have.
//
// Both stay with the image on purpose.
var pluginEnvExcluded = []string{
	"COVEY_BROWSER_CHROME_PATH",
	"COVEY_BROWSER_HEADFUL",
}

// pluginEnv collects the pass-through variables that are actually set here.
// Absent stays absent — an empty string is a meaningful value for some of
// these ("no restriction"), so writing one in would change behaviour rather
// than preserve it.
func pluginEnv() map[string]string {
	out := make(map[string]string, len(pluginEnvPassthrough))
	for _, k := range pluginEnvPassthrough {
		if v, ok := os.LookupEnv(k); ok {
			out[k] = v
		}
	}
	return out
}
