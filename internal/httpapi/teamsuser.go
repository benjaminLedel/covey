package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/google/uuid"

	"covey/internal/identity"
	"covey/internal/msauth"
	"covey/internal/teamsuser"
)

// Teams as a person (#712): a person grants an agent their own Teams.

func (s *Server) teamsUserCallback(ctx context.Context) (string, bool) {
	base, ok := s.tokenLinkBase(ctx)
	if !ok {
		return "", false
	}
	return base + "/api/teamsuser/callback", true
}

// handleTeamsUserStatus — GET /api/v1/teamsuser.
func (s *Server) handleTeamsUserStatus(w http.ResponseWriter, r *http.Request) {
	if s.TeamsUser == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	list, err := s.TeamsUser.List(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	_, ok := s.teamsUserCallback(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"available": s.TeamsUser.OwnApp(), "sso": s.TeamsUser.SSOEnabled(), "can_delegate": ok, "delegations": list, "me": principalFrom(r).ID})
}

// handleTeamsUserDelegate — POST /api/v1/teamsuser/delegate {agent_id}: the
// signed-in person starts granting their Teams to an agent of their
// organisation.
func (s *Server) handleTeamsUserDelegate(w http.ResponseWriter, r *http.Request) {
	if s.TeamsUser == nil {
		writeErr(w, http.StatusNotFound, "Teams as a person is not offered on this installation")
		return
	}
	var in struct {
		AgentID uuid.UUID `json:"agent_id"`
	}
	if err := readJSON(r, &in); err != nil || in.AgentID == uuid.Nil {
		writeErr(w, http.StatusBadRequest, `expected {"agent_id"}`)
		return
	}
	p := principalFrom(r)
	a, err := s.Registry.Get(r.Context(), in.AgentID)
	if err != nil || a.OrgID != p.OrgID {
		writeErr(w, http.StatusNotFound, "no such agent in your organisation")
		return
	}
	callback, ok := s.teamsUserCallback(r.Context())
	if !ok {
		writeErr(w, http.StatusServiceUnavailable, "no site address configured (COVEY_SITE_URL) — Microsoft can only return to a registered address")
		return
	}
	signIn, nonce, err := s.TeamsUser.Start(p.OrgID, a.ID, p.ID, callback)
	if err != nil {
		mapErr(w, err)
		return
	}
	s.flowCookie(w, teamsuser.Cookie, nonce, "/api/teamsuser/callback", int(msauth.TTL.Seconds()))
	writeJSON(w, http.StatusOK, map[string]string{"url": signIn})
}

// handleTeamsUserCallback — GET /api/teamsuser/callback.
func (s *Server) handleTeamsUserCallback(w http.ResponseWriter, r *http.Request) {
	back := func(outcome, detail string) {
		s.flowCookie(w, teamsuser.Cookie, "", "/api/teamsuser/callback", -1)
		q := url.Values{"teamsuser": {outcome}}
		if detail != "" {
			q.Set("detail", detail)
		}
		http.Redirect(w, r, "/targets?"+q.Encode(), http.StatusSeeOther)
	}
	if s.TeamsUser == nil {
		back("error", "not offered on this installation")
		return
	}
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		back("error", q.Get("error_description"))
		return
	}
	callback, ok := s.teamsUserCallback(r.Context())
	if !ok {
		back("error", "no site address configured")
		return
	}
	nonce := ""
	if c, err := r.Cookie(teamsuser.Cookie); err == nil {
		nonce = c.Value
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	agent, who, err := s.TeamsUser.Finish(ctx, q.Get("state"), nonce, q.Get("code"), callback)
	if err != nil {
		s.Log.Warn("teams delegation failed", "err", err)
		back("error", err.Error())
		return
	}
	s.Log.Info("teams delegated", "agent", agent, "ms_user", who)
	back("delegated", "")
}

// handleTeamsUserRevoke — DELETE /api/v1/teamsuser/delegations/{agent}: the
// person who granted it, or an org admin.
func (s *Server) handleTeamsUserRevoke(w http.ResponseWriter, r *http.Request) {
	if s.TeamsUser == nil {
		writeErr(w, http.StatusNotFound, "Teams as a person is not offered on this installation")
		return
	}
	agent, err := uuid.Parse(r.PathValue("agent"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid agent id")
		return
	}
	p := principalFrom(r)
	by, err := s.TeamsUser.Delegator(r.Context(), p.OrgID, agent)
	if errors.Is(err, teamsuser.ErrNoDelegation) {
		writeErr(w, http.StatusNotFound, "this agent has no delegation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	if p.Role != identity.RoleOrgAdmin && (by == nil || *by != p.ID) {
		writeErr(w, http.StatusForbidden, "only the person who connected their Teams, or an org admin, can disconnect it")
		return
	}
	if err := s.TeamsUser.Revoke(r.Context(), p.OrgID, agent); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// flowCookie sets (or, with maxAge -1, clears) the cookie that binds a
// Microsoft sign-in to the browser that started it. Always HttpOnly, Secure
// as the session cookie is, and Lax rather than Strict: the callback is a
// top-level navigation back from Microsoft, which a Strict cookie would not
// travel with.
func (s *Server) flowCookie(w http.ResponseWriter, name, value, path string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: path,
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: maxAge})
}
