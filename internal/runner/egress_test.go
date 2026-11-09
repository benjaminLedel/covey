package runner

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/egress"
)

// A container reaches the host under a different name than the host reaches
// itself. Only loopback is bent — a real deployment address stays as it is,
// because bending that would send the proxy to the wrong machine.
func TestRewriteLoopbackForProxyContainer(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"http://localhost:8494", "http://host.docker.internal:8494"},
		{"https://localhost", "https://host.docker.internal"},
		{"https://covey.example.com", "https://covey.example.com"},
		// Unparseable: handed back rather than mangled.
		{"://kaputt", "://kaputt"},
		{"", ""},
	} {
		if got := rewriteLoopbackForDocker(tc.in); got != tc.want {
			t.Errorf("rewriteLoopbackForDocker(%q) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}

// In hard mode the daemon dials the control plane through the proxy, so the
// proxy has to let it through: host.docker.internal for a loopback address,
// and the public name when there is one — without it no sandbox of an
// instance behind a real domain would connect back (#445).
func TestControlPlaneHosts(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
	}{
		{"http://localhost:8494", "host.docker.internal"},
		{"https://covey.example.com", "host.docker.internal,covey.example.com"},
		{"http://10.0.0.5:8494", "host.docker.internal,10.0.0.5"},
		{"", "host.docker.internal"},
	} {
		if got := strings.Join(controlPlaneHosts(tc.in), ","); got != tc.want {
			t.Errorf("controlPlaneHosts(%q) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}

// Both runners configure hard isolation through this one method. What it sets
// is what the proxy container needs to work on a host that is not the control
// plane's: the control plane's address, its host on the list, the runner token
// — and no cooperative URL left over beside it.
func TestUseHardEgress(t *testing.T) {
	p := &Docker{EgressProxyURL: "http://host.docker.internal:1234"}
	p.UseHardEgress("https://covey.example.com", "runner-token", []string{"helpdesk.example.org"}, "")

	if p.EgressIsolation != "network" {
		t.Errorf("isolation = %q", p.EgressIsolation)
	}
	if p.EgressProxyImage != DefaultEgressProxyImage {
		t.Errorf("image = %q, expected the default", p.EgressProxyImage)
	}
	if p.EgressRunnerToken != "runner-token" {
		t.Errorf("runner token = %q", p.EgressRunnerToken)
	}
	if p.EgressProxyURL != "" {
		t.Errorf("a cooperative proxy URL survived beside hard isolation: %q", p.EgressProxyURL)
	}
	if got := p.EgressProxyEnv["COVEY_CONTROL_URL"]; got != "https://covey.example.com" {
		t.Errorf("COVEY_CONTROL_URL = %q", got)
	}
	if got := p.EgressProxyEnv["COVEY_EGRESS_ALLOW"]; got != "helpdesk.example.org,host.docker.internal,covey.example.com" {
		t.Errorf("COVEY_EGRESS_ALLOW = %q", got)
	}
	if _, leaked := p.EgressProxyEnv["COVEY_DATABASE_URL"]; leaked {
		t.Error("the proxy container was given a database URL")
	}

	p.UseHardEgress("http://localhost:8494", "t", nil, "registry.example.org/covey-egress:1")
	if p.EgressProxyImage != "registry.example.org/covey-egress:1" {
		t.Errorf("an explicit image was not kept: %q", p.EgressProxyImage)
	}
	if got := p.EgressProxyEnv["COVEY_CONTROL_URL"]; got != "http://host.docker.internal:8494" {
		t.Errorf("a loopback control plane was not bent for the container: %q", got)
	}
}

// denyAll is a resolver that knows no agent — what the proxy answers then is
// the fail-closed 407.
type denyAll struct{}

func (denyAll) Resolve(context.Context, string, string) (*egress.Allowlist, uuid.UUID, bool) {
	return nil, uuid.Nil, false
}
func (denyAll) Log(uuid.UUID, string, string, bool) {}

// The cooperative proxy is reachable under host.docker.internal on the port it
// bound, and a request without a sandbox's token is refused.
func TestStartCooperativeProxy(t *testing.T) {
	containerURL, closeProxy, err := StartCooperativeProxy(denyAll{}, "127.0.0.1:0", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeProxy()

	u, err := url.Parse(containerURL)
	if err != nil || u.Hostname() != "host.docker.internal" || u.Port() == "" || u.User != nil {
		t.Fatalf("container URL %q: expected host.docker.internal with a port and no credentials", containerURL)
	}

	proxyURL, _ := url.Parse("http://127.0.0.1:" + u.Port())
	client := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	resp, err := client.Get("http://example.org/")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusProxyAuthRequired {
		t.Errorf("a request without a token got %d, expected 407", resp.StatusCode)
	}
}
