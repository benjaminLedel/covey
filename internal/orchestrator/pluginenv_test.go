package orchestrator

import (
	"os"
	"strings"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

func registerFake(t *testing.T, name string, env []string) {
	t.Helper()
	target.Register(target.Descriptor{Name: name, Label: name, Env: env})
}

func TestPluginEnvCarriesWhatAPluginDeclares(t *testing.T) {
	registerFake(t, "tracker", []string{"COVEY_TRACKER_INTAKE_PROJECTS", "COVEY_TRACKER_MAX_MB"})
	t.Setenv("COVEY_TRACKER_INTAKE_PROJECTS", "group/product")
	t.Setenv("COVEY_TRACKER_MAX_MB", "5")

	env := pluginEnv()
	if env["COVEY_TRACKER_INTAKE_PROJECTS"] != "group/product" {
		t.Errorf("intake scope = %q — a declared variable has to reach the sandbox",
			env["COVEY_TRACKER_INTAKE_PROJECTS"])
	}
	if env["COVEY_TRACKER_MAX_MB"] != "5" {
		t.Errorf("limit = %q, want 5", env["COVEY_TRACKER_MAX_MB"])
	}
}

func TestPluginEnvLeavesUnsetAbsent(t *testing.T) {
	registerFake(t, "quiet", []string{"COVEY_QUIET_INTAKE"})
	os.Unsetenv("COVEY_QUIET_INTAKE")
	if _, ok := pluginEnv()["COVEY_QUIET_INTAKE"]; ok {
		t.Error("an unset variable must stay absent — an empty string means " +
			"'no restriction' to several of these, which is not the same thing")
	}
}

// TestPluginEnvRefusesForeignNamespaces is the rule that makes it safe to
// honour a declaration from a plugin somebody else wrote.
func TestPluginEnvRefusesForeignNamespaces(t *testing.T) {
	registerFake(t, "greedy", []string{
		"COVEY_GREEDY_OK",              // its own — travels
		"COVEY_MASTER_KEY",             // the platform's — must not
		"COVEY_DATABASE_URL",           //
		"COVEY_DAEMON_TOKEN",           // the sandbox's own connection
		"COVEY_GITLAB_INTAKE_PROJECTS", // another plugin's policy
		"PATH",                         // not even ours
	})
	for _, k := range []string{"COVEY_GREEDY_OK", "COVEY_MASTER_KEY", "COVEY_DATABASE_URL",
		"COVEY_DAEMON_TOKEN", "COVEY_GITLAB_INTAKE_PROJECTS", "PATH"} {
		t.Setenv(k, "set")
	}

	env := pluginEnv()
	if env["COVEY_GREEDY_OK"] != "set" {
		t.Error("a plugin's own variable has to travel")
	}
	for _, k := range []string{"COVEY_MASTER_KEY", "COVEY_DATABASE_URL", "COVEY_DAEMON_TOKEN", "PATH"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s left the control plane because a plugin asked for it", k)
		}
	}
	// Another plugin's namespace: only the plugin that owns it may declare it.
	// (gitlab itself is not registered in this test binary, so nothing carries it.)
	if _, ok := env["COVEY_GITLAB_INTAKE_PROJECTS"]; ok {
		t.Error("a plugin declared another plugin's variable and got it")
	}
}

// TestPluginEnvExclusionBeatsDeclaration: the image owns two of the browser's
// variables, and a declaration must not take them back.
func TestPluginEnvExclusionBeatsDeclaration(t *testing.T) {
	registerFake(t, "browser", []string{"COVEY_BROWSER_CHROME_PATH", "COVEY_BROWSER_TIMEOUT_SECS"})
	t.Setenv("COVEY_BROWSER_CHROME_PATH", "/opt/homebrew/bin/chromium")
	t.Setenv("COVEY_BROWSER_TIMEOUT_SECS", "90")

	env := pluginEnv()
	if _, ok := env["COVEY_BROWSER_CHROME_PATH"]; ok {
		t.Error("the chrome path belongs to the image — the control plane's value " +
			"points into a different filesystem")
	}
	if env["COVEY_BROWSER_TIMEOUT_SECS"] != "90" {
		t.Error("the timeout is policy and has to travel")
	}
}

func TestPluginEnvCarriesTheSDKsSharedVariables(t *testing.T) {
	// COVEY_CHECKOUT_KEEP is read by the SDK for any plugin that checks out a
	// repository, so it belongs to no single namespace and cannot be declared.
	t.Setenv("COVEY_CHECKOUT_KEEP", "3")
	if pluginEnv()["COVEY_CHECKOUT_KEEP"] != "3" {
		t.Error("a shared SDK variable has to travel too")
	}
}

func TestPluginEnvPrefix(t *testing.T) {
	for name, want := range map[string]string{
		"gitlab":     "COVEY_GITLAB_",
		"my-tracker": "COVEY_MY_TRACKER_",
		"k8s":        "COVEY_K8S_",
	} {
		if got := pluginEnvPrefix(name); got != want {
			t.Errorf("prefix(%q) = %q, want %q", name, got, want)
		}
	}
}

// TestPluginEnvNeverCarriesConnectionVars pins the other half of the
// arrangement: the daemon's own variables are assigned after the pass-through
// when the sandbox starts, so nothing here can shadow them. This checks the
// collector never produces them in the first place.
func TestPluginEnvNeverCarriesConnectionVars(t *testing.T) {
	for _, k := range []string{"COVEY_WS_URL", "COVEY_DAEMON_TOKEN", "COVEY_AGENT_ID"} {
		t.Setenv(k, "set")
	}
	env := pluginEnv()
	for _, k := range []string{"COVEY_WS_URL", "COVEY_DAEMON_TOKEN", "COVEY_AGENT_ID"} {
		if _, ok := env[k]; ok {
			t.Errorf("%s must never come from the pass-through", k)
		}
	}
	if strings.Contains(strings.Join(pluginEnvShared, ","), "TOKEN") {
		t.Error("a token has no business in the shared list")
	}
}
