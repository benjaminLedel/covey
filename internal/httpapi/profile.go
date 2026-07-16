package httpapi

import (
	"net/http"
	"strings"
	"time"

	"covey/internal/identity"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/org"
)

// Selbstverwaltung des eigenen Kontos: Anzeigename, Passwort, Sessions.
// Anders als die Admin-Endpunkte (admin.go) wirken diese Endpunkte nur auf
// den angemeldeten Principal — Rolle und Manager bleiben Admin-Sache.

func (s *Server) handleUpdateMe(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		DisplayName     *string `json:"display_name"`
		Password        *string `json:"password"`
		CurrentPassword string  `json:"current_password"`
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
	rows, err := s.Pool.Query(r.Context(), `SELECT token_hash, created_at, expires_at
		FROM http_sessions WHERE human_id=$1 AND expires_at > now() ORDER BY created_at DESC`, p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	defer rows.Close()
	list := []sessionInfo{}
	for rows.Next() {
		var hash string
		var si sessionInfo
		if err := rows.Scan(&hash, &si.CreatedAt, &si.ExpiresAt); err != nil {
			mapErr(w, err)
			return
		}
		si.Current = hash == cur
		list = append(list, si)
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
	tag, err := s.Pool.Exec(r.Context(), `DELETE FROM http_sessions WHERE human_id=$1 AND token_hash <> $2`, p.ID, cur)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"revoked": tag.RowsAffected()})
}
