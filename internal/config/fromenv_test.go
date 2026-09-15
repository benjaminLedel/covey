package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"covey/internal/sandbox"
)

// An empty environment has to produce a usable instance. That is not a
// tautology: FromEnv reads about sixty variables, and a default that silently
// went missing shows up nowhere else — the process starts, and one setting is
// the zero value.
func TestFromEnvWithoutAnyVariable(t *testing.T) {
	clearCoveyEnv(t)
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, got, want string }{
		{"ListenAddr", c.ListenAddr, ":8494"},
		{"PublicURL", c.PublicURL, "http://localhost:8494"},
		{"IdentityProvider", c.IdentityProvider, "builtin"},
		{"SecretStore", c.SecretStore, "builtin"},
		{"SandboxProvider", c.SandboxProvider, "docker"},
		{"DataDir", c.DataDir, "./data"},
		{"HSTS", c.HSTS, "basic"},
		{"BlobStore", c.BlobStore, "builtin"},
		{"BuiltinRunner", c.BuiltinRunner, "auto"},
		{"EgressIsolation", c.EgressIsolation, "proxy"},
		{"EgressProxyAddr", c.EgressProxyAddr, ":8888"},
		{"DreamAt", c.DreamAt, "03:00"},
		{"MarketplaceURL", c.MarketplaceURL, DefaultMarketplaceURL},
		{"EmbeddingProvider", c.EmbeddingProvider, "builtin"},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %q, expected %q", tc.name, tc.got, tc.want)
		}
	}
	if c.TickInterval != 30*time.Second {
		t.Errorf("TickInterval = %v", c.TickInterval)
	}
	if c.SessionTTL != 7*24*time.Hour {
		t.Errorf("SessionTTL = %v", c.SessionTTL)
	}
	if c.DaemonTokenTTL != 15*time.Minute {
		t.Errorf("DaemonTokenTTL = %v", c.DaemonTokenTTL)
	}
	if c.TidyHomeAboveBytes != 5<<30 {
		t.Errorf("TidyHomeAboveBytes = %d, expected 5 GiB", c.TidyHomeAboveBytes)
	}
	if c.TidyHomeAboveEntries != 200 {
		t.Errorf("TidyHomeAboveEntries = %d, expected 200", c.TidyHomeAboveEntries)
	}
	if !c.HomeStore || !c.RequestLog || !c.RequestLogBodies || !c.S3PathStyle {
		t.Error("a boolean that defaults to true came out false")
	}
	if c.EgressEnforce || c.CookieSecure {
		t.Error("a boolean that defaults to false came out true")
	}
	// The default image is derived from the default profile — one value, so
	// that "instance default" and "base profile" cannot drift apart.
	if c.SandboxImage == "" || c.SandboxImage != c.SandboxImages[sandbox.DefaultName()] {
		t.Errorf("SandboxImage = %q, SandboxImages[%q] = %q", c.SandboxImage, sandbox.DefaultName(), c.SandboxImages[sandbox.DefaultName()])
	}
	if len(c.SandboxImageEnv) != 0 {
		t.Errorf("without an environment there are no overrides, got %v", c.SandboxImageEnv)
	}
}

// The documented off-switch has to arrive as one. The orchestrator fills in its
// default for 0, so a 0 passed through kept asking above 5 GB (#273).
func TestTidyThresholdsSwitchOffAtZero(t *testing.T) {
	clearCoveyEnv(t)
	t.Setenv("COVEY_HOME_TIDY_ABOVE_GB", "0")
	t.Setenv("COVEY_HOME_TIDY_ABOVE_ENTRIES", "0")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if c.TidyHomeAboveBytes >= 0 || c.TidyHomeAboveEntries >= 0 {
		t.Errorf("0 must arrive as off (negative), got %d bytes, %d entries",
			c.TidyHomeAboveBytes, c.TidyHomeAboveEntries)
	}
	t.Setenv("COVEY_HOME_TIDY_ABOVE_GB", "20")
	t.Setenv("COVEY_HOME_TIDY_ABOVE_ENTRIES", "500")
	if c, err = FromEnv(); err != nil {
		t.Fatal(err)
	}
	if c.TidyHomeAboveBytes != 20<<30 || c.TidyHomeAboveEntries != 500 {
		t.Errorf("got %d bytes, %d entries", c.TidyHomeAboveBytes, c.TidyHomeAboveEntries)
	}
}

// The secure cookie hangs off the public URL rather than off a second setting
// somebody has to remember.
func TestCookieSecureFollowsTheProtocolAndCanBeOverruled(t *testing.T) {
	clearCoveyEnv(t)
	t.Setenv("COVEY_PUBLIC_URL", "https://covey.example")
	c, err := FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	if !c.CookieSecure {
		t.Error("HTTPS did not switch the secure cookie on")
	}
	t.Setenv("COVEY_COOKIE_SECURE", "false")
	if c, err = FromEnv(); err != nil {
		t.Fatal(err)
	} else if c.CookieSecure {
		t.Error("the explicit setting did not win over the derived one")
	}
}

// Only 'builtin' is implemented. A configured Keycloak that quietly ran on the
// builtin provider would be the worse answer by far.
func TestFromEnvRefusesAnUnimplementedPort(t *testing.T) {
	clearCoveyEnv(t)
	t.Setenv("COVEY_IDENTITY_PROVIDER", "oidc")
	if _, err := FromEnv(); err == nil {
		t.Error("an unimplemented identity provider was accepted")
	}
	t.Setenv("COVEY_IDENTITY_PROVIDER", "builtin")
	t.Setenv("COVEY_SECRET_STORE", "vault")
	if _, err := FromEnv(); err == nil {
		t.Error("an unimplemented secret store was accepted")
	}
}

func TestFromEnvCarriesAMalformedProxyListOut(t *testing.T) {
	clearCoveyEnv(t)
	t.Setenv("COVEY_TRUSTED_PROXIES", "nicht-einmal-eine-adresse")
	if _, err := FromEnv(); err == nil {
		t.Error("a malformed proxy list was swallowed")
	}
}

func TestGetenvBool(t *testing.T) {
	for _, tc := range []struct {
		raw      string
		fallback bool
		want     bool
	}{
		{"1", false, true}, {"true", false, true}, {"YES", false, true}, {" on ", false, true},
		{"0", true, false}, {"false", true, false}, {"No", true, false}, {"off", true, false},
		// Unreadable counts as unset: a half-understood setting would be a
		// silently different one.
		{"vielleicht", true, true}, {"vielleicht", false, false},
		{"", true, true}, {"  ", false, false},
	} {
		t.Setenv("COVEY_TEST_BOOL", tc.raw)
		if got := getenvBool("COVEY_TEST_BOOL", tc.fallback); got != tc.want {
			t.Errorf("getenvBool(%q, %v) = %v, expected %v", tc.raw, tc.fallback, got, tc.want)
		}
	}
}

func TestGetenvInt(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want int
	}{{"7", 7}, {" 12 ", 12}, {"-3", -3}, {"5g", 5 /*fallback*/}, {"", 5}, {"  ", 5}} {
		t.Setenv("COVEY_TEST_INT", tc.raw)
		if got := getenvInt("COVEY_TEST_INT", 5); got != tc.want {
			t.Errorf("getenvInt(%q) = %d, expected %d", tc.raw, got, tc.want)
		}
	}
}

// A bare number is read as seconds — the form somebody reaches for who has not
// read the documentation.
func TestGetenvDuration(t *testing.T) {
	const fallback = 30 * time.Second
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{"90s", 90 * time.Second},
		{"2m", 2 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"45", 45 * time.Second},
		{"bald", fallback},
		{"", fallback},
	} {
		t.Setenv("COVEY_TEST_DUR", tc.raw)
		if got := getenvDuration("COVEY_TEST_DUR", fallback); got != tc.want {
			t.Errorf("getenvDuration(%q) = %v, expected %v", tc.raw, got, tc.want)
		}
	}
}

func TestGetenvFallsBackOnAnEmptyValue(t *testing.T) {
	t.Setenv("COVEY_TEST_STR", "")
	if got := getenv("COVEY_TEST_STR", "voreinstellung"); got != "voreinstellung" {
		t.Errorf("getenv = %q", got)
	}
	t.Setenv("COVEY_TEST_STR", "gesetzt")
	if got := getenv("COVEY_TEST_STR", "voreinstellung"); got != "gesetzt" {
		t.Errorf("getenv = %q", got)
	}
}

// A new target-system plugin needs no new config code: its secret is picked up
// by its name.
func TestWebhookSecretsFromEnv(t *testing.T) {
	clearCoveyEnv(t)
	t.Setenv("COVEY_ZAMMAD_WEBHOOK_SECRET", "s1")
	t.Setenv("COVEY_GITLAB_WEBHOOK_SECRET", "s2")
	t.Setenv("COVEY_WEBHOOK_SECRET", "ohne-system") // no system name
	t.Setenv("COVEY_EMPTY_WEBHOOK_SECRET", "")      // set but empty
	t.Setenv("OTHER_THING_WEBHOOK_SECRET", "fremd") // not ours
	t.Setenv("COVEY_ZAMMAD_WEBHOOK_TOKEN", "kein-secret")

	got := webhookSecretsFromEnv()
	want := map[string]string{"zammad": "s1", "gitlab": "s2"}
	if len(got) != len(want) {
		t.Fatalf("got %v, expected %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %q, expected %q", k, got[k], v)
		}
	}
}

// COVEY_SANDBOX_IMAGE is the name from before the profiles existed. It stays
// valid for the default profile — an upgrade must not silently drop an image
// somebody configured.
func TestSandboxImageEnvKeepsTheOldName(t *testing.T) {
	clearCoveyEnv(t)
	t.Setenv("COVEY_SANDBOX_IMAGE", "eigenes:bild")
	if got := sandboxImageEnv()[sandbox.DefaultName()]; got != "eigenes:bild" {
		t.Errorf("default profile = %q, expected the old variable's value", got)
	}

	// The profile-specific variable wins over the old name.
	t.Setenv(sandbox.EnvVar(sandbox.DefaultName()), "neues:bild")
	if got := sandboxImageEnv()[sandbox.DefaultName()]; got != "neues:bild" {
		t.Errorf("default profile = %q, expected the profile variable to win", got)
	}
}

func TestSandboxImageEnvReadsEveryProfile(t *testing.T) {
	clearCoveyEnv(t)
	profiles := sandbox.All()
	if len(profiles) < 2 {
		t.Skip("the catalogue carries fewer than two profiles")
	}
	for _, p := range profiles {
		t.Setenv(sandbox.EnvVar(p.Name), "bild-"+p.Name)
	}
	got := sandboxImageEnv()
	for _, p := range profiles {
		if got[p.Name] != "bild-"+p.Name {
			t.Errorf("profile %q = %q, expected %q", p.Name, got[p.Name], "bild-"+p.Name)
		}
	}
}

func TestSplitList(t *testing.T) {
	if got := splitList(""); got != nil {
		t.Errorf("empty list = %v, expected nil", got)
	}
	if got := splitList(" , ,, "); got != nil {
		t.Errorf("list of separators = %v, expected nil", got)
	}
	got := splitList(" a , b,,c ")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, expected %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, expected %q", i, got[i], want[i])
		}
	}
}

// clearCoveyEnv unsets every COVEY_ variable for the duration of the test, so
// that a variable in the developer's shell cannot decide the outcome.
func clearCoveyEnv(t *testing.T) {
	t.Helper()
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "COVEY_") {
			t.Setenv(k, "")
		}
	}
}
