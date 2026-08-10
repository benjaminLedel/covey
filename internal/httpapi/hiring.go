package httpapi

// Hiring: the act that turns a draft into an employee (spec/20).
//
// It is deliberately its own endpoint and not a flag on the agent update. A
// draft is created by all sorts of things — a template, an import, the People
// department — but there is exactly one way out of that state, and it runs
// through a human. That is also why the platform's own target system
// (`internal/target/covey`) has no hire action: an agent may draft another
// agent, it may not employ one.

import (
	"net/http"

	"covey/internal/agents"
)

// handleRollName rolls an agent name. The generator lives in the binary because
// setup and the People department need it too; the interface asks here so that
// there is one pool and not two that drift apart (spec/20).
func (s *Server) handleRollName(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, agents.RollName(langFrom(r)))
}

func (s *Server) handleHire(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	a, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Idempotent: hiring somebody who already works here is not an error, it is a
	// second click. The date stays the day it happened.
	if !a.Draft() {
		writeJSON(w, http.StatusOK, a)
		return
	}
	if err := s.Registry.Hire(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	a, err = s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	// Whatever was queued against the draft has been waiting for this moment.
	// The heartbeat needs no push — the next tick sees the agent now.
	s.Orch.EnsureRunning(id)
	writeJSON(w, http.StatusOK, a)
}

// draftBlocked answers whether an agent may not be woken because it has not
// been hired yet, and writes the error if so. The wake paths a human triggers
// go through here; the dispatch loop makes the same decision in SQL.
func draftBlocked(w http.ResponseWriter, a agents.Agent) bool {
	if !a.Draft() {
		return false
	}
	writeErr(w, http.StatusConflict, "agent has not been hired yet")
	return true
}
