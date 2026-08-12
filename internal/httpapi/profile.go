package httpapi

import (
	"net/http"
	"strings"
	"time"

	"covey/internal/identity"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/org"
)

// Self-administration of one's own account: display name, profile (job title,
// GitLab username, contact, responsibilities), password, sessions. Unlike the
// admin endpoints (admin.go) these endpoints act only on the signed-in
// principal — role and manager stay an admin matter.

// profilePatch holds the optional profile fields of an update request — shared
// between self-administration (PATCH /auth/me) and the admin endpoint
// (PATCH /users/{id}). nil = unchanged; values are trimmed.
type profilePatch struct {
	JobTitle *string `json:"job_title"`
	// Identities: platform handles as a map system → handle; nil = unchanged,
	// otherwise a complete replacement (the store normalizes).
	Identities       map[string]string `json:"identities"`
	Phone            *string           `json:"phone"`
	Responsibilities *string           `json:"responsibilities"`
	// Custom: values of the profile fields configured org-wide (profile_fields).
	Custom map[string]string `json:"custom"`
}

func (pp profilePatch) apply(upd *org.HumanUpdate) {
	upd.JobTitle = trimPtr(pp.JobTitle)
	upd.Identities = pp.Identities
	upd.Phone = trimPtr(pp.Phone)
	upd.Responsibilities = trimPtr(pp.Responsibilities)
	upd.Custom = pp.Custom
}

func trimPtr(s *string) *string {
	if s == nil {
		return nil
	}
	t := strings.TrimSpace(*s)
	return &t
}

func trimProfile(p org.Profile) org.Profile {
	return org.Profile{
		JobTitle:         strings.TrimSpace(p.JobTitle),
		Identities:       p.Identities, // the store normalizes on write
		Phone:            strings.TrimSpace(p.Phone),
		Responsibilities: strings.TrimSpace(p.Responsibilities),
		Custom:           p.Custom,
	}
}

// handleMyProfile returns one's own complete record (including the profile
// fields) — the principal from the session carries only the login view.
func (s *Server) handleMyProfile(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	h, err := s.Org.GetHuman(r.Context(), p.OrgID, p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		DisplayName     *string `json:"display_name"`
		Password        *string `json:"password"`
		CurrentPassword string  `json:"current_password"`
		profilePatch
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) == "" {
		writeErr(w, http.StatusBadRequest, "display_name must not be empty")
		return
	}
	upd := org.HumanUpdate{DisplayName: in.DisplayName}
	in.profilePatch.apply(&upd)
	if in.Password != nil {
		if len(*in.Password) < minPasswordLen {
			writeErr(w, http.StatusBadRequest, "password needs at least 8 characters")
			return
		}
		// Change the password only against proof of the current one.
		if _, err := s.Identity.AuthenticateHuman(r.Context(), identity.Credentials{Email: p.Email, Password: in.CurrentPassword}); err != nil {
			writeErr(w, http.StatusForbidden, "current password is not correct")
			return
		}
		hash, err := identbuiltin.HashPassword(*in.Password)
		if err != nil {
			mapErr(w, err)
			return
		}
		upd.PasswordHash = &hash
	}
	h, err := s.Org.UpdateHuman(r.Context(), p.OrgID, p.ID, upd)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

type sessionInfo struct {
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cur := ""
	if cookie, err := r.Cookie("covey_session"); err == nil {
		cur = hashToken(cookie.Value)
	}
	offen, err := s.sessions().List(r.Context(), p.AccountID)
	if err != nil {
		mapErr(w, err)
		return
	}
	list := []sessionInfo{}
	for _, sitz := range offen {
		list = append(list, sessionInfo{
			CreatedAt: sitz.CreatedAt,
			ExpiresAt: sitz.ExpiresAt,
			Current:   sitz.TokenHash == cur,
		})
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRevokeOtherSessions ends all of the user's sessions except the current one.
func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cur := ""
	if cookie, err := r.Cookie("covey_session"); err == nil {
		cur = hashToken(cookie.Value)
	}
	n, err := s.sessions().DeleteOthers(r.Context(), p.AccountID, cur)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}
