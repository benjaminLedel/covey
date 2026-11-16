package orchestrator

import (
	"context"

	"covey/internal/netguard"
)

// orgURLContext marks ctx for a request the control plane itself makes to an
// organisation-configured URL — a target system's <system>_url — and checks
// that URL up front (#692). The mark is what makes the plugin's HTTP client
// refuse loopback, link-local and multicast destinations at dial time; it is
// opt-in per context, so every control-plane plugin call (a credential probe,
// a workflow action step, a gate or nur-wenn work check, a platform issue)
// goes through here rather than deriving its own context. The up-front check
// only answers earlier and more legibly; the dial-time check stays the real one.
func orgURLContext(ctx context.Context, baseURL string) (context.Context, error) {
	ctx = netguard.Guard(ctx)
	if err := netguard.CheckURL(ctx, baseURL); err != nil {
		return ctx, err
	}
	return ctx, nil
}
