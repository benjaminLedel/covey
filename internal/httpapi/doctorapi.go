package httpapi

import (
	"net/http"

	"covey/internal/doctor"
)

// handleDoctor: GET /platform/doctor — what a restart would run into on this
// installation, in the browser.
//
// The same checks as `covey doctor`, from the same package. Whoever operates an
// instance through the interface should not have to reach for a shell to learn
// that an image their agents need is missing on this host — and the agent
// config lint made exactly that point first: a check that lives only in a
// subcommand is one that effectively does not exist.
//
// It reads and changes nothing, so a plain GET is honest. Platform admin only:
// the answer names paths, images and the state of the store.
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if s.Config == nil {
		writeErr(w, http.StatusServiceUnavailable, "the process configuration is not available")
		return
	}
	writeJSON(w, http.StatusOK, doctor.RunWith(r.Context(), *s.Config, s.Pool))
}
