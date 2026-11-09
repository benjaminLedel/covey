package orchestrator

import (
	"context"
	"testing"

	"github.com/benjaminLedel/covey-plugin-sdk/target"

	"covey/internal/agents"
)

// TestPlaceholderRefusesSystemCredentials is #691: {{secret:<key>}} handed out
// a target system's own token and URL, past ACCESS.md, the activation and the
// deny_system guard rail that brokerCredential applies to the same values.
func TestPlaceholderRefusesSystemCredentials(t *testing.T) {
	target.Register(target.Descriptor{Name: "ticket-691", Label: "Tickets"})
	o := &Orchestrator{}
	agent := agents.Agent{}

	for _, key := range []string{"ticket-691_token", "ticket-691_url", "ticket-691_ca", "ticket-691_webhook_secret"} {
		if system, ok := o.systemCredentialKey(context.Background(), agent, key); !ok || system != "ticket-691" {
			t.Errorf("%s: (%q, %v), expected it to belong to ticket-691", key, system, ok)
		}
	}
	// A custom secret stays the placeholder's — including one whose name only
	// looks like a credential of a system that does not exist.
	for _, key := range []string{"staging_password", "api_token", "ticket-691", "_token"} {
		if system, ok := o.systemCredentialKey(context.Background(), agent, key); ok {
			t.Errorf("%s was taken for a credential of %q", key, system)
		}
	}
}
