package main

import (
	"strings"
	"testing"
)

// The unit carries the config path because register may have written it
// somewhere else — a unit that starts the runner without its token is a host
// that reports offline for a reason nobody sees in the runner view.
func TestUnitCarriesBinaryAndConfig(t *testing.T) {
	got := unitFile(unitParams{Binary: "/usr/local/bin/covey-runner", Config: "/etc/covey-runner/config.toml"})
	for _, want := range []string{
		"ExecStart=/usr/local/bin/covey-runner run --config /etc/covey-runner/config.toml",
		"After=network-online.target docker.service",
		"Restart=always",
		"WantedBy=multi-user.target",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit is missing %q:\n%s", want, got)
		}
	}
	// Root is the default, and a User= line for it would be noise.
	if strings.Contains(got, "User=") {
		t.Errorf("no User= belongs in the default unit:\n%s", got)
	}
}

// A user of its own still needs the Docker socket. Without the group the
// service starts and fails at the first sandbox — later, and in a place that
// looks like a Docker problem rather than a permission one.
func TestUnitGivesANonRootUserTheDockerGroup(t *testing.T) {
	got := unitFile(unitParams{Binary: "/usr/local/bin/covey-runner", Config: "/etc/covey-runner/config.toml", User: "covey"})
	if !strings.Contains(got, "User=covey") || !strings.Contains(got, "SupplementaryGroups=docker") {
		t.Errorf("unit for a non-root user is incomplete:\n%s", got)
	}
}
