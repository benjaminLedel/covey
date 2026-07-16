package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// handleSSE streamt Live-Events (Agent-Status, Backlog, Recording, Approvals)
// an die Admin-UI — das "WebSocket/SSE für Live-Updates" aus spec/10.
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, http.StatusInternalServerError, "streaming nicht unterstützt")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch, cancel := s.Orch.Events().Subscribe()
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
