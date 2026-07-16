package httpapi

import (
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
	if humans == nil {
		humans = []org.Human{}
	}
	if agentList == nil {
		agentList = []agents.Agent{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"humans": humans,
		"agents": agentList,
	})
}

// handleSetSupervisor hängt einen Agenten im Org-Chart um. Leere supervisor_id
// löst die Zuordnung. Der Vorgesetzte muss ein Mensch der eigenen Organisation
// sein — Agenten berichten an Menschen, nicht an andere Agenten.
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
		if _, err := s.Org.GetHuman(r.Context(), p.OrgID, sid); err != nil {
			mapErr(w, err)
			return
		}
		supervisorID = &sid
	}
	if err := s.Registry.SetSupervisor(r.Context(), id, supervisorID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
