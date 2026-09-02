package agents

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// The credential rules. They read what the secret store has learned about a
// token (#176) — a rejection from a run, an expiry a person or the plugin
// reported — and say it where a person looks for the agent's state. A finding
// here ends the way it is fixed: with a new value under the same key.

// CredentialState is what the lint sees of the token an agent's target system
// resolves to: the fields of secrets.Lifetime that decide a finding, plus
// where the value sits.
type CredentialState struct {
	System string
	Key    string
	// Own: the agent's own secret rather than an assigned org-wide one. It
	// decides where the hint sends the reader.
	Own            bool
	ExpiresAt      *time.Time
	RejectedAt     *time.Time
	RejectedReason string
	// Rotatable: the platform renews this one itself; a finding about its
	// expiry means that renewal did not happen.
	Rotatable bool
}

// credentialWarnAhead is the same fortnight the daily check warns from
// (internal/orchestrator): the lint and the mail agree on what "soon" means.
const credentialWarnAhead = 14 * 24 * time.Hour

func lintCredentials(s Subject) []Finding {
	now := s.Now
	if now.IsZero() {
		now = time.Now()
	}
	systems := lintSystems(s.Files["ACCESS.md"])
	var out []Finding
	for _, c := range s.Credentials {
		if !systems[strings.ToLower(c.System)] {
			continue // stored, but not this agent's business
		}
		place := "Secrets → " + c.Key
		if c.Own {
			place = "the agent's own secrets → " + c.Key
		}
		switch {
		case c.RejectedAt != nil:
			reason := c.RejectedReason
			if reason == "" {
				reason = "the credential was refused"
			}
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "credential-rejected", Severity: SeverityWarn,
				Message: fmt.Sprintf("%s refused %s on %s: %s — every action against it fails until the value is replaced",
					c.System, c.Key, c.RejectedAt.Format("2006-01-02 15:04"), reason),
				Hint: fmt.Sprintf("Store a new value under %s; the finding ends with it.", place),
			})
		case c.ExpiresAt != nil && !now.Before(*c.ExpiresAt):
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "credential-expired", Severity: SeverityWarn,
				Message: fmt.Sprintf("%s expired on %s — %s will refuse it", c.Key, c.ExpiresAt.Format("2006-01-02"), c.System),
				Hint:    expiryHint(c, place),
			})
		case c.ExpiresAt != nil && c.ExpiresAt.Sub(now) < credentialWarnAhead:
			days := int(c.ExpiresAt.Sub(now).Hours() / 24)
			out = append(out, Finding{
				AgentSlug: s.Slug, Rule: "credential-expiring", Severity: SeverityWarn,
				Message: fmt.Sprintf("%s expires on %s (in %d days)", c.Key, c.ExpiresAt.Format("2006-01-02"), days),
				Hint:    expiryHint(c, place),
			})
		}
	}
	return out
}

func expiryHint(c CredentialState, place string) string {
	if c.Rotatable {
		return "covey renews this credential itself a month ahead; that it has not means the last rotation failed — " +
			"the credential events in the recording say why. Until then: create a new one in " + c.System +
			" and store it under " + place + "."
	}
	return "Create a new credential in " + c.System + " and store it under " + place +
		" before the date; the finding ends with the new value."
}

// credentialStates collects, for one agent, the token each of its target
// systems would resolve to — the agent's own before the assigned org-wide one,
// the lowest slot, as the broker chooses — with what the store knows about it.
func credentialStates(ctx context.Context, pool *pgxpool.Pool, orgID, agentID uuid.UUID) ([]CredentialState, error) {
	rows, err := pool.Query(ctx, `SELECT s.key, s.agent_id IS NOT NULL, s.expires_at, s.rejected_at, s.rejected_reason, s.rotatable
		FROM secrets s
		WHERE s.org_id=$1 AND s.key LIKE '%\_token' AND (
			s.agent_id=$2
			OR (s.agent_id IS NULL AND EXISTS (SELECT 1 FROM secret_assignments a
				WHERE a.org_id=$1 AND a.key=s.key AND a.agent_id=$2)))
		ORDER BY s.key, s.agent_id NULLS LAST, s.slot`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CredentialState
	for rows.Next() {
		var c CredentialState
		if err := rows.Scan(&c.Key, &c.Own, &c.ExpiresAt, &c.RejectedAt, &c.RejectedReason, &c.Rotatable); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Key == c.Key {
			continue // the first row per key is the one the broker hands out
		}
		c.System = strings.TrimSuffix(c.Key, "_token")
		out = append(out, c)
	}
	return out, rows.Err()
}
