package httpapi

import (
	"net/http"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/orchestrator"
)

// Live events published by the API itself (#619).
//
// The orchestrator publishes what a run does. What a person does through the
// API — creating, hiring, renaming or deleting an agent, changing a
// department, giving an agent a task, discarding one — went past the event
// stream, and every client that keeps itself current from it (the web's
// lists, the app's spaces) saw it only on the next unrelated event or a
// reload.

// roster wraps a handler that changes the roster: after a successful answer
// (below 300) it publishes a `roster` event with change. For the changes to
// one agent (updated, hired, deleted) the agent is the route's {id}; for a
// creation the id is not in the path (it is a template's on instantiate) and
// the event names no agent, nor does a department change.
func (s *Server) roster(change string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &statusMitschrift{ResponseWriter: w, status: http.StatusOK}
		next(rec, r)
		if rec.status >= http.StatusMultipleChoices || s.Orch == nil {
			return
		}
		var agentID uuid.UUID
		switch change {
		case orchestrator.RosterUpdated, orchestrator.RosterHired, orchestrator.RosterDeleted:
			agentID, _ = uuid.Parse(r.PathValue("id"))
		}
		s.Orch.Events().PublishRoster(principalFrom(r).OrgID, agentID, change)
	}
}

// publishTask says that a task changed through the API, the event the
// orchestrator publishes when a run moves one.
func (s *Server) publishTask(t backlog.Task) {
	if s.Orch == nil {
		return
	}
	s.Orch.Events().Publish(orchestrator.Event{Type: "task", AgentID: t.AgentID.String(), OrgID: t.OrgID,
		Data: map[string]string{"task_id": t.ID.String(), "state": t.State}})
}
