package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/org"
)

func (s *Server) handleListDepartments(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Org.ListDepartments(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []org.Department{}
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateDepartment(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	d, err := s.Org.CreateDepartment(r.Context(), p.OrgID, in.Name, in.Description)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleRenameDepartment(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if err := s.Org.RenameDepartment(r.Context(), p.OrgID, id, in.Name); err != nil {
		if errors.Is(err, org.ErrDeptNotFound) {
			writeErr(w, http.StatusNotFound, "nicht gefunden")
			return
		}
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteDepartment(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	if err := s.Org.DeleteDepartment(r.Context(), p.OrgID, id); err != nil {
		if errors.Is(err, org.ErrDeptNotFound) {
			writeErr(w, http.StatusNotFound, "nicht gefunden")
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAddDepartmentLead nimmt ein Mitglied (Mensch oder Agent) in die
// Leitung einer Abteilung auf — eine Abteilung kann mehrere Leitungen haben.
func (s *Server) handleAddDepartmentLead(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Kind     string `json:"kind"`
		MemberID string `json:"member_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	if in.Kind != "human" && in.Kind != "agent" {
		writeErr(w, http.StatusBadRequest, "kind muss human oder agent sein")
		return
	}
	memberID, err := uuid.Parse(in.MemberID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige member_id")
		return
	}
	if err := s.Org.AddDepartmentLead(r.Context(), p.OrgID, id, in.Kind, memberID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleRemoveDepartmentLead(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	memberID, err := uuid.Parse(r.PathValue("member"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige member-id")
		return
	}
	p := principalFrom(r)
	if err := s.Org.RemoveDepartmentLead(r.Context(), p.OrgID, id, memberID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetAgentDepartment(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		DepartmentID string `json:"department_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	var deptID *uuid.UUID
	if in.DepartmentID != "" {
		did, err := uuid.Parse(in.DepartmentID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "ungültige department_id")
			return
		}
		if _, err := s.Org.GetDepartment(r.Context(), p.OrgID, did); err != nil {
			if errors.Is(err, org.ErrDeptNotFound) {
				writeErr(w, http.StatusNotFound, "abteilung nicht gefunden")
				return
			}
			mapErr(w, err)
			return
		}
		deptID = &did
	}
	if err := s.Registry.SetDepartment(r.Context(), id, deptID); err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "nicht gefunden")
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleSetHumanDepartment(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	var in struct {
		DepartmentID string `json:"department_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	var deptID *uuid.UUID
	if in.DepartmentID != "" {
		did, err := uuid.Parse(in.DepartmentID)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "ungültige department_id")
			return
		}
		if _, err := s.Org.GetDepartment(r.Context(), p.OrgID, did); err != nil {
			if errors.Is(err, org.ErrDeptNotFound) {
				writeErr(w, http.StatusNotFound, "abteilung nicht gefunden")
				return
			}
			mapErr(w, err)
			return
		}
		deptID = &did
	}
	if err := s.Org.SetHumanDepartment(r.Context(), p.OrgID, id, deptID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
