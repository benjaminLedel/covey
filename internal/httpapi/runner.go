package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/egress"
	runnerstore "covey/internal/runner/store"
)

// The runner API is the only interface a runner has to the platform
// (spec/16-runner.md, "Trust boundary"). It authenticates with the runner
// token, never with a session, and everything it answers is scoped to the
// runner's organisation.
//
// The first user of it is the egress proxy in hard isolation mode. Until now it
// read its allowlist from Postgres itself and therefore had to be given
// COVEY_DATABASE_URL — on a remote runner that would mean distributing the
// database credentials to every host that runs sandboxes. The proxy is an
// enforcement point, not a database client; this is where it stops being one.

// runnerAuth resolves the runner token and hands the runner to the handler.
// Without a valid token: 401, and no hint as to which part was wrong.
func (s *Server) runnerAuth(next func(http.ResponseWriter, *http.Request, runnerstore.Runner)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.Runners == nil {
			writeErr(w, http.StatusServiceUnavailable, "runner API not available")
			return
		}
		token := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		rn, err := s.Runners.ByToken(r.Context(), token)
		if err != nil {
			if !errors.Is(err, runnerstore.ErrNotFound) {
				s.Log.Warn("runner auth failed", "err", err)
			}
			writeErr(w, http.StatusUnauthorized, "runner token invalid")
			return
		}
		// Best effort: a missing timestamp is a display flaw, not a reason to
		// refuse the request the runner is actually here for.
		if err := s.Runners.Seen(r.Context(), rn.ID); err != nil {
			s.Log.Debug("runner heartbeat not recorded", "runner", rn.ID, "err", err)
		}
		next(w, r, rn)
	})
}

// handleRunnerAllowlist answers with one agent's effective allowlist plus the
// hash of its per-sandbox token, so that the proxy can check the token locally
// instead of asking back on every request.
//
// An agent of a foreign organisation is answered with 404 and not with 403: to
// this runner it does not exist, and the difference between "not there" and
// "not yours" is one it has no business learning.
func (s *Server) handleRunnerAllowlist(w http.ResponseWriter, r *http.Request, rn runnerstore.Runner) {
	agentID, err := uuid.Parse(r.URL.Query().Get("agent"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "agent: no valid ID")
		return
	}
	agent, err := s.Registry.Get(r.Context(), agentID)
	if err != nil || agent.OrgID != rn.OrgID {
		writeErr(w, http.StatusNotFound, "agent not found")
		return
	}

	// A missing token is not an error: a sandbox that has never woken has none
	// yet. The empty hash is the fail-closed answer, and the proxy caches it
	// instead of asking again for every request.
	hash, err := s.EgressStore.AgentTokenHash(r.Context(), agentID)
	if err != nil {
		hash = ""
	}
	patterns, err := s.EgressStore.EffectiveAllowlist(r.Context(), agentID)
	if err != nil {
		s.Log.Warn("runner allowlist: reading failed", "agent", agentID, "err", err)
		writeErr(w, http.StatusInternalServerError, "allowlist not readable")
		return
	}
	writeJSON(w, http.StatusOK, egress.AllowlistResponse{Patterns: patterns, TokenHash: hash})
}

// handleRunnerDecisions takes the decision log of a runner's proxy. The
// organisation filter sits in the insert (see egress.Store.LogDecisions), so a
// foreign agent ID in the batch drops out instead of landing in someone else's
// records.
func (s *Server) handleRunnerDecisions(w http.ResponseWriter, r *http.Request, rn runnerstore.Runner) {
	var req egress.DecisionsRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "body is not valid JSON")
		return
	}
	if err := s.EgressStore.LogDecisions(r.Context(), rn.OrgID, req.Decisions); err != nil {
		s.Log.Warn("runner decisions: writing failed", "runner", rn.ID, "err", err)
		writeErr(w, http.StatusInternalServerError, "decision log not written")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
