package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/runner"
)

// The egress keys survive the round trip through register's file and run's
// reading of it — a key one side writes and the other ignores is a host that
// believes it is isolated and is not.
func TestEgressConfigRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	want := config{URL: "https://covey.example", Token: "t", WorkDir: "/w", Egress: egressSettings{
		Isolation:  "network",
		Allow:      []string{"helpdesk.example.org", "*.internal.example.org"},
		ListenAddr: ":8495",
		ProxyImage: "registry.example.org/covey-egress:1",
	}}
	if err := writeConfig(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Egress.Disabled || got.Egress.mode() != "network" || got.Egress.ListenAddr != ":8495" ||
		got.Egress.ProxyImage != want.Egress.ProxyImage ||
		strings.Join(got.Egress.Allow, ",") != strings.Join(want.Egress.Allow, ",") {
		t.Errorf("egress did not survive: %+v", got.Egress)
	}

	want.Egress = egressSettings{Disabled: true}
	if err := writeConfig(path, want); err != nil {
		t.Fatal(err)
	}
	if got, _ := readConfig(path); !got.Egress.Disabled {
		t.Error("egress_enforce = false did not survive")
	}
}

// A config written before #707 has no egress keys. It must come out enforced,
// not open — that it came out open is the finding.
func TestReadConfigEnforcesEgressByDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte("url = \"https://covey.example\"\ntoken = \"t\"\n"), 0o600)
	cfg, err := readConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Egress.Disabled || cfg.Egress.mode() != "proxy" {
		t.Errorf("a config without egress keys: %+v", cfg.Egress)
	}
}

// A typo must not quietly become the open or the weaker mode.
func TestEgressSettingsRefuseTypos(t *testing.T) {
	for _, line := range []string{
		`egress_isolation = "netwrok"`,
		`egress_isolation = "none"`,
		`egress_enforce = "ja"`,
	} {
		path := filepath.Join(t.TempDir(), "config.toml")
		os.WriteFile(path, []byte(line+"\n"), 0o600)
		if _, err := readConfig(path); err == nil {
			t.Errorf("%s was accepted", line)
		}
	}
	eg := defaultEgressSettings()
	if err := applyEgressEnv(&eg, func(k string) string {
		if k == "COVEY_EGRESS_ISOLATION" {
			return "hard"
		}
		return ""
	}); err == nil || !strings.Contains(err.Error(), "COVEY_EGRESS_ISOLATION") {
		t.Errorf("an unknown COVEY_EGRESS_ISOLATION was accepted or not named: %v", err)
	}
}

// The control plane's variable names override the file, so an operator sets
// the runner the way they set `covey serve`.
func TestEgressEnvOverridesTheFile(t *testing.T) {
	eg := egressSettings{Isolation: "proxy"}
	env := map[string]string{
		"COVEY_EGRESS_ISOLATION":   "network",
		"COVEY_EGRESS_ALLOW":       "a.example.org, b.example.org",
		"COVEY_EGRESS_PROXY_IMAGE": "registry.example.org/covey-egress:2",
	}
	if err := applyEgressEnv(&eg, func(k string) string { return env[k] }); err != nil {
		t.Fatal(err)
	}
	if eg.mode() != "network" || strings.Join(eg.Allow, ",") != "a.example.org,b.example.org" ||
		eg.ProxyImage != "registry.example.org/covey-egress:2" {
		t.Errorf("env did not override: %+v", eg)
	}
	if err := applyEgressEnv(&eg, func(k string) string {
		if k == "COVEY_EGRESS_ENFORCE" {
			return "false"
		}
		return ""
	}); err != nil || !eg.Disabled {
		t.Errorf("COVEY_EGRESS_ENFORCE=false: %+v, %v", eg, err)
	}
}

// What the remote runner hands its docker provider is what the built-in runner
// hands it: the hard mode with this runner's control-plane address and token,
// or the cooperative proxy in this process. Before #707 it handed it nothing.
func TestWireEgress(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := config{URL: "https://covey.example", Token: "runner-token"}

	t.Run("hard", func(t *testing.T) {
		d := &runner.Docker{}
		c := cfg
		c.Egress = egressSettings{Isolation: "network", Allow: []string{"helpdesk.example.org"}}
		closeFn, err := wireEgress(ctx, d, c, false, quietLogger())
		if err != nil || closeFn != nil {
			t.Fatalf("err=%v close=%v", err, closeFn != nil)
		}
		if d.EgressIsolation != "network" || d.EgressRunnerToken != "runner-token" ||
			d.EgressProxyImage != runner.DefaultEgressProxyImage {
			t.Errorf("hard mode not wired: %+v", d)
		}
		if d.EgressProxyEnv["COVEY_CONTROL_URL"] != "https://covey.example" ||
			!strings.Contains(d.EgressProxyEnv["COVEY_EGRESS_ALLOW"], "covey.example") ||
			!strings.Contains(d.EgressProxyEnv["COVEY_EGRESS_ALLOW"], "helpdesk.example.org") {
			t.Errorf("proxy env: %v", d.EgressProxyEnv)
		}
	})

	t.Run("cooperative", func(t *testing.T) {
		d := &runner.Docker{}
		c := cfg
		c.Egress = egressSettings{ListenAddr: "127.0.0.1:0"} // zero value: enforced, proxy
		closeFn, err := wireEgress(ctx, d, c, false, quietLogger())
		if err != nil {
			t.Fatal(err)
		}
		if closeFn == nil {
			t.Fatal("no proxy to close — none was started")
		}
		defer closeFn()
		if !strings.HasPrefix(d.EgressProxyURL, "http://host.docker.internal:") || d.EgressIsolation == "network" {
			t.Errorf("cooperative mode not wired: url=%q isolation=%q", d.EgressProxyURL, d.EgressIsolation)
		}
	})

	t.Run("cooperative in a container without a fixed port", func(t *testing.T) {
		d := &runner.Docker{}
		if _, err := wireEgress(ctx, d, cfg, true, quietLogger()); err == nil ||
			!strings.Contains(err.Error(), "COVEY_EGRESS_LISTEN_ADDR") {
			t.Errorf("a proxy nobody can reach was accepted: %v", err)
		}
	})

	t.Run("switched off", func(t *testing.T) {
		d := &runner.Docker{}
		c := cfg
		c.Egress = egressSettings{Disabled: true}
		closeFn, err := wireEgress(ctx, d, c, false, quietLogger())
		if err != nil || closeFn != nil || d.EgressProxyURL != "" || d.EgressIsolation != "" {
			t.Errorf("switched off, yet something was wired: %+v err=%v", d, err)
		}
	})
}
