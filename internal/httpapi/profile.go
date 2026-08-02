package httpapi

import (
	"net/http"
	"strings"
	"time"

	"covey/internal/identity"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/org"
)

// Selbstverwaltung des eigenen Kontos: Anzeigename, Profil (Funktion,
// GitLab-Username, Kontakt, Zuständigkeiten), Passwort, Sessions. Anders als
// die Admin-Endpunkte (admin.go) wirken diese Endpunkte nur auf den
// angemeldeten Principal — Rolle und Manager bleiben Admin-Sache.

// profilePatch sind die optionalen Profilfelder eines Update-Requests —
// geteilt zwischen Selbstverwaltung (PATCH /auth/me) und Admin-Endpunkt
// (PATCH /users/{id}). nil = unverändert; Werte werden getrimmt.
type profilePatch struct {
	JobTitle *string `json:"job_title"`
	// Identities: Plattform-Kennungen als Map system → kennung; nil =
	// unverändert, sonst vollständiger Ersatz (der Store normalisiert).
	Identities       map[string]string `json:"identities"`
	Phone            *string           `json:"phone"`
	Responsibilities *string           `json:"responsibilities"`
	// Custom: Werte der org-weit konfigurierten Profilfelder (profile_fields).
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
		Identities:       p.Identities, // normalisiert der Store beim Schreiben
		Phone:            strings.TrimSpace(p.Phone),
		Responsibilities: strings.TrimSpace(p.Responsibilities),
		Custom:           p.Custom,
	}
}

// handleMyProfile liefert den vollständigen eigenen Datensatz (inkl.
// Profilfelder) — der Principal aus der Session trägt nur die Login-Sicht.
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
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if in.DisplayName != nil && strings.TrimSpace(*in.DisplayName) == "" {
		writeErr(w, http.StatusBadRequest, "display_name darf nicht leer sein")
		return
	}
	upd := org.HumanUpdate{DisplayName: in.DisplayName}
	in.profilePatch.apply(&upd)
	if in.Password != nil {
		if len(*in.Password) < minPasswordLen {
			writeErr(w, http.StatusBadRequest, "passwort braucht mindestens 8 Zeichen")
			return
		}
		// Passwortwechsel nur mit Nachweis des aktuellen Passworts.
		if _, err := s.Identity.AuthenticateHuman(r.Context(), identity.Credentials{Email: p.Email, Password: in.CurrentPassword}); err != nil {
			writeErr(w, http.StatusForbidden, "aktuelles Passwort stimmt nicht")
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
	offen, err := s.sessions().List(r.Context(), p.ID)
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

// handleRevokeOtherSessions beendet alle Sessions des Nutzers außer der aktuellen.
func (s *Server) handleRevokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cur := ""
	if cookie, err := r.Cookie("covey_session"); err == nil {
		cur = hashToken(cookie.Value)
	}
	n, err := s.sessions().DeleteOthers(r.Context(), p.ID, cur)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": n})
}
