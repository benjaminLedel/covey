package httpapi

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/chat"
)

/* The spoken reply as it is written (#529).
 *
 * In a call the triage streams its turn, and the sentences of the spoken
 * form are ready one by one while the model still writes the rest. They go
 * to the caller only — the org-wide event stream carries no text: a direct
 * conversation is its members' business. The app asks for the reply to the
 * turn it has just posted, GET /conversations/{id}/messages/{message}/spoken,
 * and gets the sentences as NDJSON, one {"text"} per line, {"end"} once
 * the spoken form is complete (#533) — the app speaks it as one utterance
 * —, and {"done"} when the turn is.
 *
 * What it reads is held in memory while the turn runs and a little after,
 * for an app that asks late; the reply itself is a message as ever, and
 * this stream is only its spoken form, early.
 */

// gesprochenNachhalt is how long a finished stream is kept for an app that
// asks for it after the turn ended.
const gesprochenNachhalt = time.Minute

// gesprochenFrist bounds the endpoint: a turn takes seconds, and the
// triage's own bound is far beyond what a caller waits for.
const gesprochenFrist = 45 * time.Second

// gesprochenStrom is one turn's spoken form so far.
type gesprochenStrom struct {
	conv   uuid.UUID
	saetze []string
	// gesagt: the spoken form is complete (#533); fertig: the turn is.
	gesagt bool
	fertig bool
	ende   time.Time
	// wach is closed and replaced at each change.
	wach chan struct{}
}

// gesprochenHub holds the streams by the message they answer. The zero value
// is ready.
type gesprochenHub struct {
	mu      sync.Mutex
	stroeme map[uuid.UUID]*gesprochenStrom
}

// oeffnen starts the stream of the reply to msg in conv.
func (h *gesprochenHub) oeffnen(msg, conv uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stroeme == nil {
		h.stroeme = map[uuid.UUID]*gesprochenStrom{}
	}
	now := time.Now()
	for id, s := range h.stroeme {
		if s.fertig && now.Sub(s.ende) > gesprochenNachhalt {
			delete(h.stroeme, id)
		}
	}
	h.stroeme[msg] = &gesprochenStrom{conv: conv, wach: make(chan struct{})}
}

// satz adds a sentence to the reply to msg; false when nobody opened it or
// it is done.
func (h *gesprochenHub) satz(msg uuid.UUID, text string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stroeme[msg]
	if s == nil || s.fertig {
		return false
	}
	s.saetze = append(s.saetze, text)
	close(s.wach)
	s.wach = make(chan struct{})
	return true
}

// gesagt marks the spoken form of the reply to msg complete (#533).
func (h *gesprochenHub) gesagt(msg uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stroeme[msg]
	if s == nil || s.fertig || s.gesagt {
		return
	}
	s.gesagt = true
	close(s.wach)
	s.wach = make(chan struct{})
}

// schliessen ends the stream of the reply to msg: nothing more comes.
func (h *gesprochenHub) schliessen(msg uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stroeme[msg]
	if s == nil || s.fertig {
		return
	}
	s.fertig, s.ende = true, time.Now()
	close(s.wach)
}

// ab answers the sentences from the n-th on, whether the spoken form and
// the stream are done, and what to wait on for more; ok is false for a
// stream that does not exist in conv.
func (h *gesprochenHub) ab(msg, conv uuid.UUID, n int) (saetze []string, gesagt, fertig bool, wach <-chan struct{}, ok bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.stroeme[msg]
	if s == nil || s.conv != conv {
		return nil, false, false, nil, false
	}
	if n < len(s.saetze) {
		saetze = append([]string(nil), s.saetze[n:]...)
	}
	return saetze, s.gesagt, s.fertig, s.wach, true
}

// handleSpokenReply streams the spoken form of the reply to one message of
// the conversation, as the triage writes it. 404 when there is none to
// stream — not said in a call, triage off, an older turn, another
// instance's process —: the app then waits for the message as before.
func (s *Server) handleSpokenReply(w http.ResponseWriter, r *http.Request, c chat.Conversation) {
	msg, err := uuid.Parse(r.PathValue("message"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid message id")
		return
	}
	if _, _, _, _, ok := s.gesprochen.ab(msg, c.ID, 0); !ok {
		writeErr(w, http.StatusNotFound, "no spoken reply to stream")
		return
	}
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	enc := json.NewEncoder(w)
	frist := time.NewTimer(gesprochenFrist)
	defer frist.Stop()
	n := 0
	endSaid := false
	for {
		saetze, gesagt, fertig, wach, ok := s.gesprochen.ab(msg, c.ID, n)
		if !ok {
			return
		}
		for _, t := range saetze {
			if enc.Encode(map[string]string{"text": t}) != nil {
				return
			}
			n++
		}
		if gesagt && !endSaid {
			endSaid = true
			if enc.Encode(map[string]bool{"end": true}) != nil {
				return
			}
		}
		if fertig {
			_ = enc.Encode(map[string]bool{"done": true})
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
		select {
		case <-wach:
		case <-frist.C:
			return
		case <-r.Context().Done():
			return
		}
	}
}
