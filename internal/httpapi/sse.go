package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
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
	//
	// ?types=chat,task narrows it to those event types (#535). The web reads
	// everything and sends no parameter; the app reads four types and never
	// `recording`, of which a run publishes one per step — unfiltered, those
	// filled its buffer and pushed out the events it came for.
	ch, cancel := s.Orch.Events().Subscribe(principalFrom(r).OrgID, eventTypes(r.URL.Query().Get("types"))...)
	defer cancel()

	keepalive := time.NewTicker(keepaliveEvery(r.URL.Query().Get("keepalive")))
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

// eventTypes reads the comma-separated types parameter of /api/v1/events;
// empty means all.
func eventTypes(raw string) []string {
	var out []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// The keepalive's interval. Twenty seconds by default; a client may ask for
// a longer one with ?keepalive=<seconds> (#447). On a phone every write wakes
// the radio from idle and holds it in a high-power state for seconds
// afterwards, so an open, idle app at three pings a minute never let it
// rest. The ceiling stays under the sixty seconds a reverse proxy commonly
// waits before it cuts a silent connection (nginx's proxy_read_timeout);
// the client opens the stream again when it hears nothing for longer.
const (
	keepaliveDefault = 20 * time.Second
	keepaliveMax     = 55 * time.Second
)

func keepaliveEvery(raw string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return keepaliveDefault
	}
	d := time.Duration(n) * time.Second
	return min(max(d, keepaliveDefault), keepaliveMax)
}
