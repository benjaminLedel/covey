package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"covey/internal/egress"
	"covey/internal/runner"
)

// The egress settings of a runner host. They mean what the control plane's
// variables of the same names mean (docs/en/operations/operations.md, "Egress
// isolation"), and they are read on this host because this is where the
// sandboxes run: the enforcement point has to stand next to them.
//
// Until #707 the remote runner set none of this, and its sandboxes reached any
// host — not even the cooperative proxy stood in between. Enforcement is on by
// default here as it is on the control plane; switching it off is a sentence
// somebody writes into the config, and the runner repeats it at every start.
type egressSettings struct {
	// Disabled: egress_enforce = false / COVEY_EGRESS_ENFORCE=false. Stored
	// the other way round so that the zero value is the enforced one — a
	// config struct somebody builds without thinking of egress must not be
	// the open one.
	Disabled bool
	// Isolation: egress_isolation / COVEY_EGRESS_ISOLATION, "proxy" (the
	// default, cooperative) or "network" (hard). Empty reads as "proxy".
	Isolation string
	// Allow: egress_allow / COVEY_EGRESS_ALLOW — infrastructure hosts every
	// sandbox on this host may reach, on top of the agent's allowlist.
	Allow []string
	// ListenAddr: egress_listen_addr / COVEY_EGRESS_LISTEN_ADDR — where the
	// cooperative proxy binds. Empty = a free port.
	ListenAddr string
	// ProxyImage: egress_proxy_image / COVEY_EGRESS_PROXY_IMAGE — the hard
	// mode's proxy image. A remote host does not have the repository to run
	// `make egress-image` in, so the image can come from a registry.
	ProxyImage string
}

func defaultEgressSettings() egressSettings {
	return egressSettings{Isolation: "proxy"}
}

// mode is the isolation in effect, with the default filled in.
func (e egressSettings) mode() string {
	if e.Isolation == "" {
		return "proxy"
	}
	return e.Isolation
}

// set applies one config-file or environment value. An unknown
// isolation mode or an unreadable boolean is an error, not a fallback: a typo
// that silently became the open or the weaker mode is the failure these
// settings exist to prevent.
func (e *egressSettings) set(key, value string) error {
	value = strings.TrimSpace(value)
	switch key {
	case "enforce":
		b, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("egress enforce = %q is not true or false", value)
		}
		e.Disabled = !b
	case "isolation":
		switch value {
		case "proxy", "network":
			e.Isolation = value
		default:
			return fmt.Errorf("egress isolation = %q: only \"proxy\" and \"network\" exist", value)
		}
	case "allow":
		e.Allow = splitHosts(value)
	case "listen_addr":
		e.ListenAddr = value
	case "proxy_image":
		e.ProxyImage = value
	}
	return nil
}

// applyEgressEnv lets the control plane's variable names override the config
// file — the same names an operator already knows from `covey serve`, and the
// way a systemd drop-in or a container sets them.
func applyEgressEnv(e *egressSettings, getenv func(string) string) error {
	for key, name := range map[string]string{
		"enforce":     "COVEY_EGRESS_ENFORCE",
		"isolation":   "COVEY_EGRESS_ISOLATION",
		"allow":       "COVEY_EGRESS_ALLOW",
		"listen_addr": "COVEY_EGRESS_LISTEN_ADDR",
		"proxy_image": "COVEY_EGRESS_PROXY_IMAGE",
	} {
		if v := getenv(name); v != "" {
			if err := e.set(key, v); err != nil {
				return fmt.Errorf("%s: %w", name, err)
			}
		}
	}
	return nil
}

func splitHosts(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// wireEgress puts the enforcement point in front of this host's sandboxes,
// through the same provider fields and the same proxy the built-in runner
// uses. The proxy authenticates to the control plane with this runner's token
// and gets the allowlist of the runner's own organisation only — the control
// plane answers a foreign agent with 404.
//
// It returns a function that stops the cooperative proxy; nil when there is
// none to stop.
func wireEgress(ctx context.Context, docker *runner.Docker, cfg config, inContainer bool, log *slog.Logger) (func(), error) {
	eg := cfg.Egress
	if eg.Disabled {
		log.Warn("egress NOT enforced (egress_enforce = false) — sandboxes on this host reach any host; " +
			"the allowlists are kept on the control plane but not applied here")
		return nil, nil
	}
	if eg.mode() == "network" {
		docker.UseHardEgress(cfg.URL, cfg.Token, eg.Allow, eg.ProxyImage)
		log.Info("egress enforcement: hard network isolation", "proxy-image", docker.EgressProxyImage)
		return nil, nil
	}

	if inContainer && eg.ListenAddr == "" {
		// The same configuration the control plane refuses to stay silent
		// about: a free port inside a container that no host publishes, and
		// every request of every sandbox is refused.
		return nil, fmt.Errorf("the runner runs in a container and the cooperative egress proxy would bind " +
			"a free port no host publishes — sandboxes would get nothing out. Set COVEY_EGRESS_LISTEN_ADDR=:8495 " +
			"and publish 8495:8495, or use egress_isolation = \"network\"")
	}
	resolver := egress.NewAPIResolver(ctx, cfg.URL, cfg.Token, eg.Allow, 15*time.Second, log)
	proxyURL, closeProxy, err := runner.StartCooperativeProxy(resolver, eg.ListenAddr, log)
	if err != nil {
		return nil, err
	}
	docker.EgressProxyURL = proxyURL
	log.Info("egress enforcement active (cooperative — a sandbox can connect around the proxy; "+
		"egress_isolation = \"network\" is the mode that stops it)", "proxy", proxyURL, "base", eg.Allow)
	return closeProxy, nil
}

// runningInContainer: docker puts this marker at the root of every container.
func runningInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}
