package httpapi

// The endpoints of the public website — reachable without a session, because
// whoever asks them has none yet. Today that is one question: does this
// installation accept registrations (feature-requests/002-plattform-registrierung.md).
//
// It is answered by the server rather than baked into the build for the same
// reason the install script is served by the instance: Covey is self-hosted,
// and what is true for this installation is not true for the next one. A
// website that guessed would either offer a sign-up nobody can complete, or
// hide one that is open.

import (
	"net/http"

	"covey/internal/settings"
)

type signupState struct {
	// Mode: off | waitlist | open. off means the website offers nothing.
	Mode string `json:"mode"`
	// SiteName: what this installation calls itself.
	SiteName string `json:"site_name"`
}

func (s *Server) handleSignupState(w http.ResponseWriter, r *http.Request) {
	st := signupState{Mode: settings.ModeOff, SiteName: "Covey"}
	if s.Settings != nil {
		st.Mode = s.Settings.Mode(r.Context())
		if name, err := s.Settings.Get(r.Context(), settings.SiteName); err == nil && name != "" {
			st.SiteName = name
		}
	}
	// Not cached: whoever closes the registration wants it closed now, not
	// after a proxy's TTL has run out.
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, st)
}
