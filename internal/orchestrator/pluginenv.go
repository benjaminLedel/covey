package orchestrator

import (
	"os"
	"strings"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Operational plugin configuration lives in the control plane's environment
// (12-factor), but the target plugins that read it run in the SANDBOX — the
// action proxy executes them in coveyd, not here. An unset variable there is
// not a stricter default: the intake allowlists read "empty = no restriction",
// and a size limit falls back to its own built-in default. So a variable that
// never reaches the sandbox does not merely lose effect; for an allowlist it
// inverts into the widest possible setting, silently.
//
// That is how COVEY_GITLAB_INTAKE_PROJECTS came to be set on the control plane
// and enforced only in the wake-up gate (HasWork, which does run here) while
// list_issues/list_projects in the sandbox saw every project the shared token
// could reach — observed in this installation as agents of one product
// triaging the backlog of an unrelated one.
//
// WHICH variables travel is not decided here. Each plugin declares what it
// reads (target.Descriptor.Env) and this collects the declarations from the
// registry. A hand-maintained list in covey was the first fix, and it was
// wrong in a way that would have kept costing: it described code living in
// another repository, it had already missed two entries when it was written,
// and it could never have covered a plugin somebody else wrote.

// pluginEnvExcluded documents what deliberately does NOT travel even when a
// plugin declares it:
//
//   - COVEY_BROWSER_CHROME_PATH — the path is a property of the sandbox image
//     (Dockerfile.sandbox sets it to /usr/bin/chromium). The control plane's
//     value, if it had one, would point into a different filesystem.
//   - COVEY_BROWSER_HEADFUL — a headful browser needs a display the sandbox
//     container does not have.
//
// Both stay with the image on purpose, and the exclusion wins over any
// declaration.
var pluginEnvExcluded = map[string]bool{
	"COVEY_BROWSER_CHROME_PATH": true,
	"COVEY_BROWSER_HEADFUL":     true,
}

// pluginEnvShared are the variables the SDK itself reads on a plugin's behalf,
// so they belong to no single plugin's namespace and cannot be declared by one.
// Kept short on purpose: every entry here is a small piece of the old problem
// coming back.
var pluginEnvShared = []string{
	// Read by target.PruneOldCheckouts, for any plugin that checks out a repo.
	"COVEY_CHECKOUT_KEEP",
}

// pluginEnv collects what the registered plugins declared, plus the SDK's own
// shared variables.
//
// Two rules make it safe to honour a declaration written by somebody else:
//
// A plugin may only name variables in its OWN namespace (COVEY_<NAME>_…).
// Fail-closed — an entry outside it is dropped rather than trusted, so no
// plugin, however careless or hostile, can have COVEY_MASTER_KEY or
// COVEY_DATABASE_URL carried into a sandbox by declaring it.
//
// And absent stays absent. An empty string is a meaningful value for several
// of these ("no restriction"), so writing one in would change behaviour rather
// than preserve it.
func pluginEnv() map[string]string {
	out := map[string]string{}
	add := func(key string) {
		if pluginEnvExcluded[key] {
			return
		}
		if v, ok := os.LookupEnv(key); ok {
			out[key] = v
		}
	}
	for _, d := range target.All() {
		prefix := pluginEnvPrefix(d.Name)
		for _, key := range d.Env {
			if !strings.HasPrefix(key, prefix) {
				continue // outside the plugin's namespace — see above
			}
			add(key)
		}
	}
	for _, key := range pluginEnvShared {
		add(key)
	}
	return out
}

// pluginEnvPrefix is the namespace a plugin may declare in: "gitlab" →
// "COVEY_GITLAB_", "my-tracker" → "COVEY_MY_TRACKER_".
func pluginEnvPrefix(name string) string {
	return "COVEY_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_")) + "_"
}
