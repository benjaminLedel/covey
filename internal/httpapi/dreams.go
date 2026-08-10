package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"

	"covey/internal/dream"
)

// Dreams (spec/05). The agent tidies up its memory — at night on its own,
// during the day at the push of a button. The endpoints here are the seam:
// start, watch, read up, take back. The work itself lives in internal/dream.

// handleStartDream wakes a dream. Returns immediately; the dream runs in the
// background and is followed through the history. If the agent is already
// dreaming, the running one comes back instead of a second — every dream costs
// an LLM call.
// dreamStore hands out the dream store or answers the request itself. As with
// the skills it is optional: an instance without a configured night run should
// answer with 503 and not with a nil pointer crash that tears down the whole
// connection.
func (s *Server) dreamStore(w http.ResponseWriter) (*dream.Store, bool) {
	if s.Dreams == nil {
		writeErr(w, http.StatusServiceUnavailable, "dreams are not configured on this instance")
		return nil, false
	}
	return s.Dreams, true
}

func (s *Server) handleStartDream(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dreamStore(w); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if _, err := s.Registry.Get(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	p := principalFrom(r)
	provider, err := s.resolveOrgLLM(r.Context(), p.OrgID)
	if err != nil {
		writeErr(w, http.StatusPreconditionFailed,
			"no control-plane LLM credential configured — the agent cannot dream")
		return
	}

	d, err := s.Dreams.Begin(r.Context(), id, "manual")
	if err == dream.ErrAsleep {
		cur, _, cerr := s.Dreams.Current(r.Context(), id)
		if cerr != nil {
			mapErr(w, cerr)
			return
		}
		writeJSON(w, http.StatusOK, cur)
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	// Deliberately not attached to r.Context(): the request is over in a
	// moment, the dream should not be — it runs for minutes. But not attached to
	// context.Background() either: then it would survive a shutdown as a
	// goroutine that nobody cancels and nobody waits for. The server's lifecycle
	// is exactly the right measure in between.
	go func() {
		ctx, cancel := context.WithTimeout(s.baseCtx(), 10*time.Minute)
		defer cancel()
		s.Dreams.Run(ctx, d, provider)
	}()
	writeJSON(w, http.StatusAccepted, d)
}

// handleListDreams returns the dream history: what the agent did with its
// memory over the last nights, action by action.
func (s *Server) handleListDreams(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dreamStore(w); !ok {
		return
	}
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	list, err := s.Dreams.List(r.Context(), id, 30)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleUndoDreamAction takes back a single action.
func (s *Server) handleUndoDreamAction(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.dreamStore(w); !ok {
		return
	}
	actionID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Dreams.Undo(r.Context(), actionID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
