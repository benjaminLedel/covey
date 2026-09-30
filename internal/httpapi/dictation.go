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
//
// A turn said in a call ("turn": true, #511) is only corrected, not
// rewritten (dictation.CleanTurn); when it comes back as recognised, "kept"
// says why — too short to clean, translated, or changed too much.
func (s *Server) handleDictationClean(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text    string            `json:"text"`
		App     string            `json:"app"`
		Context dictation.Context `json:"context"`
		Turn    bool              `json:"turn"`
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
	// A short turn is sent as recognised whatever the organisation has.
	if in.Turn && dictation.ShortTurn(in.Text) {
		writeJSON(w, http.StatusOK, map[string]string{"text": in.Text, "kept": dictation.KeptShort})
		return
	}
	p := principalFrom(r)
	provider, err := llm.Resolve(r.Context(), s.Secrets, s.Runtimes, p.OrgID)
	if errors.Is(err, llm.ErrNoCredential) {
		writeErr(w, http.StatusConflict, "cleanup needs a control-plane credential for this organisation")
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	if in.Turn {
		out, kept, err := dictation.CleanTurn(r.Context(), provider, in.Text, c)
		if err != nil {
			writeErr(w, http.StatusBadGateway, "the model did not clean the turn: "+err.Error())
			return
		}
		res := map[string]string{"text": out}
		if kept != "" {
			res["kept"] = kept
		}
		writeJSON(w, http.StatusOK, res)
		return
	}
	out, err := dictation.Clean(r.Context(), provider, in.Text, in.App, c)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "the model did not clean the dictation: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"text": out})
}
