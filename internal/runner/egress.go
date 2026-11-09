package runner

import (
	"log/slog"
	"net"
	"net/url"
	"os"
	"strings"

	"covey/internal/egress"
)

// DefaultEgressProxyImage is the image of the hard mode's proxy container
// (`make egress-image`).
const DefaultEgressProxyImage = "covey-egress:latest"

// The egress wiring lives here, beside the provider it configures, and not in
// either binary: the built-in runner (cmd/covey) and the remote one
// (cmd/covey-runner) set the same fields from the same inputs. When each did
// it itself, the remote runner did not do it at all, and its sandboxes ran
// without any allowlist (#707). The path to a remote host is not a second
// implementation, and that includes the way out.

// UseHardEgress switches the provider to hard isolation: the sandbox on an
// internal network without internet, the proxy container as its only way out.
//
// controlURL is the control plane as this host reaches it; the proxy fetches
// the allowlist from there with runnerToken and is never given a database URL
// (spec/16, "Trust boundary"). allow is the operator's infrastructure add-on
// (COVEY_EGRESS_ALLOW). The control plane's own host is added to it, because in
// hard mode the daemon's link back runs through the proxy by CONNECT — without
// it no sandbox would connect.
func (p *Docker) UseHardEgress(controlURL, runnerToken string, allow []string, image string) {
	if image == "" {
		image = DefaultEgressProxyImage
	}
	p.EgressIsolation = "network"
	p.EgressProxyImage = image
	p.EgressRunnerToken = runnerToken
	p.EgressProxyURL = ""
	p.EgressProxyEnv = map[string]string{
		"COVEY_CONTROL_URL":       rewriteLoopbackForDocker(controlURL),
		"COVEY_EGRESS_ALLOW":      strings.Join(append(append([]string{}, allow...), controlPlaneHosts(controlURL)...), ","),
		"COVEY_EGRESS_PROXY_ADDR": ":8888",
		// The exceptions to the proxy's refusal of loopback and link-local
		// destinations, the same this host's other proxies apply (#705).
		"COVEY_OUTBOUND_ALLOW": os.Getenv("COVEY_OUTBOUND_ALLOW"),
	}
}

// controlPlaneHosts is what the hard-mode proxy lets through for the daemon
// link: host.docker.internal for a loopback address, and the public host when
// the address is a real name (spec/16, "Egress with distributed runners",
// point 3).
func controlPlaneHosts(controlURL string) []string {
	hosts := []string{"host.docker.internal"}
	if u, err := url.Parse(rewriteLoopbackForDocker(controlURL)); err == nil {
		if h := u.Hostname(); h != "" && h != "host.docker.internal" {
			hosts = append(hosts, h)
		}
	}
	return hosts
}

// StartCooperativeProxy binds the cooperative egress proxy in this process and
// returns the address a sandbox dials it under, without credentials — the
// provider appends the per-sandbox token per agent. listen empty means a free
// port, which is right for a process running directly on the host; inside a
// container the port has to be fixed and published, or no sandbox reaches it.
//
// The resolver is the caller's: the control plane reads the allowlist from its
// database, a remote runner asks the control plane for it with its token.
//
// What this mode is has to be said wherever it is chosen: the sandbox is told
// to use the proxy (HTTP_PROXY), nothing prevents it from opening a direct
// connection. It keeps cooperating tools on the list; it does not stop a
// sandbox that wants to send data elsewhere. That is the hard mode's job.
func StartCooperativeProxy(resolver egress.Resolver, listen string, log *slog.Logger) (string, func(), error) {
	proxy := egress.New(resolver, log)
	if listen == "" {
		listen = ":0"
	}
	addr, err := proxy.Start(listen)
	if err != nil {
		return "", nil, err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		_ = proxy.Close()
		return "", nil, err
	}
	return "http://host.docker.internal:" + port, func() { _ = proxy.Close() }, nil
}
