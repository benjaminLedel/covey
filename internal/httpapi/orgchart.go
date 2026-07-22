package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/org"
)

// Org-Chart (spec/02, spec/09): eine unternehmensweite Sicht auf Menschen und
// Agenten samt Vorgesetzten-Beziehungen. Lesbar für alle Rollen — der Chart
// ist die gemeinsame Landkarte der Organisation, keine Admin-Ansicht.

func (s *Server) handleOrgChart(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	humans, err := s.Org.ListHumans(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	agentList, err := s.Registry.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	departments, err := s.Org.ListDepartments(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if humans == nil {
		humans = []org.Human{}
	}
	if agentList == nil {
		agentList = []agents.Agent{}
	}
	if departments == nil {
		departments = []org.Department{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"humans":      humans,
		"agents":      agentList,
		"departments": departments,
	})
}

// handleGetHuman liefert eine einzelne Person der eigenen Organisation —
// die Datenbasis der Profil-Seite. Wie der Chart für alle Rollen lesbar
// (dieselben Daten stehen auch in der Chart-Antwort).
func (s *Server) handleGetHuman(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	h, err := s.Org.GetHuman(r.Context(), p.OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

// --- Konfigurierbare Profilfelder (spec/09) ---
// Lesbar für alle Rollen (der Profil-Editor braucht die Definitionen),
// verwaltet vom platform_admin unter „Organisationen".

func (s *Server) handleListProfileFields(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Org.ListProfileFields(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []org.ProfileField{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateProfileField(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	f, err := s.Org.CreateProfileField(r.Context(), p.OrgID, in.Label)
	if errors.Is(err, org.ErrFieldExists) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleRenameProfileField(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if err := s.Org.RenameProfileField(r.Context(), p.OrgID, id, in.Label); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteProfileField(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	if err := s.Org.DeleteProfileField(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetSupervisor hängt einen Agenten im Org-Chart um. Leere supervisor_id
// löst die Zuordnung. Der Vorgesetzte darf ein Mensch ODER ein anderer Agent
// derselben Organisation sein — Agenten lassen sich damit auch anderen Agenten
// unterordnen. Selbst- und zyklische Zuordnungen werden abgewiesen.
func (s *Server) handleSetSupervisor(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		SupervisorID string `json:"supervisor_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	var supervisorID *uuid.UUID
	if in.SupervisorID != "" {
		sid, err := uuid.Parse(in.SupervisorID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "ungültige supervisor_id")
			return
		}
		// Vorgesetzter = Mensch der eigenen Org …
		_, herr := s.Org.GetHuman(r.Context(), p.OrgID, sid)
		if herr != nil {
			// … oder ein Agent der eigenen Org.
			sup, aerr := s.Registry.Get(r.Context(), sid)
			if aerr != nil || sup.OrgID != p.OrgID {
				writeErr(w, http.StatusNotFound, "vorgesetzter nicht gefunden")
				return
			}
			// Zyklus-Schutz: den Vorgesetzten-Pfad von sid aufwärts verfolgen —
			// stößt er auf id, entstünde ein Kreis (A→B→…→A).
			cur := sid
			for hops := 0; hops < 64; hops++ {
				if cur == id {
					writeErr(w, http.StatusBadRequest, "zyklische Unterstellung nicht erlaubt")
					return
				}
				a, gerr := s.Registry.Get(r.Context(), cur)
				if gerr != nil || a.SupervisorID == nil {
					break
				}
				cur = *a.SupervisorID
			}
		}
		supervisorID = &sid
	}
	if err := s.Registry.SetSupervisor(r.Context(), id, supervisorID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
