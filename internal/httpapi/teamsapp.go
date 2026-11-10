package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/benjaminLedel/covey-plugin-pack/teams"
	"github.com/google/uuid"

	"covey/internal/teamsapp"
	"covey/internal/teamsapp/teamsys"
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

	in, err := s.TeamsApp.Route(r.Context(), a)
	if err != nil {
		mapErr(w, err)
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
	outcome, err := s.Orch.HandleWebhook(r.Context(), *in.Agent, teamsys.Name, in.Event)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
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
	http.SetCookie(w, &http.Cookie{Name: teamsapp.LinkCookie, Value: nonce, Path: "/api/teamsapp/callback",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int((15 * time.Minute).Seconds())})
	writeJSON(w, http.StatusOK, map[string]string{"url": signIn})
}

// handleTeamsAppCallback — GET /api/teamsapp/callback: Microsoft's redirect
// after the sign-in. Answers with a redirect into the settings page either
// way, carrying the outcome.
func (s *Server) handleTeamsAppCallback(w http.ResponseWriter, r *http.Request) {
	back := func(outcome, detail string) {
		http.SetCookie(w, &http.Cookie{Name: teamsapp.LinkCookie, Value: "", Path: "/api/teamsapp/callback", MaxAge: -1})
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
