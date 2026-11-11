package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"covey/internal/identity"
	"covey/internal/sso"
)

// Single sign-on per organisation (#723). The sign-in itself is three public
// routes — discover, start, callback; the configuration hangs off the
// organisation and belongs to its org admin.

func (s *Server) ssoRedirectURI(ctx context.Context) (string, bool) {
	base, ok := s.tokenLinkBase(ctx)
	if !ok {
		return "", false
	}
	return base + "/api/sso/callback", true
}

// passwordRefused says whether an address must sign in through single
// sign-on. An error reading it counts as "no": the password check behind it
// still stands, and a database hiccup must not turn into a lockout.
func (s *Server) passwordRefused(ctx context.Context, email string) bool {
	if s.SSO == nil {
		return false
	}
	refused, err := s.SSO.PasswordRefused(ctx, email)
	if err != nil {
		s.Log.Warn("could not read whether single sign-on is required", "err", err)
		return false
	}
	return refused
}

const ssoRequiredMsg = "this address signs in with single sign-on"

// writeSSORequired answers a password move for an address that must use
// single sign-on. sso:true lets the sign-in page switch to the right button.
func writeSSORequired(w http.ResponseWriter) {
	writeJSON(w, http.StatusConflict, map[string]any{"error": ssoRequiredMsg, "sso": true})
}

// handleSSODiscover — GET /api/v1/public/sso/discover?email=… Where the
// address signs in. It says no more than the sign-in page shows anyway: that a
// domain uses single sign-on, and through which organisation.
func (s *Server) handleSSODiscover(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{"sso": false}
	if s.SSO == nil {
		writeJSON(w, http.StatusOK, out)
		return
	}
	route, ok, err := s.SSO.Discover(r.Context(), r.URL.Query().Get("email"))
	if err != nil {
		mapErr(w, err)
		return
	}
	if ok {
		out = map[string]any{"sso": true, "required": route.Required, "org_name": route.OrgName}
	}
	writeJSON(w, http.StatusOK, out)
}

// ssoFail sends the browser back to the sign-in page with a reason the page
// can name. The details go into the log, not into the address bar.
func (s *Server) ssoFail(w http.ResponseWriter, r *http.Request, code string, err error) {
	if err != nil {
		s.Log.Warn("single sign-on failed", "reason", code, "err", err)
	}
	http.Redirect(w, r, "/?sso_error="+url.QueryEscape(code), http.StatusSeeOther)
}

// handleSSOStart — GET /api/sso/start?email=… A browser navigation, not an API
// call: it ends at the provider.
func (s *Server) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	if s.SSO == nil {
		s.ssoFail(w, r, "unavailable", nil)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("email")))
	route, ok, err := s.SSO.Discover(r.Context(), email)
	if err != nil || !ok {
		s.ssoFail(w, r, "unknown_domain", err)
		return
	}
	redirect, ok := s.ssoRedirectURI(r.Context())
	if !ok {
		s.ssoFail(w, r, "unavailable", nil)
		return
	}
	authURL, cookie, err := s.SSO.Begin(r.Context(), route.OrgID, redirect, email)
	if err != nil {
		s.ssoFail(w, r, "provider", err)
		return
	}
	// SameSite=Lax, unlike the session: the provider's redirect back is a
	// top-level navigation from a foreign site, and the cookie has to ride
	// along. Path-scoped to the callback, short-lived, sealed.
	http.SetCookie(w, &http.Cookie{Name: sso.FlowCookie, Value: cookie, Path: "/api/sso/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(sso.FlowTTL.Seconds())})
	http.Redirect(w, r, authURL, http.StatusFound)
}

// handleSSOCallback — GET /api/sso/callback. The provider sends the browser
// back here; the session starts on the seat in the organisation signed in to.
func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	if s.SSO == nil {
		s.ssoFail(w, r, "unavailable", nil)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: sso.FlowCookie, Value: "", Path: "/api/sso/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	q := r.URL.Query()
	if e := q.Get("error"); e != "" {
		s.ssoFail(w, r, "provider", errors.New(e+": "+q.Get("error_description")))
		return
	}
	c, err := r.Cookie(sso.FlowCookie)
	if err != nil {
		s.ssoFail(w, r, "expired", sso.ErrFlow)
		return
	}
	redirect, ok := s.ssoRedirectURI(r.Context())
	if !ok {
		s.ssoFail(w, r, "unavailable", nil)
		return
	}
	res, err := s.SSO.Finish(r.Context(), c.Value, q.Get("state"), q.Get("code"), redirect)
	switch {
	case errors.Is(err, sso.ErrFlow):
		s.ssoFail(w, r, "expired", err)
		return
	case errors.Is(err, sso.ErrForeignAddress):
		s.ssoFail(w, r, "foreign_address", err)
		return
	case err != nil:
		s.ssoFail(w, r, "failed", err)
		return
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		s.ssoFail(w, r, "failed", err)
		return
	}
	token := hex.EncodeToString(buf)
	if err := s.sessions().Create(r.Context(), hashToken(token), res.AccountID, res.SeatID,
		time.Now().Add(s.SessionTTL)); err != nil {
		s.ssoFail(w, r, "failed", err)
		return
	}
	s.Log.Info("single sign-on", "org", res.OrgID, "account", res.AccountID, "linked", res.Linked, "created", res.Created)
	s.setSessionCookie(w, token, int(s.SessionTTL.Seconds()))
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

// --- configuration (org admin) ---

// handleGetOrgSSO — GET /api/v1/org/sso.
func (s *Server) handleGetOrgSSO(w http.ResponseWriter, r *http.Request) {
	if s.SSO == nil {
		writeJSON(w, http.StatusOK, map[string]any{"available": false})
		return
	}
	p := principalFrom(r)
	out := map[string]any{"available": true}
	if uri, ok := s.ssoRedirectURI(r.Context()); ok {
		out["redirect_uri"] = uri
	}
	cfg, err := s.SSO.Get(r.Context(), p.OrgID)
	switch {
	case err == nil:
		out["config"] = cfg
		linked, err := s.SSO.Linked(r.Context(), p.OrgID, p.AccountID)
		if err != nil {
			mapErr(w, err)
			return
		}
		out["signed_in_via_sso"] = linked
	case errors.Is(err, sso.ErrNotConfigured):
	default:
		mapErr(w, err)
		return
	}
	domains, err := s.SSO.Domains(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out["domains"] = domains
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) ssoAvailable(w http.ResponseWriter) bool {
	if s.SSO == nil {
		writeErr(w, http.StatusNotFound, "single sign-on is not available on this installation")
		return false
	}
	return true
}

// handleSaveOrgSSO — PUT /api/v1/org/sso.
func (s *Server) handleSaveOrgSSO(w http.ResponseWriter, r *http.Request) {
	if !s.ssoAvailable(w) {
		return
	}
	var in sso.Input
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "expected {issuer, client_id, secret, default_role}")
		return
	}
	in.DefaultRole = identity.NormalizeRole(strings.TrimSpace(in.DefaultRole))
	if in.DefaultRole == "" {
		in.DefaultRole = identity.RoleAuditor
	}
	if !validRoles[in.DefaultRole] {
		writeErr(w, http.StatusBadRequest, "unknown role "+in.DefaultRole)
		return
	}
	cfg, err := s.SSO.Save(r.Context(), principalFrom(r).OrgID, in)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

// handleDeleteOrgSSO — DELETE /api/v1/org/sso.
func (s *Server) handleDeleteOrgSSO(w http.ResponseWriter, r *http.Request) {
	if !s.ssoAvailable(w) {
		return
	}
	if err := s.SSO.Delete(r.Context(), principalFrom(r).OrgID); errors.Is(err, sso.ErrNotConfigured) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	} else if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) currentSessionHash(r *http.Request) string {
	if c, err := r.Cookie("covey_session"); err == nil {
		return hashToken(c.Value)
	}
	return ""
}

// handleSetOrgSSORequired — PUT /api/v1/org/sso/required {required}. Session
// only: the proof that the admin signed in through the provider is the
// session, and a key has none.
func (s *Server) handleSetOrgSSORequired(w http.ResponseWriter, r *http.Request) {
	if !s.ssoAvailable(w) {
		return
	}
	var in struct {
		Required bool `json:"required"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "expected {required}")
		return
	}
	p := principalFrom(r)
	n, err := s.SSO.SetRequired(r.Context(), p.OrgID, p.AccountID, in.Required, s.currentSessionHash(r))
	switch {
	case errors.Is(err, sso.ErrNotConfigured):
		writeErr(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, sso.ErrNotLinked):
		writeErr(w, http.StatusConflict, err.Error())
		return
	case err != nil:
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"required": in.Required, "rewritten": n})
}

// handleAddOrgSSODomain — POST /api/v1/org/sso/domains {domain}.
func (s *Server) handleAddOrgSSODomain(w http.ResponseWriter, r *http.Request) {
	if !s.ssoAvailable(w) {
		return
	}
	var in struct {
		Domain string `json:"domain"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "expected {domain}")
		return
	}
	d, err := s.SSO.AddDomain(r.Context(), principalFrom(r).OrgID, in.Domain)
	if errors.Is(err, sso.ErrDomainTaken) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

// handleVerifyOrgSSODomain — POST /api/v1/org/sso/domains/{domain}/verify.
func (s *Server) handleVerifyOrgSSODomain(w http.ResponseWriter, r *http.Request) {
	if !s.ssoAvailable(w) {
		return
	}
	d, err := s.SSO.VerifyDomain(r.Context(), principalFrom(r).OrgID, r.PathValue("domain"), s.currentSessionHash(r))
	switch {
	case errors.Is(err, sso.ErrNoDomain):
		writeErr(w, http.StatusNotFound, err.Error())
		return
	case errors.Is(err, sso.ErrNotVerified):
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	case err != nil:
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

// handleDeleteOrgSSODomain — DELETE /api/v1/org/sso/domains/{domain}.
func (s *Server) handleDeleteOrgSSODomain(w http.ResponseWriter, r *http.Request) {
	if !s.ssoAvailable(w) {
		return
	}
	if err := s.SSO.RemoveDomain(r.Context(), principalFrom(r).OrgID, r.PathValue("domain")); errors.Is(err, sso.ErrNoDomain) {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	} else if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
