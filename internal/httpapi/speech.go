package httpapi

import (
	"net/http"
	"os"
	"strconv"
)

// The speech model (#348): the Whisper model the app recognises speech with
// on the device. The instance, not a third party, hands it out, so an app
// that talks to this instance needs no other address — and an installation
// without internet access can still offer dictation by placing the file.

// handleSpeechModel says which model this instance offers and whether it can
// be fetched yet. The app keeps a copy per digest and asks again only when
// the digest changes.
func (s *Server) handleSpeechModel(w http.ResponseWriter, r *http.Request) {
	if s.Speech == nil {
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
		return
	}
	ready, fetching, lastErr := s.Speech.Status()
	if !ready && !fetching {
		// A failed fetch is retried when somebody asks — the network may be
		// back, or an operator has placed the file since.
		s.Speech.Ensure()
		ready, fetching, _ = s.Speech.Status()
	}
	body := map[string]any{
		"enabled":  true,
		"name":     s.Speech.Model.Name,
		"sha256":   s.Speech.Model.SHA256,
		"size":     s.Speech.Model.Size,
		"ready":    ready,
		"fetching": fetching,
	}
	if !ready && !fetching && lastErr != nil {
		body["error"] = lastErr.Error()
	}
	writeJSON(w, http.StatusOK, body)
}

// handleSpeechModelFile serves the verified model. Ranges are honoured
// (http.ServeContent), so an interrupted download on a phone resumes.
func (s *Server) handleSpeechModelFile(w http.ResponseWriter, r *http.Request) {
	if s.Speech == nil {
		writeErr(w, http.StatusNotFound, "speech recognition is off on this instance")
		return
	}
	if ready, _, _ := s.Speech.Status(); !ready {
		s.Speech.Ensure()
		w.Header().Set("Retry-After", "30")
		writeErr(w, http.StatusServiceUnavailable, "the speech model is not ready yet")
		return
	}
	f, err := os.Open(s.Speech.Path())
	if err != nil {
		mapErr(w, err)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		mapErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("ETag", strconv.Quote(s.Speech.Model.SHA256))
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	http.ServeContent(w, r, "", st.ModTime(), f)
}
