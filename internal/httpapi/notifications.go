package httpapi

// What a person wants to hear about (#169).
//
// Four switches, one per class, and they belong to the ACCOUNT rather than to
// a seat: somebody who works in two organisations does not want to answer the
// same question twice.
//
// The endpoints sit under /auth because they are about the signed-in person
// and not about an organisation — the same place the sessions and the
// password live.

import "net/http"

// handleGetNotificationPrefs — GET /api/v1/auth/notifications.
//
// It answers with the EFFECTIVE settings, defaults included, so the interface
// does not have to carry a second copy of what "on by default" means.
func (s *Server) handleGetNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		writeJSON(w, http.StatusOK, map[string]bool{})
		return
	}
	p := principalFrom(r)
	prefs, err := s.Notify.Preferences(r.Context(), p.AccountID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}

// handleSetNotificationPrefs — PUT /api/v1/auth/notifications.
//
// The body carries only what changed. Sending all four every time would make
// two browsers open at once overwrite each other's answer to a question they
// were not asked.
func (s *Server) handleSetNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		writeErr(w, http.StatusServiceUnavailable, "this installation sends no notifications")
		return
	}
	var in map[string]bool
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	p := principalFrom(r)
	for class, enabled := range in {
		if err := s.Notify.SetPreference(r.Context(), p.AccountID, class, enabled); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	prefs, err := s.Notify.Preferences(r.Context(), p.AccountID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, prefs)
}
