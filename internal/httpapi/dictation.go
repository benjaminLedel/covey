package httpapi

import (
	"errors"
	"net/http"
	"strings"

	"covey/internal/dictation"
	"covey/internal/llm"
)

// handleDictationClean turns dictated text into what the person meant to
// write (#355): the desktop's dictate-anywhere sends what the device
// recognised, the name of the app it is for, and — when the person allows
// it — where in that app it goes (#362). One fast control-plane turn
// with the organisation's credential; without one, 409, and the app inserts
// the raw text. Nothing is stored.
func (s *Server) handleDictationClean(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text    string            `json:"text"`
		App     string            `json:"app"`
		Context dictation.Context `json:"context"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	in.Text = strings.TrimSpace(in.Text)
	if in.Text == "" || len(in.Text) > dictation.MaxInput || len(in.App) > 200 {
		writeErr(w, http.StatusBadRequest, "text is required (at most 20000 characters), app at most 200")
		return
	}
	c := in.Context
	if len([]rune(c.Before)) > dictation.MaxContextBefore || len([]rune(c.After)) > dictation.MaxContextAfter ||
		len(c.Window) > 300 || len(c.Field) > 300 {
		writeErr(w, http.StatusBadRequest, "context: at most 600 characters before, 200 after, 300 for window and field")
		return
	}
	p := principalFrom(r)
	provider, err := llm.Resolve(r.Context(), s.Secrets, p.OrgID)
	if errors.Is(err, llm.ErrNoCredential) {
		writeErr(w, http.StatusConflict, "cleanup needs a control-plane credential for this organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	out, err := dictation.Clean(r.Context(), provider, in.Text, in.App, c)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the model did not clean the dictation: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": out})
}
