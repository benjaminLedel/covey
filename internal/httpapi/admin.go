package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/identity"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/org"
)

// Admin-Endpunkte: Benutzer- und Mandanten-Verwaltung, nur platform_admin.
// Passwort-Hashing ist builtin-spezifisch (Argon2id) — mit dem OIDC-Ausbau
// verwaltet der externe Provider die Logins und diese Endpunkte schrumpfen.

var validRoles = map[string]bool{
	identity.RolePlatformAdmin: true,
	identity.RoleAgentOwner:    true,
	identity.RoleSecurity:      true,
	identity.RoleAuditor:       true,
	identity.RoleControlling:   true,
}

const minPasswordLen = 8

// --- Benutzer (gescopt auf die Organisation des Aufrufers) ---

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Org.ListHumans(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []org.Human{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Email       string `json:"email"`
		DisplayName string `json:"display_name"`
		Role        string `json:"role"`
		Password    string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(in.Email, "@") || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "email und display_name sind Pflicht")
		return
	}
	if !validRoles[in.Role] {
		writeErr(w, http.StatusBadRequest, "unbekannte rolle "+in.Role)
		return
	}
	if len(in.Password) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "passwort braucht mindestens 8 Zeichen")
		return
	}
	hash, err := identbuiltin.HashPassword(in.Password)
	if err != nil {
		mapErr(w, err)
		return
	}
	h, err := s.Org.CreateHuman(r.Context(), p.OrgID, in.Email, in.DisplayName, in.Role, hash)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Password    *string `json:"password"`
		// ManagerID: nil = unverändert, "" = Zuordnung lösen, sonst UUID.
		ManagerID *string `json:"manager_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	upd := org.HumanUpdate{DisplayName: in.DisplayName, Role: in.Role}
	if in.ManagerID != nil {
		nu := uuid.NullUUID{}
		if *in.ManagerID != "" {
			mid, err := uuid.Parse(*in.ManagerID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "ungültige manager_id")
				return
			}
			nu = uuid.NullUUID{UUID: mid, Valid: true}
		}
		upd.ManagerID = &nu
	}
	if in.DisplayName != nil && *in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "display_name darf nicht leer sein")
		return
	}
	if in.Role != nil && !validRoles[*in.Role] {
		writeErr(w, http.StatusBadRequest, "unbekannte rolle "+*in.Role)
		return
	}
	if in.Password != nil {
		if len(*in.Password) < minPasswordLen {
			writeErr(w, http.StatusBadRequest, "passwort braucht mindestens 8 Zeichen")
			return
		}
		hash, err := identbuiltin.HashPassword(*in.Password)
		if err != nil {
			mapErr(w, err)
			return
		}
		upd.PasswordHash = &hash
	}
	h, err := s.Org.UpdateHuman(r.Context(), p.OrgID, id, upd)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	if id == p.ID {
		writeErr(w, http.StatusConflict, "das eigene Konto kann nicht gelöscht werden")
		return
	}
	if err := s.Org.DeleteHuman(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Mandanten (Organisationen) ---

func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	list, err := s.Org.ListOrgs(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []org.Organization{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name          string `json:"name"`
		AdminEmail    string `json:"admin_email"`
		AdminName     string `json:"admin_name"`
		AdminPassword string `json:"admin_password"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name ist Pflicht")
		return
	}
	in.AdminEmail = strings.ToLower(strings.TrimSpace(in.AdminEmail))
	if !strings.Contains(in.AdminEmail, "@") || in.AdminName == "" {
		writeErr(w, http.StatusBadRequest, "admin_email und admin_name sind Pflicht (Organisation ohne Admin wäre unerreichbar)")
		return
	}
	if len(in.AdminPassword) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "admin_password braucht mindestens 8 Zeichen")
		return
	}
	hash, err := identbuiltin.HashPassword(in.AdminPassword)
	if err != nil {
		mapErr(w, err)
		return
	}
	o, err := s.Org.CreateOrg(r.Context(), strings.TrimSpace(in.Name), in.AdminEmail, in.AdminName, hash)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, o)
}

func (s *Server) handleUpdateOrg(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name ist Pflicht")
		return
	}
	if err := s.Org.RenameOrg(r.Context(), id, strings.TrimSpace(in.Name)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteOrg(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	if id == p.OrgID {
		writeErr(w, http.StatusConflict, "die eigene Organisation kann nicht gelöscht werden")
		return
	}
	if err := s.Org.DeleteOrg(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
