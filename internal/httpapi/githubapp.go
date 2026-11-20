package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	ghplugin "github.com/benjaminLedel/covey-plugin-pack/github"
	"github.com/benjaminLedel/covey-plugin-sdk/target"

	"covey/internal/agents"
	"covey/internal/githubapp"
)

// GitHub Apps (#github-app): an organisation reaches GitHub through the
// platform's shared App ("Connect GitHub", one click) or through an App of its
// own, created via GitHub's manifest flow (its own bot name). Both end in an
// installation the control plane mints tokens for; a stored github_token keeps
// working beside them.
//
// The browser leaves covey for github.com and comes back to two public
// addresses. The session cookie is SameSite=Strict and does not come along;
// what binds the return to the organisation is a sealed state, and what binds
// it to the browser that started is a Lax flow cookie carrying the state's
// nonce.

const (
	ghFlowCookie   = "covey_github_flow"
	ghFlowPath     = "/api/integrations/github"
	ghManifestBack = ghFlowPath + "/manifest"
	ghInstallBack  = ghFlowPath + "/installed"
)

type githubAppStatus struct {
	// SharedAvailable: this instance offers its own App ("Connect GitHub").
	SharedAvailable bool   `json:"shared_available"`
	SharedSlug      string `json:"shared_slug,omitempty"`
	// CanCreate: a site address is configured, so GitHub can send the browser
	// back (needed by both flows).
	CanCreate  bool                  `json:"can_create"`
	Connection *githubapp.Connection `json:"connection,omitempty"`
	BotLogin   string                `json:"bot_login,omitempty"`
	Installed  bool                  `json:"installed"`
	Error      string                `json:"error,omitempty"`
}

func (s *Server) githubAppStatus(ctx context.Context, org uuid.UUID) githubAppStatus {
	st := githubAppStatus{}
	if s.GitHubApp == nil {
		return st
	}
	if s.GitHubApp.Shared.Configured() {
		st.SharedAvailable, st.SharedSlug = true, s.GitHubApp.Shared.Slug
	}
	_, st.CanCreate = s.tokenLinkBase(ctx)
	c, err := s.GitHubApp.Get(ctx, org)
	switch {
	case err == nil:
		st.Connection, st.BotLogin, st.Installed = &c, c.BotLogin(), c.Installed()
	case !errors.Is(err, githubapp.ErrNotConnected):
		st.Error = err.Error()
	}
	return st
}

// handleGitHubAppStatus — GET /api/v1/integrations/github.
func (s *Server) handleGitHubAppStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.githubAppStatus(r.Context(), principalFrom(r).OrgID))
}

// handleGitHubAppConnect — POST /api/v1/integrations/github/connect {source}:
// starts installing the shared App, or the organisation's own App once it
// exists. Answers {url}; the browser goes there.
func (s *Server) handleGitHubAppConnect(w http.ResponseWriter, r *http.Request) {
	if s.GitHubApp == nil {
		writeErr(w, http.StatusNotFound, "GitHub Apps are not offered on this installation")
		return
	}
	var in struct {
		Source string `json:"source"`
	}
	_ = readJSON(r, &in)
	if in.Source == "" {
		in.Source = githubapp.SourceShared
	}
	p := principalFrom(r)
	app, err := s.GitHubApp.AppFor(r.Context(), p.OrgID, in.Source)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if _, ok := s.tokenLinkBase(r.Context()); !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured (COVEY_SITE_URL) — GitHub can only send the browser back to a known address")
		return
	}
	state, nonce, err := s.GitHubApp.NewState(p.OrgID, "install", in.Source)
	if err != nil {
		mapErr(w, err)
		return
	}
	s.flowCookie(w, ghFlowCookie, nonce, ghFlowPath, int(githubapp.FlowTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"url": githubapp.InstallURL(app.Slug, state)})
}

// handleGitHubAppManifest — POST /api/v1/integrations/github/manifest
// {github_org, name}: starts creating the organisation's own App. GitHub's
// manifest flow is a form POST from the browser, so the answer carries the
// form's target and the manifest; the page submits it.
func (s *Server) handleGitHubAppManifest(w http.ResponseWriter, r *http.Request) {
	if s.GitHubApp == nil {
		writeErr(w, http.StatusNotFound, "GitHub Apps are not offered on this installation")
		return
	}
	var in struct {
		GitHubOrg string `json:"github_org"`
		Name      string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, `expected {"github_org","name"}`)
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || len(in.Name) > 34 {
		// GitHub caps an App's name at 34 characters.
		writeErr(w, http.StatusBadRequest, "the App needs a name of at most 34 characters")
		return
	}
	if !validGitHubLogin(in.GitHubOrg) {
		writeErr(w, http.StatusBadRequest, "github_org must be a GitHub organisation login (or empty for your personal account)")
		return
	}
	base, ok := s.tokenLinkBase(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured (COVEY_SITE_URL) — GitHub can only send the browser back to a known address")
		return
	}
	p := principalFrom(r)
	state, nonce, err := s.GitHubApp.NewState(p.OrgID, "manifest", githubapp.SourceOwn)
	if err != nil {
		mapErr(w, err)
		return
	}
	manifest, _ := json.Marshal(githubapp.NewManifest(in.Name, base))
	s.flowCookie(w, ghFlowCookie, nonce, ghFlowPath, int(githubapp.FlowTTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{
		"action":   githubapp.ManifestFormURL(in.GitHubOrg, state),
		"manifest": string(manifest),
	})
}

var githubLoginRe = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,38})$`)

func validGitHubLogin(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || githubLoginRe.MatchString(s)
}

// handleGitHubAppDisconnect — DELETE /api/v1/integrations/github.
func (s *Server) handleGitHubAppDisconnect(w http.ResponseWriter, r *http.Request) {
	if s.GitHubApp == nil {
		writeErr(w, http.StatusNotFound, "GitHub Apps are not offered on this installation")
		return
	}
	if err := s.GitHubApp.Disconnect(r.Context(), principalFrom(r).OrgID); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ghBack ends a flow: clear the cookie, return to the target page with the
// outcome the page shows.
func (s *Server) ghBack(w http.ResponseWriter, r *http.Request, outcome, detail string) {
	s.flowCookie(w, ghFlowCookie, "", ghFlowPath, -1)
	q := url.Values{"github": {outcome}}
	if detail != "" {
		q.Set("detail", detail)
	}
	http.Redirect(w, r, "/targets?"+q.Encode(), http.StatusSeeOther)
}

func ghCookieNonce(r *http.Request) string {
	if c, err := r.Cookie(ghFlowCookie); err == nil {
		return c.Value
	}
	return ""
}

// handleGitHubManifestReturn — GET /api/integrations/github/manifest?code&state:
// GitHub created the App. The code is exchanged for its credentials, they are
// sealed for the organisation, and the browser goes straight on to install it.
func (s *Server) handleGitHubManifestReturn(w http.ResponseWriter, r *http.Request) {
	if s.GitHubApp == nil {
		s.ghBack(w, r, "error", "not offered on this installation")
		return
	}
	q := r.URL.Query()
	flow, err := s.GitHubApp.OpenState(q.Get("state"), "manifest", ghCookieNonce(r))
	if err != nil {
		s.ghBack(w, r, "error", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	created, err := s.GitHubApp.Client.ConvertManifest(ctx, s.GitHubApp.API, q.Get("code"))
	if err != nil {
		s.Log.Warn("github app manifest conversion failed", "org", flow.Org, "err", err)
		s.ghBack(w, r, "error", err.Error())
		return
	}
	if err := s.GitHubApp.StoreOwnApp(ctx, flow.Org, created); err != nil {
		s.ghBack(w, r, "error", err.Error())
		return
	}
	s.Log.Info("github app created", "org", flow.Org, "app", created.Slug, "owner", created.Owner.Login)
	// Straight on to the installation, in the same browser.
	state, nonce, err := s.GitHubApp.NewState(flow.Org, "install", githubapp.SourceOwn)
	if err != nil {
		s.ghBack(w, r, "error", err.Error())
		return
	}
	s.flowCookie(w, ghFlowCookie, nonce, ghFlowPath, int(githubapp.FlowTTL.Seconds()))
	http.Redirect(w, r, githubapp.InstallURL(created.Slug, state), http.StatusSeeOther)
}

// handleGitHubInstallReturn — GET /api/integrations/github/installed
// ?installation_id&setup_action&state[&code]: the App is installed. The
// installation is confirmed with GitHub (and, for the shared App, against the
// installing user) before it is bound to the organisation.
func (s *Server) handleGitHubInstallReturn(w http.ResponseWriter, r *http.Request) {
	if s.GitHubApp == nil {
		s.ghBack(w, r, "error", "not offered on this installation")
		return
	}
	q := r.URL.Query()
	flow, err := s.GitHubApp.OpenState(q.Get("state"), "install", ghCookieNonce(r))
	if err != nil {
		s.ghBack(w, r, "error", err.Error())
		return
	}
	id, err := strconv.ParseInt(q.Get("installation_id"), 10, 64)
	if err != nil || id <= 0 {
		// setup_action=request: an organisation member asked an owner to
		// install; nothing to bind yet.
		if q.Get("setup_action") == "request" {
			s.ghBack(w, r, "requested", "")
			return
		}
		s.ghBack(w, r, "error", "GitHub sent no installation")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	c, err := s.GitHubApp.CompleteInstall(ctx, flow.Org, flow.Source, id, q.Get("code"))
	if err != nil {
		s.Log.Warn("github app installation not bound", "org", flow.Org, "installation", id, "err", err)
		s.ghBack(w, r, "error", err.Error())
		return
	}
	s.Log.Info("github app connected", "org", flow.Org, "source", c.Source, "installation", id, "account", c.AccountLogin)
	s.ghBack(w, r, "connected", c.AccountLogin)
}

// --- the central webhook ------------------------------------------------------

// ghHook is the part of a GitHub webhook the routing reads.
type ghHook struct {
	Action       string `json:"action"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
	Issue *struct {
		Body   string    `json:"body"`
		Labels []ghLabel `json:"labels"`
	} `json:"issue"`
	PullRequest *struct {
		Body   string    `json:"body"`
		Labels []ghLabel `json:"labels"`
	} `json:"pull_request"`
	Comment *struct {
		Body string `json:"body"`
	} `json:"comment"`
	Review *struct {
		Body string `json:"body"`
	} `json:"review"`
}

type ghLabel struct {
	Name string `json:"name"`
}

// handleGitHubAppWebhook — POST /api/webhooks/github-app: one address for every
// organisation. The installation says which organisation; the routing below
// says which of its agents.
func (s *Server) handleGitHubAppWebhook(w http.ResponseWriter, r *http.Request) {
	if s.GitHubApp == nil {
		writeErr(w, http.StatusNotFound, "GitHub Apps are not offered on this installation")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	var h ghHook
	if err := json.Unmarshal(body, &h); err != nil {
		writeErr(w, http.StatusBadRequest, "not a GitHub webhook")
		return
	}
	event := r.Header.Get("X-GitHub-Event")
	if event == "ping" {
		writeJSON(w, http.StatusOK, map[string]string{"outcome": "pong"})
		return
	}
	conn, err := s.GitHubApp.ByInstallation(r.Context(), h.Installation.ID)
	if err != nil {
		// An installation nobody connected (yet): acknowledged, nothing to do.
		writeJSON(w, http.StatusAccepted, map[string]string{"outcome": "installation not connected"})
		return
	}
	noteIncomingOrg(r, conn.OrgID)
	secret := conn.WebhookSecret()
	if secret == "" {
		writeErr(w, http.StatusUnauthorized, "no webhook secret for this GitHub App")
		return
	}
	if !ghplugin.VerifySignature(secret, body, r.Header.Get("X-Hub-Signature-256")) {
		writeErr(w, http.StatusUnauthorized, "invalid signature")
		return
	}
	if event == "installation" && h.Action == "deleted" {
		if err := s.GitHubApp.Uninstalled(r.Context(), h.Installation.ID); err != nil {
			mapErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"outcome": "uninstalled"})
		return
	}
	if !s.webhookLimiter.allow("github-app:"+conn.OrgID.String(), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many webhook calls for this organisation — please redeliver later")
		return
	}
	agent, why, err := s.routeGitHubHook(r.Context(), conn, h)
	if err != nil {
		mapErr(w, err)
		return
	}
	if agent == nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"outcome": "not routed", "reason": why})
		return
	}
	noteWebhookAgent(r, *agent)
	sys, err := s.Targets.System(r.Context(), conn.OrgID, "github")
	if err != nil {
		writeErr(w, http.StatusNotFound, "target system github is disabled for this organisation")
		return
	}
	hook, ok := sys.(target.Webhooker)
	if !ok {
		writeErr(w, http.StatusNotFound, "target system github has no webhook entrance")
		return
	}
	ev, err := hook.ParseWebhook(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	outcome, err := s.Orch.HandleWebhook(r.Context(), *agent, "github", ev)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome, "agent": agent.Slug, "routed_by": why})
}

// routeGitHubHook picks the ONE agent an event is for. Under a shared identity
// every agent of the organisation is "the bot", so the event has to say whom
// it addresses. In order:
//
//  1. a mention in the new text: "@<app> <agent-slug>" or "/covey <agent-slug>";
//  2. a label "agent:<agent-slug>" on the issue or pull request;
//  3. the marker of the agent that wrote the issue or pull request
//     (<!-- covey-agent: slug -->) — a review goes back to the PR's author;
//  4. the organisation's only agent with GitHub access.
//
// Only agents with GitHub in their ACCESS.md qualify. Several candidates and
// no address: not routed (the answer says how to address one) — delivering to
// all would start several agents on the same item.
func (s *Server) routeGitHubHook(ctx context.Context, conn githubapp.Connection, h ghHook) (*agents.Agent, string, error) {
	list, err := s.Registry.List(ctx, conn.OrgID)
	if err != nil {
		return nil, "", err
	}
	bySlug := map[string]agents.Agent{}
	var withAccess []agents.Agent
	for _, a := range list {
		if a.Killed || a.Status == agents.StatusKilled {
			continue
		}
		if ok, err := s.Registry.HasAccess(ctx, a.ID, "github", ""); err != nil || !ok {
			continue
		}
		bySlug[a.Slug] = a
		withAccess = append(withAccess, a)
	}
	pick := func(slug, why string) (*agents.Agent, string, bool) {
		if a, ok := bySlug[strings.ToLower(slug)]; ok {
			return &a, why, true
		}
		return nil, "", false
	}
	var text, threadBody string
	var labels []ghLabel
	if h.Comment != nil {
		text = h.Comment.Body
	} else if h.Review != nil {
		text = h.Review.Body
	}
	if h.PullRequest != nil {
		threadBody, labels = h.PullRequest.Body, h.PullRequest.Labels
	} else if h.Issue != nil {
		threadBody, labels = h.Issue.Body, h.Issue.Labels
	}
	if text == "" {
		text = threadBody
	}
	for _, m := range ghMentionRe(conn.AppSlug).FindAllStringSubmatch(text, -1) {
		if a, why, ok := pick(m[1], "mention"); ok {
			return a, why, nil
		}
	}
	for _, l := range labels {
		if slug, ok := strings.CutPrefix(strings.ToLower(l.Name), "agent:"); ok {
			if a, why, ok := pick(strings.TrimSpace(slug), "label"); ok {
				return a, why, nil
			}
		}
	}
	if slug := ghplugin.AgentFromBody(threadBody); slug != "" {
		if a, why, ok := pick(slug, "author"); ok {
			return a, why, nil
		}
	}
	switch len(withAccess) {
	case 0:
		return nil, "no agent of the organisation has GitHub in its ACCESS.md", nil
	case 1:
		a := withAccess[0]
		return &a, "only agent", nil
	}
	return nil, "several agents have GitHub access — address one with the label agent:<slug> or a mention /covey <slug>", nil
}

func ghMentionRe(appSlug string) *regexp.Regexp {
	names := []string{"covey"}
	if appSlug != "" && !strings.EqualFold(appSlug, "covey") {
		names = append(names, regexp.QuoteMeta(strings.ToLower(appSlug)))
	}
	return regexp.MustCompile(`(?i)(?:^|\s)[@/](?:` + strings.Join(names, "|") + `)(?:\[bot\])?\s+([a-z0-9][a-z0-9-]{0,62})\b`)
}

// githubAppProbeCredential tests the connection the way a run would get it:
// through the organisation's GitHub App, unless the agent tested as has a
// github_token of its own.
func (s *Server) githubAppProbeCredential(ctx context.Context, system string, org uuid.UUID, agentID *uuid.UUID) (target.Credential, bool) {
	if system != "github" || s.GitHubApp == nil {
		return target.Credential{}, false
	}
	if agentID != nil {
		if st, err := s.Secrets.Lookup(ctx, org, *agentID, "github_token"); err == nil && st.AgentID != nil {
			return target.Credential{}, false
		}
	}
	tok, conn, err := s.GitHubApp.Token(ctx, org)
	if err != nil {
		return target.Credential{}, false
	}
	return target.Credential{BaseURL: s.GitHubApp.BaseURL(), Token: tok.Value, Login: conn.BotLogin()}, true
}
