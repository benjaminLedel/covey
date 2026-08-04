package zammad

import (
	"os"
	"strings"
)

// Operational configuration of the Zammad plugin from ENV (12-factor, like the
// webhook secrets in internal/config). Everything has safe defaults, so an
// unset field keeps the previous behaviour.
//
// Why ENV and not the DB: the built-in SecretStore and the webhook secrets
// already run through ENV, and the control plane is single-node in the MVP. A
// per-org configuration in the DB (several support queues on several agents) is
// the next step — see docs/ops-zammad.md, section "Outlook".

// intakeGroups returns the allowlist of Zammad groups (queues) whose tickets
// may trigger a task at all. Format:
//
//	COVEY_ZAMMAD_INTAKE_GROUPS="Support L1, Beschwerden"
//
// Empty/unset → no restriction (all groups). The comparison is case-insensitive,
// leading/trailing spaces are ignored.
func intakeGroups() map[string]bool {
	return parseSet(os.Getenv("COVEY_ZAMMAD_INTAKE_GROUPS"))
}

// externalReplyType determines the Zammad article type for customer-visible
// answers (internal=false). Default "email" — the answer goes to the customer
// by mail. Overridable for web/chat-based instances via
//
//	COVEY_ZAMMAD_REPLY_TYPE=web
//
// Internal notes (internal=true) are always type "note".
func externalReplyType() string {
	if t := strings.TrimSpace(os.Getenv("COVEY_ZAMMAD_REPLY_TYPE")); t != "" {
		return t
	}
	return "email"
}

// parseSet splits a comma-separated ENV list into a set of lower-cased, trimmed
// values. Empty entries are dropped.
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
