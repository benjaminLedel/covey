package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/google/uuid"

	"covey/internal/msauth"
	"covey/internal/teamsapp"
	"covey/internal/teamsapp/teamsys"
	"covey/internal/teamsuser"
)

// The installation's Teams app (#710): one messaging endpoint for every
// organisation, the tenant link, and what an organisation sees of it.

// handleTeamsAppWebhook — POST /api/webhooks/teamsapp, the bot's messaging
// endpoint. Verified before anything is read into the database: the token
// must be the Bot Framework's for this app, and its serviceurl claim must
// name the host the body names, since that host is stored and later gets the
// bot's credential.
func (s *Server) handleTeamsAppWebhook(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "no Teams app on this installation")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	a, err := teams.ParseWebhook(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.TeamsApp.Verify(r.Header.Get("Authorization"), a.ServiceURL); err != nil {
		s.Log.Warn("teams app: delivery refused", "err", err)
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	if t := a.TenantID(); t != "" && !s.webhookLimiter.allow("teamsapp:"+t, time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many messages from this tenant — please redeliver later")
		return
	}

	if strings.EqualFold(a.Type, "invoke") && a.Name == "signin/tokenExchange" {
		s.teamsAppTokenExchange(w, r, a, body)
		return
	}

	in, err := s.TeamsApp.Route(r.Context(), a, body)
	if err != nil {
		mapErr(w, err)
		return
	}
	if in.SignIn {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		base, _ := s.tokenLinkBase(ctx)
		link := base + "/api/teamsapp/signin?t=" + url.QueryEscape(s.TeamsApp.PersonLinkToken(in.Tenant, in.Person))
		if err := s.TeamsApp.AskSignIn(ctx, a, base, link); err != nil {
			s.Log.Warn("teams app: sign-in card not sent", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]string{"outcome": "sign_in_requested"})
		return
	}
	if in.Answer != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := s.TeamsApp.Say(ctx, a, in.Answer); err != nil {
			s.Log.Warn("teams app: answer not sent", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]string{"outcome": "answered"})
		return
	}
	if in.Agent == nil {
		writeJSON(w, http.StatusOK, map[string]string{"outcome": "ignored"})
		return
	}
	noteWebhookAgent(r, *in.Agent)
	outcome, err := s.teamsAppHandOver(r.Context(), in)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
}

// teamsAppHandOver gives a routed message to its agent and records, before
// the run can start, whose message the task is working on (#713).
func (s *Server) teamsAppHandOver(ctx context.Context, in teamsapp.Inbound) (string, error) {
	var record func(context.Context, uuid.UUID) error
	if s.TeamsUser != nil && in.Person != "" {
		record = func(ctx context.Context, task uuid.UUID) error {
			return s.TeamsUser.SetTaskPerson(ctx, task, in.Tenant, in.Person)
		}
	}
	outcome, _, err := s.Orch.HandleWebhookTask(ctx, *in.Agent, teamsys.Name, in.Event, record)
	return outcome, err
}

// teamsAppTokenExchange answers Teams' signin/tokenExchange invoke: the
// token of the person who saw the sign-in card. Exchanged on their behalf
// and stored, the kept message goes to the agent. A failed exchange is
// answered 412, which tells Teams to fall back — and the bot offers the
// browser sign-in.
func (s *Server) teamsAppTokenExchange(w http.ResponseWriter, r *http.Request, a teams.Activity, body []byte) {
	var in struct {
		Value struct {
			ID             string `json:"id"`
			ConnectionName string `json:"connectionName"`
			Token          string `json:"token"`
		} `json:"value"`
	}
	_ = json.Unmarshal(body, &in)
	tenant, oid := a.TenantID(), a.From.AADObjectID
	fail := func(detail string) {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{
			"id": in.Value.ID, "connectionName": in.Value.ConnectionName, "failureDetail": detail})
	}
	if s.TeamsUser == nil || !s.TeamsUser.SSOEnabled() || tenant == "" || oid == "" || in.Value.Token == "" {
		fail("single sign-on is not available here")
		return
	}
	if _, err := s.TeamsApp.Store.OrgOfTenant(r.Context(), tenant); err != nil {
		fail("this tenant is not connected to covey")
		return
	}
	if err := s.TeamsUser.Exchange(r.Context(), tenant, oid, in.Value.Token); err != nil {
		s.Log.Info("teams app: token exchange failed, offering the browser sign-in", "err", err)
		fail("exchange failed")
		base, _ := s.tokenLinkBase(r.Context())
		link := base + "/api/teamsapp/signin?t=" + url.QueryEscape(s.TeamsApp.PersonLinkToken(tenant, oid))
		go func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := s.TeamsApp.OfferLink(ctx, a, link); err != nil {
				s.Log.Warn("teams app: sign-in link not sent", "err", err)
			}
		}(context.WithoutCancel(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": in.Value.ID, "connectionName": in.Value.ConnectionName})
	go s.teamsAppReplay(context.WithoutCancel(r.Context()), tenant, oid)
}

// teamsAppReplay hands the message kept during sign-in to its agent.
func (s *Server) teamsAppReplay(ctx context.Context, tenant, oid string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	raw, ok := s.TeamsUser.TakePending(ctx, tenant, oid)
	if !ok {
		return
	}
	a, err := teams.ParseWebhook(raw)
	if err != nil {
		return
	}
	in, err := s.TeamsApp.Route(ctx, a, raw)
	if err != nil {
		s.Log.Warn("teams app: kept message not routed", "err", err)
		return
	}
	switch {
	case in.Agent != nil:
		if _, err := s.teamsAppHandOver(ctx, in); err != nil {
			s.Log.Warn("teams app: kept message not handed over", "err", err)
		}
	case in.Answer != "":
		_ = s.TeamsApp.Say(ctx, a, in.Answer)
	}
}

// handleTeamsAppPersonSignIn — GET /api/teamsapp/signin?t=…: the browser
// sign-in the bot offers. Sets the binding cookie and goes to Microsoft.
func (s *Server) handleTeamsAppPersonSignIn(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil || s.TeamsUser == nil || !s.TeamsUser.SSOEnabled() {
		writeErr(w, http.StatusNotFound, "not available on this installation")
		return
	}
	tenant, oid, err := s.TeamsApp.CheckPersonLink(r.URL.Query().Get("t"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	base, ok := s.tokenLinkBase(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured (COVEY_SITE_URL)")
		return
	}
	signIn, nonce, err := s.TeamsUser.StartPersonSignIn(tenant, oid, base+"/api/teamsapp/person-callback")
	if err != nil {
		mapErr(w, err)
		return
	}
	s.flowCookie(w, teamsuser.Cookie+"_person", nonce, "/api/teamsapp/person-callback", int(msauth.TTL.Seconds()))
	http.Redirect(w, r, signIn, http.StatusSeeOther)
}

// handleTeamsAppPersonCallback — GET /api/teamsapp/person-callback.
func (s *Server) handleTeamsAppPersonCallback(w http.ResponseWriter, r *http.Request) {
	page := func(status int, text string) {
		s.flowCookie(w, teamsuser.Cookie+"_person", "", "/api/teamsapp/person-callback", -1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><meta name=viewport content='width=device-width'>" +
			"<title>covey</title><body style='font-family:system-ui;max-width:32rem;margin:4rem auto;padding:0 1rem'><p>" +
			html.EscapeString(text) + "</p></body>"))
	}
	if s.TeamsApp == nil || s.TeamsUser == nil || !s.TeamsUser.SSOEnabled() {
		page(http.StatusNotFound, "Not available on this installation.")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		page(http.StatusBadRequest, "Microsoft did not sign you in: "+q.Get("error_description"))
		return
	}
	base, _ := s.tokenLinkBase(r.Context())
	nonce := ""
	if c, err := r.Cookie(teamsuser.Cookie + "_person"); err == nil {
		nonce = c.Value
	}
	tenant, oid, err := s.TeamsUser.FinishPersonSignIn(r.Context(), q.Get("state"), nonce, q.Get("code"), base+"/api/teamsapp/person-callback")
	if err != nil {
		s.Log.Warn("teams app: person sign-in failed", "err", err)
		page(http.StatusBadRequest, "Signing in did not work: "+err.Error())
		return
	}
	go s.teamsAppReplay(context.WithoutCancel(r.Context()), tenant, oid)
	page(http.StatusOK, "You are signed in. Close this window and go back to Teams — your message is being handled.")
}

// teamsAppCallback is the redirect URI registered with the app. It needs the
// configured site address: Microsoft only returns to addresses registered in
// advance, and one taken from a request header is not one.
func (s *Server) teamsAppCallback(ctx context.Context) (string, bool) {
	base, ok := s.tokenLinkBase(ctx)
	if !ok {
		return "", false
	}
	return base + "/api/teamsapp/callback", true
}

// handleTeamsAppStatus — GET /api/v1/teamsapp: whether the installation has
// an app, the organisation's linked tenants, the teams the bot is in, and the
// channel defaults.
func (s *Server) handleTeamsAppStatus(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	org := principalFrom(r).OrgID
	tenants, err := s.TeamsApp.Store.Tenants(r.Context(), org)
	if err != nil {
		mapErr(w, err)
		return
	}
	teamList, err := s.TeamsApp.Store.Teams(r.Context(), org)
	if err != nil {
		mapErr(w, err)
		return
	}
	defaults, err := s.TeamsApp.Store.ChannelAgents(r.Context(), org)
	if err != nil {
		mapErr(w, err)
		return
	}
	_, callbackOK := s.teamsAppCallback(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"available":      true,
		"app_id":         s.TeamsApp.AppID(),
		"can_link":       callbackOK,
		"tenants":        tenants,
		"teams":          teamList,
		"channel_agents": defaults,
	})
}

// handleTeamsAppLink — POST /api/v1/teamsapp/link: starts the sign-in with
// Microsoft. The nonce goes into a cookie of this browser only; see
// teamsapp/link.go for why the callback refuses without it.
func (s *Server) handleTeamsAppLink(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "no Teams app on this installation")
		return
	}
	callback, ok := s.teamsAppCallback(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured (COVEY_SITE_URL) — Microsoft can only return to a registered address")
		return
	}
	signIn, nonce, err := s.TeamsApp.StartLink(principalFrom(r).OrgID, callback)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Lax, not Strict like the session: the callback is a top-level
	// navigation coming back from Microsoft, and a Strict cookie would not
	// travel with it.
	s.flowCookie(w, teamsapp.LinkCookie, nonce, "/api/teamsapp/callback", int((15 * time.Minute).Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"url": signIn})
}

// handleTeamsAppCallback — GET /api/teamsapp/callback: Microsoft's redirect
// after the sign-in. Answers with a redirect into the settings page either
// way, carrying the outcome.
func (s *Server) handleTeamsAppCallback(w http.ResponseWriter, r *http.Request) {
	back := func(outcome, detail string) {
		s.flowCookie(w, teamsapp.LinkCookie, "", "/api/teamsapp/callback", -1)
		q := url.Values{"teams": {outcome}}
		if detail != "" {
			q.Set("detail", detail)
		}
		http.Redirect(w, r, "/targets?"+q.Encode(), http.StatusSeeOther)
	}
	if s.TeamsApp == nil {
		back("error", "no Teams app on this installation")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		back("error", q.Get("error_description"))
		return
	}
	callback, ok := s.teamsAppCallback(r.Context())
	if !ok {
		back("error", "no site address configured")
		return
	}
	nonce := ""
	if c, err := r.Cookie(teamsapp.LinkCookie); err == nil {
		nonce = c.Value
	}
	org, tenant, err := s.TeamsApp.FinishLink(r.Context(), q.Get("state"), nonce, q.Get("code"), callback)
	switch {
	case errors.Is(err, teamsapp.ErrTenantTaken):
		back("taken", "")
	case errors.Is(err, teamsapp.ErrNotAdmin):
		back("not_admin", err.Error())
	case err != nil:
		s.Log.Warn("teams app: link failed", "err", err)
		back("error", err.Error())
	default:
		s.Log.Info("teams app: tenant linked", "org", org, "tenant", tenant)
		back("linked", "")
	}
}

// handleTeamsAppUnlink — DELETE /api/v1/teamsapp/tenants/{tenant}.
func (s *Server) handleTeamsAppUnlink(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "no Teams app on this installation")
		return
	}
	err := s.TeamsApp.Store.Unlink(r.Context(), principalFrom(r).OrgID, r.PathValue("tenant"))
	if errors.Is(err, teamsapp.ErrNotLinked) {
		writeErr(w, http.StatusNotFound, "this tenant is not linked to your organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTeamsAppChannels — GET /api/v1/teamsapp/channels, fresh from Teams.
func (s *Server) handleTeamsAppChannels(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "no Teams app on this installation")
		return
	}
	list, err := s.TeamsApp.Channels(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleTeamsAppChannelAgent — PUT /api/v1/teamsapp/channel-agent
// {tenant_id, channel_id, agent_id|null}: the colleague a channel goes to
// when a message names nobody.
func (s *Server) handleTeamsAppChannelAgent(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "no Teams app on this installation")
		return
	}
	var in struct {
		TenantID  string     `json:"tenant_id"`
		ChannelID string     `json:"channel_id"`
		AgentID   *uuid.UUID `json:"agent_id"`
	}
	if err := readJSON(r, &in); err != nil || in.TenantID == "" || in.ChannelID == "" {
		writeErr(w, http.StatusBadRequest, `expected {"tenant_id","channel_id","agent_id"}`)
		return
	}
	org := principalFrom(r).OrgID
	if in.AgentID != nil {
		a, err := s.Registry.Get(r.Context(), *in.AgentID)
		if err != nil || a.OrgID != org {
			writeErr(w, http.StatusNotFound, "no such agent in your organisation")
			return
		}
	}
	err := s.TeamsApp.Store.SetChannelAgent(r.Context(), org, in.TenantID, in.ChannelID, in.AgentID)
	if errors.Is(err, teamsapp.ErrNotLinked) {
		writeErr(w, http.StatusNotFound, "this tenant is not linked to your organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTeamsAppPackage — GET /api/v1/teamsapp/package: the app package to
// upload into Teams.
func (s *Server) handleTeamsAppPackage(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "no Teams app on this installation")
		return
	}
	base, ok := s.tokenLinkBase(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured (COVEY_SITE_URL)")
		return
	}
	zip, err := s.TeamsApp.Package(base, "covey")
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="covey-teams-app.zip"`)
	_, _ = w.Write(zip)
}
