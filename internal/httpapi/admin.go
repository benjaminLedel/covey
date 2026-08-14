package httpapi

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/identity"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/org"
)

// Admin endpoints: user and tenant management, org_admin only.
// Password hashing is builtin-specific (Argon2id) — once OIDC is built out the
// external provider manages the logins and these endpoints shrink.

var validRoles = map[string]bool{
	identity.RoleOrgAdmin:    true,
	identity.RoleAgentOwner:  true,
	identity.RoleSecurity:    true,
	identity.RoleAuditor:     true,
	identity.RoleControlling: true,
}

const minPasswordLen = 8

// --- Users (scoped to the caller's organization) ---

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
		org.Profile
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	if !strings.Contains(in.Email, "@") || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "email and display_name are required")
		return
	}
	// A caller written against the API before migration 0061 still sends
	// "org_admin". That is the same role under its old name, not an
	// unknown one — accept it and store the current name.
	in.Role = identity.NormalizeRole(in.Role)
	if !validRoles[in.Role] {
		writeErr(w, http.StatusBadRequest, "unknown role "+in.Role)
		return
	}
	if len(in.Password) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "password needs at least 8 characters")
		return
	}
	hash, err := identbuiltin.HashPassword(in.Password)
	if err != nil {
		mapErr(w, err)
		return
	}
	h, err := s.Org.CreateHuman(r.Context(), p.OrgID, in.Email, in.DisplayName, in.Role, hash, trimProfile(in.Profile))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, h)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		DisplayName *string `json:"display_name"`
		Role        *string `json:"role"`
		Password    *string `json:"password"`
		// ManagerID: nil = unchanged, "" = detach, otherwise a UUID.
		ManagerID *string `json:"manager_id"`
		profilePatch
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if in.Role != nil {
		normalized := identity.NormalizeRole(*in.Role)
		in.Role = &normalized
	}
	upd := org.HumanUpdate{DisplayName: in.DisplayName, Role: in.Role}
	in.profilePatch.apply(&upd)
	if in.ManagerID != nil {
		nu := uuid.NullUUID{}
		if *in.ManagerID != "" {
			mid, err := uuid.Parse(*in.ManagerID)
			if err != nil {
				writeErr(w, http.StatusBadRequest, "invalid manager_id")
				return
			}
			nu = uuid.NullUUID{UUID: mid, Valid: true}
		}
		upd.ManagerID = &nu
	}
	if in.DisplayName != nil && *in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "display_name must not be empty")
		return
	}
	if in.Role != nil && !validRoles[*in.Role] {
		writeErr(w, http.StatusBadRequest, "unknown role "+*in.Role)
		return
	}
	if in.Password != nil {
		if len(*in.Password) < minPasswordLen {
			writeErr(w, http.StatusBadRequest, "password needs at least 8 characters")
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
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	if id == p.ID {
		writeErr(w, http.StatusConflict, "your own account cannot be deleted")
		return
	}
	if err := s.Org.DeleteHuman(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// --- Tenants (organizations) ---

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
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	in.AdminEmail = strings.ToLower(strings.TrimSpace(in.AdminEmail))
	if !strings.Contains(in.AdminEmail, "@") || in.AdminName == "" {
		writeErr(w, http.StatusBadRequest, "admin_email and admin_name are required (an organization without an admin would be unreachable)")
		return
	}
	if len(in.AdminPassword) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "admin_password needs at least 8 characters")
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
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Name string `json:"name"`
		// Description is optional and may be cleared — hence a pointer: an
		// absent field must not silently wipe what somebody wrote.
		Description *string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, http.StatusBadRequest, "name is required")
		return
	}
	if err := s.Org.RenameOrg(r.Context(), id, strings.TrimSpace(in.Name)); err != nil {
		mapErr(w, err)
		return
	}
	if in.Description != nil {
		if err := s.Org.SetOrgDescription(r.Context(), id, strings.TrimSpace(*in.Description)); err != nil {
			mapErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleGetOwnOrg — the signed-in human's own organisation: name and what it
// does. Readable by every role (it is the context every agent works in),
// writable through handleSetOwnOrgDescription.
func (s *Server) handleGetOwnOrg(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	o, err := s.Org.GetOrg(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, o)
}

// handleSetOwnOrgDescription stores the company description without needing the
// tenant administration: the setup asks for it, and afterwards it is edited
// where it is read — in one's own organisation (spec/20).
func (s *Server) handleSetOwnOrgDescription(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Description string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if err := s.Org.SetOrgDescription(r.Context(), p.OrgID, strings.TrimSpace(in.Description)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetPlatformRepo stores where this platform's own source lives
// (spec/21): the target system and the project on it.
//
// Konfiguration und keine Entscheidung des Agenten. Eine Instanz, die gegen
// den oeffentlichen GitHub-Spiegel laeuft, haette sonst einen Agenten, der
// Issues dorthin schreibt, wo die Welt mitliest; eine Instanz, die in ihr
// eigenes GitLab meldet, behaelt sie im Haus. Das entscheidet die
// Organisation, einmal, und nicht ein Modell je Lauf.
//
// Drei Zustaende, seit die Voreinstellung da ist (agents.PlatformRepo):
// leer = das Projekt, aus dem dieses Programm stammt; ein Zielsystem plus
// Projekt = das eigene Repository; agents.RepoOff = gar nicht.
func (s *Server) handleSetPlatformRepo(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		System  string `json:"system"`
		Project string `json:"project"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	system := strings.ToLower(strings.TrimSpace(in.System))
	project := strings.TrimSpace(in.Project)
	// "Aus" traegt kein Projekt — und braucht auch keine Pruefung gegen die
	// angeschlossenen Zielsysteme, weil es keins benennt.
	if system == agents.RepoOff {
		if err := s.Org.SetPlatformRepo(r.Context(), p.OrgID, system, ""); err != nil {
			mapErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		return
	}
	// Beides oder nichts: ein System ohne Projekt ist eine halbe Adresse, und
	// eine halbe Adresse im Prompt ist schlimmer als keine.
	if (system == "") != (project == "") {
		writeErr(w, http.StatusBadRequest, "system and project belong together — set both, or clear both")
		return
	}
	// Nur ein Zielsystem, das diese Organisation wirklich angeschlossen hat.
	// Sonst stuende im Prompt eine Adresse auf einem System, fuer das es kein
	// Credential gibt — und der Agent liefe erst beim Checkout dagegen.
	if system != "" && s.Targets != nil {
		plugins, err := s.Targets.List(r.Context(), p.OrgID)
		if err != nil {
			mapErr(w, err)
			return
		}
		var known bool
		for _, pl := range plugins {
			if pl.Name == system && pl.Enabled {
				known = true
			}
		}
		if !known {
			writeErr(w, http.StatusBadRequest,
				"this organisation has no enabled target system "+system)
			return
		}
	}
	if err := s.Org.SetPlatformRepo(r.Context(), p.OrgID, system, project); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteOrg(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	if id == p.OrgID {
		writeErr(w, http.StatusConflict, "your own organization cannot be deleted")
		return
	}
	if err := s.Org.DeleteOrg(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
