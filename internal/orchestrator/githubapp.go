package orchestrator

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/githubapp"
	"covey/internal/observability"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// An organisation connected through a GitHub App gets an installation token
// minted per request instead of a stored github_token. Order of precedence:
//
//  1. a github_token the AGENT owns (agent-scoped secret) — someone gave this
//     one agent its own account on purpose;
//  2. the organisation's GitHub App, when it is installed;
//  3. the organisation's github_token, as before.
//
// The ACCESS.md, activation and guard-rail checks have run before this point;
// the App needs no secret assignment, because the token is never stored.

// agentOwnsGitHubToken reports whether the agent has a github_token of its own.
func (o *Orchestrator) agentOwnsGitHubToken(ctx context.Context, orgID, agentID uuid.UUID) bool {
	st, err := o.Secrets.Lookup(ctx, orgID, agentID, "github_token")
	return err == nil && st.AgentID != nil
}

// githubAppCredential: used=false means "no App here — carry on with tokens".
// used=true with Granted=false means the App is connected but failed; that is
// reported, not silently replaced by some other account's token.
func (o *Orchestrator) githubAppCredential(ctx context.Context, agent agents.Agent, req daemon.RequestCredential) (daemon.InjectCredentials, bool) {
	if o.GitHubApp == nil || o.agentOwnsGitHubToken(ctx, agent.OrgID, agent.ID) {
		return daemon.InjectCredentials{}, false
	}
	tok, conn, err := o.GitHubApp.Token(ctx, agent.OrgID)
	if errors.Is(err, githubapp.ErrNotConnected) {
		return daemon.InjectCredentials{}, false
	}
	if err != nil {
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System, Reason: err.Error()}, true
	}
	ttl := o.DaemonTokenTTL
	// The daemon caches for TTLSecs; never past the token's own life (minus a
	// margin for an action that starts just before).
	if left := time.Until(tok.ExpiresAt) - 5*time.Minute; left < ttl {
		ttl = max(left, time.Minute)
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": req.System, "granted": true, "github_app": conn.Source,
			"installation": conn.InstallationID, "ttl_secs": int(ttl.Seconds())})
	return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System, Granted: true,
		Token: tok.Value, Login: conn.BotLogin(), BaseURL: o.GitHubApp.BaseURL(), CA: o.brokerCA(ctx, agent, req.System),
		TTLSecs: int(ttl.Seconds())}, true
}

// githubAppPollCredential is the same choice for the nur-wenn: pre-check.
func (o *Orchestrator) githubAppPollCredential(ctx context.Context, orgID, agentID uuid.UUID) (target.Credential, bool) {
	if o.GitHubApp == nil || o.agentOwnsGitHubToken(ctx, orgID, agentID) {
		return target.Credential{}, false
	}
	tok, conn, err := o.GitHubApp.Token(ctx, orgID)
	if err != nil {
		if !errors.Is(err, githubapp.ErrNotConnected) {
			o.Log.Warn("nur-wenn: GitHub App token failed", "org", orgID, "err", err)
		}
		return target.Credential{}, false
	}
	return target.Credential{BaseURL: o.GitHubApp.BaseURL(), Token: tok.Value, Login: conn.BotLogin()}, true
}

// orgName is the organisation's display name for signatures; "" when unknown.
func (o *Orchestrator) orgName(ctx context.Context, orgID uuid.UUID) string {
	if o.Pool == nil {
		return ""
	}
	var name string
	_ = o.Pool.QueryRow(ctx, `SELECT name FROM organizations WHERE id = $1`, orgID).Scan(&name)
	return name
}

// githubAppInstalled reports whether the organisation has an installed App.
func (o *Orchestrator) githubAppInstalled(ctx context.Context, orgID uuid.UUID) bool {
	c, err := o.GitHubApp.Get(ctx, orgID)
	return err == nil && c.Installed()
}
