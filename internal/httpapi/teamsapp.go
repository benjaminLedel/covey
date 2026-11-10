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

// An organisation's Teams bot (#710, #715): its messaging endpoint, its
// configuration, and what the organisation sees of it.

// handleTeamsAppWebhook — POST /api/webhooks/teamsapp/{bot}, one bot's
// messaging endpoint. The token must be the Bot Framework's for THIS bot's
// app ID, and its serviceurl claim must name the host the body names, since
// that host is stored and later gets the bot's credential.
func (s *Server) handleTeamsAppWebhook(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "Teams bots are not available on this installation")
		return
	}
	id, err := uuid.Parse(r.PathValue("bot"))
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such bot")
		return
	}
	bot, err := s.TeamsApp.BotByID(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no such bot")
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
	if err := s.TeamsApp.Verify(bot.AppID, r.Header.Get("Authorization"), a.ServiceURL); err != nil {
		s.Log.Warn("teams bot: delivery refused", "bot", bot.ID, "err", err)
		writeErr(w, http.StatusUnauthorized, "invalid token")
		return
	}
	if !s.webhookLimiter.allow("teamsapp:"+bot.ID.String(), time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests, "too many messages for this bot — please redeliver later")
		return
	}

	if strings.EqualFold(a.Type, "invoke") && a.Name == "signin/tokenExchange" {
		s.teamsAppTokenExchange(w, r, bot, a, body)
		return
	}

	in, err := s.TeamsApp.Route(r.Context(), bot, a, body)
	if err != nil {
		mapErr(w, err)
		return
	}
	if in.SignIn {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		base, _ := s.tokenLinkBase(ctx)
		if err := s.TeamsApp.AskSignIn(ctx, bot, a, base, s.personLink(base, bot, in.Person)); err != nil {
			s.Log.Warn("teams bot: sign-in card not sent", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]string{"outcome": "sign_in_requested"})
		return
	}
	if in.Answer != "" {
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		if err := s.TeamsApp.Say(ctx, bot, a, in.Answer); err != nil {
			s.Log.Warn("teams bot: answer not sent", "err", err)
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

func (s *Server) personLink(base string, bot teamsapp.Bot, oid string) string {
	return base + "/api/teamsapp/signin?t=" + url.QueryEscape(teamsapp.PersonLinkToken(bot, oid))
}

// teamsAppHandOver gives a routed message to its agent and records, before
// the run can start, whose message the task is working on (#713).
func (s *Server) teamsAppHandOver(ctx context.Context, in teamsapp.Inbound) (string, error) {
	var record func(context.Context, uuid.UUID) error
	if s.TeamsUser != nil && in.Person != "" {
		record = func(ctx context.Context, task uuid.UUID) error {
			return s.TeamsUser.SetTaskPerson(ctx, task, in.Bot.ID, in.Person)
		}
	}
	outcome, _, err := s.Orch.HandleWebhookTask(ctx, *in.Agent, teamsys.Name, in.Event, record)
	return outcome, err
}

// teamsAppTokenExchange answers Teams' signin/tokenExchange invoke: the
// token of the person who saw the sign-in card, exchanged on their behalf
// and stored; the kept message goes to the agent. A failed exchange is
// answered 412, which tells Teams to fall back — and the bot offers the
// browser sign-in.
func (s *Server) teamsAppTokenExchange(w http.ResponseWriter, r *http.Request, bot teamsapp.Bot, a teams.Activity, body []byte) {
	var in struct {
		Value struct {
			ID             string `json:"id"`
			ConnectionName string `json:"connectionName"`
			Token          string `json:"token"`
		} `json:"value"`
	}
	_ = json.Unmarshal(body, &in)
	oid := a.From.AADObjectID
	fail := func(detail string) {
		writeJSON(w, http.StatusPreconditionFailed, map[string]string{
			"id": in.Value.ID, "connectionName": in.Value.ConnectionName, "failureDetail": detail})
	}
	if s.TeamsUser == nil || oid == "" || in.Value.Token == "" || !strings.EqualFold(a.TenantID(), bot.TenantID) {
		fail("single sign-on is not available here")
		return
	}
	ref, err := s.TeamsApp.DelegationByBot(r.Context(), bot.ID)
	if err == nil {
		err = s.TeamsUser.Exchange(r.Context(), ref, oid, in.Value.Token)
	}
	if err != nil {
		s.Log.Info("teams bot: token exchange failed, offering the browser sign-in", "err", err)
		fail("exchange failed")
		base, _ := s.tokenLinkBase(r.Context())
		go func(ctx context.Context) {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := s.TeamsApp.OfferLink(ctx, bot, a, s.personLink(base, bot, oid)); err != nil {
				s.Log.Warn("teams bot: sign-in link not sent", "err", err)
			}
		}(context.WithoutCancel(r.Context()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"id": in.Value.ID, "connectionName": in.Value.ConnectionName})
	go s.teamsAppReplay(context.WithoutCancel(r.Context()), bot.ID, oid)
}

// teamsAppReplay hands the message kept during sign-in to its agent.
func (s *Server) teamsAppReplay(ctx context.Context, botID uuid.UUID, oid string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	bot, err := s.TeamsApp.BotByID(ctx, botID)
	if err != nil {
		return
	}
	raw, ok := s.TeamsUser.TakePending(ctx, botID, oid)
	if !ok {
		return
	}
	a, err := teams.ParseWebhook(raw)
	if err != nil {
		return
	}
	in, err := s.TeamsApp.Route(ctx, bot, a, raw)
	if err != nil {
		s.Log.Warn("teams bot: kept message not routed", "err", err)
		return
	}
	switch {
	case in.Agent != nil:
		if _, err := s.teamsAppHandOver(ctx, in); err != nil {
			s.Log.Warn("teams bot: kept message not handed over", "err", err)
		}
	case in.Answer != "":
		_ = s.TeamsApp.Say(ctx, bot, a, in.Answer)
	}
}

const personCookie = teamsuser.Cookie + "_person"

// handleTeamsAppPersonSignIn — GET /api/teamsapp/signin?t=…: the browser
// sign-in the bot offers. Sets the binding cookie and goes to Microsoft.
func (s *Server) handleTeamsAppPersonSignIn(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil || s.TeamsUser == nil {
		writeErr(w, http.StatusNotFound, "not available on this installation")
		return
	}
	bot, oid, err := s.TeamsApp.CheckPersonLink(r.Context(), r.URL.Query().Get("t"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	base, ok := s.tokenLinkBase(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured")
		return
	}
	ref, err := s.TeamsApp.DelegationByBot(r.Context(), bot.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	signIn, nonce, err := s.TeamsUser.StartPersonSignIn(ref, oid, base+"/api/teamsapp/person-callback")
	if err != nil {
		mapErr(w, err)
		return
	}
	s.flowCookie(w, personCookie, nonce, "/api/teamsapp/person-callback", int(msauth.TTL.Seconds()))
	http.Redirect(w, r, signIn, http.StatusSeeOther)
}

// handleTeamsAppPersonCallback — GET /api/teamsapp/person-callback.
func (s *Server) handleTeamsAppPersonCallback(w http.ResponseWriter, r *http.Request) {
	page := func(status int, text string) {
		s.flowCookie(w, personCookie, "", "/api/teamsapp/person-callback", -1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(status)
		_, _ = w.Write([]byte("<!doctype html><meta charset=utf-8><meta name=viewport content='width=device-width'>" +
			"<title>covey</title><body style='font-family:system-ui;max-width:32rem;margin:4rem auto;padding:0 1rem'><p>" +
			html.EscapeString(text) + "</p></body>"))
	}
	if s.TeamsApp == nil || s.TeamsUser == nil {
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
	if c, err := r.Cookie(personCookie); err == nil {
		nonce = c.Value
	}
	bot, oid, err := s.TeamsUser.FinishPersonSignIn(r.Context(), q.Get("state"), nonce, q.Get("code"), base+"/api/teamsapp/person-callback")
	if err != nil {
		s.Log.Warn("teams bot: person sign-in failed", "err", err)
		page(http.StatusBadRequest, "Signing in did not work: "+err.Error())
		return
	}
	go s.teamsAppReplay(context.WithoutCancel(r.Context()), bot, oid)
	page(http.StatusOK, "You are signed in. Close this window and go back to Teams — your message is being handled.")
}

// handleTeamsAppStatus — GET /api/v1/teamsapp: the organisation's bot
// (never its secrets), what to enter in Azure, the teams it is in and the
// channel defaults.
func (s *Server) handleTeamsAppStatus(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	base, siteOK := s.tokenLinkBase(r.Context())
	out := map[string]any{"available": true, "site_configured": siteOK}
	bot, err := s.TeamsApp.BotOfOrg(r.Context(), principalFrom(r).OrgID)
	if errors.Is(err, teamsapp.ErrNoBot) {
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	teamList, err := s.TeamsApp.Store.Teams(r.Context(), bot.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	defaults, err := s.TeamsApp.Store.ChannelAgents(r.Context(), bot.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out["bot"] = bot
	out["teams"] = teamList
	out["channel_agents"] = defaults
	if siteOK {
		out["endpoint"] = base + "/api/webhooks/teamsapp/" + bot.ID.String()
		out["redirect_uris"] = []string{base + "/api/teamsapp/person-callback", base + "/api/teamsuser/callback"}
		out["sso_resource"] = bot.SSOResource(base)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleTeamsAppSaveBot — PUT /api/v1/teamsapp/bot (org admin).
func (s *Server) handleTeamsAppSaveBot(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "Teams bots are not available on this installation")
		return
	}
	var in teamsapp.BotInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "expected {name, app_id, tenant_id, secret, sso_connection, deleg_app_id, deleg_secret}")
		return
	}
	bot, err := s.TeamsApp.SaveBot(r.Context(), principalFrom(r).OrgID, in)
	if errors.Is(err, teamsapp.ErrAppTaken) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, bot)
}

// handleTeamsAppDeleteBot — DELETE /api/v1/teamsapp/bot (org admin).
func (s *Server) handleTeamsAppDeleteBot(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "Teams bots are not available on this installation")
		return
	}
	if err := s.TeamsApp.DeleteBot(r.Context(), principalFrom(r).OrgID); errors.Is(err, teamsapp.ErrNoBot) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	} else if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTeamsAppChannels — GET /api/v1/teamsapp/channels, fresh from Teams.
func (s *Server) handleTeamsAppChannels(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "Teams bots are not available on this installation")
		return
	}
	list, err := s.TeamsApp.Channels(r.Context(), principalFrom(r).OrgID)
	if errors.Is(err, teamsapp.ErrNoBot) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleTeamsAppChannelAgent — PUT /api/v1/teamsapp/channel-agent
// {channel_id, agent_id|null}.
func (s *Server) handleTeamsAppChannelAgent(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "Teams bots are not available on this installation")
		return
	}
	var in struct {
		ChannelID string     `json:"channel_id"`
		AgentID   *uuid.UUID `json:"agent_id"`
	}
	if err := readJSON(r, &in); err != nil || in.ChannelID == "" {
		writeErr(w, http.StatusBadRequest, `expected {"channel_id","agent_id"}`)
		return
	}
	org := principalFrom(r).OrgID
	bot, err := s.TeamsApp.BotOfOrg(r.Context(), org)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if in.AgentID != nil {
		a, err := s.Registry.Get(r.Context(), *in.AgentID)
		if err != nil || a.OrgID != org {
			writeErr(w, http.StatusNotFound, "no such agent in your organisation")
			return
		}
	}
	if err := s.TeamsApp.Store.SetChannelAgent(r.Context(), bot.ID, in.ChannelID, in.AgentID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleTeamsAppPackage — GET /api/v1/teamsapp/package: the organisation's
// app package to upload into Teams.
func (s *Server) handleTeamsAppPackage(w http.ResponseWriter, r *http.Request) {
	if s.TeamsApp == nil {
		writeErr(w, http.StatusNotFound, "Teams bots are not available on this installation")
		return
	}
	bot, err := s.TeamsApp.BotOfOrg(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	base, ok := s.tokenLinkBase(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured")
		return
	}
	zip, err := s.TeamsApp.Package(bot, base)
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="covey-teams-app.zip"`)
	_, _ = w.Write(zip)
}
