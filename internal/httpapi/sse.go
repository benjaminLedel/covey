package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleSSE streams live events (agent status, backlog, recording, approvals)
// to the admin UI — the "WebSocket/SSE for live updates" from spec/10.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// The connection belongs to one organisation — it gets only that one's
	// events (FR-003, finding A). An account without membership subscribes to
	// the empty UUID and thus hears nothing, instead of everything.
	ch, cancel := s.Orch.Events().Subscribe(principalFrom(r).OrgID)
	defer cancel()

	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	fmt.Fprint(w, "event: hello\ndata: {}\n\n")
	flusher.Flush()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case ev := <-ch:
			raw, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, raw)
			flusher.Flush()
		}
	}
}
