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

// notificationPrefs is the answer of both endpoints: the person's switches,
// and the classes the installation has switched off for everybody (#180) —
// so the page can grey those out instead of offering a switch that does
// nothing.
type notificationPrefs struct {
	Prefs    map[string]bool `json:"prefs"`
	Disabled []string        `json:"disabled"`
}

func (s *Server) notificationPrefs(r *http.Request) (notificationPrefs, error) {
	p := principalFrom(r)
	prefs, err := s.Notify.Preferences(r.Context(), p.AccountID)
	if err != nil {
		return notificationPrefs{}, err
	}
	return notificationPrefs{Prefs: prefs, Disabled: s.Notify.Disabled(r.Context())}, nil
}

// handleGetNotificationPrefs — GET /api/v1/auth/notifications.
//
// It answers with the EFFECTIVE settings, defaults included, so the interface
// does not have to carry a second copy of what "on by default" means.
func (s *Server) handleGetNotificationPrefs(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		writeJSON(w, http.StatusOK, notificationPrefs{Prefs: map[string]bool{}, Disabled: []string{}})
		return
	}
	out, err := s.notificationPrefs(r)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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
	out, err := s.notificationPrefs(r)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
