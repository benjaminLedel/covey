package httpapi

import (
	"net/http"
	"time"

	"covey/internal/doctor"
	"covey/internal/settings"
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

// handleConfirmHomeStoreBackup records that somebody has taken the block store
// into this installation's backup — or withdraws that confirmation.
//
// The platform cannot check it: it does not see into anybody's backup. What it
// can do is let the obligation END. A finding that stands on "!" no matter what
// anybody does is furniture within two weeks, and then the finding next to it —
// the one that IS actionable — goes unread as well.
//
// The date is the value, not a tick: a confirmation from two years ago says
// something different from one from Tuesday, and the check shows which it is.
func (s *Server) handleConfirmHomeStoreBackup(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil {
		writeErr(w, http.StatusServiceUnavailable, "no settings store")
		return
	}
	var in struct {
		Confirmed bool `json:"confirmed"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	value := ""
	if in.Confirmed {
		value = time.Now().UTC().Format(time.RFC3339)
	}
	p := principalFrom(r)
	if err := s.Settings.Set(r.Context(), settings.HomeStoreBackup, value, &p.AccountID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"confirmed_at": value})
}
