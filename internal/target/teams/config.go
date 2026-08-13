package teams

import (
	"os"
	"strings"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Operational configuration of the Teams plugin from ENV (12-factor, like the
// webhook secrets in internal/config and the Zammad plugin). Everything has
// safe defaults, so an unset field keeps the previous behaviour.

// defaultTokenEndpoint is the OAuth2 token endpoint for Bot Connector access
// (client_credentials). The default is the multi-tenant Bot Framework
// endpoint; overridable instance-wide via COVEY_TEAMS_TOKEN_URL and per agent
// via the brokered teams_url (e.g. a tenant-specific endpoint for
// single-tenant bots).
func defaultTokenEndpoint() string {
	if v := strings.TrimSpace(os.Getenv("COVEY_TEAMS_TOKEN_URL")); v != "" {
		return v
	}
	return "https://login.microsoftonline.com/botframework.com/oauth2/v2.0/token"
}

// connectorScope is the OAuth2 scope the Bot Connector expects.
const connectorScope = "https://api.botframework.com/.default"

// intakeTenants returns the allowlist of Microsoft 365 tenant IDs whose
// messages may trigger a task at all. Format:
//
//	COVEY_TEAMS_INTAKE_TENANTS="11111111-2222-3333-4444-555555555555, …"
//
// Empty/unset → no restriction (all tenants). Comparison is case-insensitive,
// leading/trailing whitespace is ignored.
func intakeTenants() map[string]bool {
	return parseSet(os.Getenv("COVEY_TEAMS_INTAKE_TENANTS"))
}

// maxAttachmentBytes is the upper bound for a single attachment materialized
// into the sandbox. Default 25 MB, overridable via
// COVEY_TEAMS_ATTACHMENT_MAX_MB (1 up to 1024 MB). Larger values are clamped,
// unparsable ones keep the default — both with a line in the log, see the
// shared helper in internal/target.
func maxAttachmentBytes() int64 {
	return target.MaxBytesFromEnv("COVEY_TEAMS_ATTACHMENT_MAX_MB", 25, 1024)
}

// parseSet splits a comma-separated ENV list into a set of lower-cased,
// trimmed values. Empty entries are discarded.
func parseSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, part := range strings.Split(raw, ",") {
		v := strings.ToLower(strings.TrimSpace(part))
		if v != "" {
			out[v] = true
		}
	}
	return out
}
