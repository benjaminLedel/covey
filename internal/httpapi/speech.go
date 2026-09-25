package httpapi

import (
	"net/http"
	"os"
	"strconv"

	"covey/internal/speech"
)

// The speech models (#348, #351): the Whisper models the app recognises
// speech with on the device. The instance, not a third party, hands them
// out, so an app that talks to this instance needs no other address — and
// an installation without internet access can still offer dictation by
// placing the files.

// modelState is one model as the app sees it.
func modelState(st *speech.Store) map[string]any {
	ready, fetching, lastErr := st.Status()
	out := map[string]any{
		"name":     st.Model.Name,
		"sha256":   st.Model.SHA256,
		"size":     st.Model.Size,
		"ready":    ready,
		"fetching": fetching,
	}
	if fetching {
		out["received"] = st.Received()
	}
	if !ready && !fetching && lastErr != nil {
		out["error"] = lastErr.Error()
	}
	return out
}

// handleSpeechModel says which models this instance offers and how far each
// is. ?name= asks about one of them and starts fetching it when it is not
// there yet — the app asks this when somebody picks a model. The top-level
// fields describe that model (the default without ?name=), so an app that
// knows only one model reads what it always read.
func (s *Server) handleSpeechModel(w http.ResponseWriter, r *http.Request) {
	if s.Speech == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	st, err := s.Speech.Get(r.URL.Query().Get("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if ready, fetching, _ := st.Status(); !ready && !fetching {
		// Fetched on demand; a failed fetch is retried when somebody asks
		// again — the network may be back, or an operator has placed the file.
		st.Ensure()
	}
	body := modelState(st)
	body["enabled"] = true
	body["default"] = s.Speech.Default
	models := make([]map[string]any, 0, len(s.Speech.Names))
	for _, n := range s.Speech.Names {
		if m, err := s.Speech.Get(n); err == nil {
			models = append(models, modelState(m))
		}
	}
	body["models"] = models
	writeJSON(w, http.StatusOK, body)
}

// handleSpeechModelFile serves a verified model (?name=, default the
// default). Ranges are honoured (http.ServeContent), so an interrupted
// download on a phone resumes.
func (s *Server) handleSpeechModelFile(w http.ResponseWriter, r *http.Request) {
	if s.Speech == nil {
		writeErr(w, http.StatusNotFound, "speech recognition is off on this instance")
		return
	}
	st, err := s.Speech.Get(r.URL.Query().Get("name"))
	if err != nil {
		writeErr(w, http.StatusNotFound, err.Error())
		return
	}
	if ready, _, _ := st.Status(); !ready {
		st.Ensure()
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusServiceUnavailable, "the speech model is not ready yet")
		return
	}
	f, err := os.Open(st.Path())
	if err != nil {
		mapErr(w, err)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", strconv.Quote(st.Model.SHA256))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", fi.ModTime(), f)
}
