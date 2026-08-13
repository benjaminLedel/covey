package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/org"
)

// Org chart (spec/02, spec/09): a company-wide view of humans and agents
// together with their supervisor relationships. Readable for all roles — the
// chart is the shared map of the organization, not an admin view.

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

// handleGetHuman returns a single person of one's own organization — the data
// behind the profile page. Readable for all roles like the chart (the same data
// is in the chart response as well).
func (s *Server) handleGetHuman(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
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

// --- Configurable profile fields (spec/09) ---
// Readable for all roles (the profile editor needs the definitions), managed by
// the org_admin under "Organizations".

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
		writeErr(w, http.StatusBadRequest, "invalid request")
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
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Label string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
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
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	if err := s.Org.DeleteProfileField(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetHumanManager re-hangs a human in the org chart. An empty manager_id
// detaches the assignment. The supervisor has to be a human of the same
// organization; self and cyclic assignments the store refuses (ErrManagerCycle).
func (s *Server) handleSetHumanManager(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		ManagerID string `json:"manager_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	upd := org.HumanUpdate{ManagerID: &uuid.NullUUID{}}
	if in.ManagerID != "" {
		mid, err := uuid.Parse(in.ManagerID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid manager_id")
			return
		}
		upd.ManagerID = &uuid.NullUUID{UUID: mid, Valid: true}
	}
	if _, err := s.Org.UpdateHuman(r.Context(), p.OrgID, id, upd); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetSupervisor re-hangs an agent in the org chart. An empty
// supervisor_id detaches the assignment. The supervisor may be a human OR
// another agent of the same organization — agents can thereby be subordinated
// to other agents too. Self and cyclic assignments are refused.
func (s *Server) handleSetSupervisor(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		SupervisorID string `json:"supervisor_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	var supervisorID *uuid.UUID
	if in.SupervisorID != "" {
		sid, err := uuid.Parse(in.SupervisorID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid supervisor_id")
			return
		}
		// Supervisor = a human of one's own org …
		_, herr := s.Org.GetHuman(r.Context(), p.OrgID, sid)
		if herr != nil {
			// … or an agent of one's own org.
			sup, aerr := s.Registry.Get(r.Context(), sid)
			if aerr != nil || sup.OrgID != p.OrgID {
				writeErr(w, http.StatusNotFound, "supervisor not found")
				return
			}
			// Cycle protection: follow the supervisor path upwards from sid —
			// if it runs into id, a circle would arise (A→B→…→A).
			cur := sid
			for hops := 0; hops < 64; hops++ {
				if cur == id {
					writeErr(w, http.StatusBadRequest, "cyclic subordination not allowed")
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
