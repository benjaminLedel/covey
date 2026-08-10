package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"covey/internal/egress"
	"covey/internal/runner"
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

// handleRunnerWS is where a foreign runner arrives. The direction is what makes
// remote execution practical at all: the runner dials out and the control plane
// only waits — a runner needs no inbound reachability, only a way out.
func (s *Server) handleRunnerWS(w http.ResponseWriter, r *http.Request, rn runnerstore.Runner) {
	if s.RunnerPool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no runner pool")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Runners connect machine to machine, not from a browser.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// The pool speaks the protocol until the connection ends. It re-checks the
	// identity from the handshake against the token's: a runner that named a
	// foreign ID would otherwise take another one's place in the pool.
	transport := runner.NewWSTransport(conn)
	if err := s.RunnerPool.AttachRemote(s.baseCtx(), transport, rn.ID, rn.OrgID, func(version, arch string, protocol int) {
		if err := s.Runners.NoteCapabilities(context.WithoutCancel(r.Context()), rn.ID, version, arch, protocol); err != nil {
			s.Log.Warn("runner capabilities not recorded", "runner", rn.ID, "err", err)
		}
	}); err != nil {
		s.Log.Warn("runner connection ended", "runner", rn.ID, "err", err)
	}
}

// handleRunnerBlock serves the home store to a runner: HEAD asks whether a
// block is there, GET fetches it, PUT stores it.
//
// A runner never gets the store's credentials — that would be the same
// omission as the database URL in the egress proxy. It is a client with a
// token, and everything it can reach is its own organisation's (spec/16).
func (s *Server) handleRunnerBlock(w http.ResponseWriter, r *http.Request, rn runnerstore.Runner) {
	if s.Blobs == nil {
		writeErr(w, http.StatusServiceUnavailable, "no home store")
		return
	}
	hash := r.PathValue("hash")
	ctx := r.Context()

	switch r.Method {
	case http.MethodHead:
		has, err := s.Blobs.Has(ctx, rn.OrgID, hash)
		if err != nil || !has {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	case http.MethodGet:
		rc, err := s.Blobs.Get(ctx, rn.OrgID, hash)
		if err != nil {
			writeErr(w, http.StatusNotFound, "block not found")
			return
		}
		defer rc.Close()
		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = io.Copy(w, rc)
	case http.MethodPut:
		// Bounded by the largest block the manifest format produces, with room
		// to spare: an unbounded upload is a way to fill the control plane's
		// disk with a single request.
		body := http.MaxBytesReader(w, r.Body, 16<<20)
		if err := s.Blobs.Put(ctx, rn.OrgID, hash, body); err != nil {
			s.Log.Warn("block not stored", "runner", rn.ID, "err", err)
			writeErr(w, http.StatusBadRequest, "block not stored")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

// handleRunnerRegister turns an organisation's registration token into a
// runner and its own token. Unauthenticated in the session sense — whoever
// registers has nothing to log in with; the registration token IS the
// authentication, and it names the organisation the runner will belong to.
func (s *Server) handleRunnerRegister(w http.ResponseWriter, r *http.Request) {
	if s.Runners == nil {
		writeErr(w, http.StatusServiceUnavailable, "runner API not available")
		return
	}
	var in struct {
		Token       string   `json:"token"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
		Version     string   `json:"version"`
		Arch        string   `json:"arch"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&in); err != nil {
		writeErr(w, http.StatusBadRequest, "body is not valid JSON")
		return
	}
	rn, token, err := s.Runners.Register(r.Context(), in.Token, in.Description, in.Tags)
	if err != nil {
		if errors.Is(err, runnerstore.ErrTokenInvalid) {
			writeErr(w, http.StatusUnauthorized, "registration token invalid or revoked")
			return
		}
		s.Log.Warn("runner registration failed", "err", err)
		writeErr(w, http.StatusInternalServerError, "registration failed")
		return
	}
	if in.Version != "" || in.Arch != "" {
		_ = s.Runners.NoteCapabilities(r.Context(), rn.ID, in.Version, in.Arch, 0)
	}
	s.Log.Info("runner registered", "runner", rn.ID, "org", rn.OrgID, "description", in.Description)
	writeJSON(w, http.StatusOK, map[string]any{
		"runner_id": rn.ID, "org_id": rn.OrgID, "token": token,
	})
}

// handleRunnerWhoami answers who a runner token belongs to. The runner asks
// before it connects: a wrong token should say so plainly here instead of as a
// WebSocket that closes without a reason.
func (s *Server) handleRunnerWhoami(w http.ResponseWriter, _ *http.Request, rn runnerstore.Runner) {
	writeJSON(w, http.StatusOK, map[string]any{"runner_id": rn.ID, "org_id": rn.OrgID})
}

// --- The administrative side: what a human does with runners ---

// handleListRunners shows an organisation's runners. The built-in one is in
// there too — visible so that the model is comprehensible, but it has no token
// to revoke and no delete button: it exists, or the rule says it does not.
func (s *Server) handleListRunners(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Runners.ListForOrg(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []runnerstore.Runner{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleCreateRegistrationToken issues the organisation's registration token.
// It is returned in the clear exactly once — afterwards only its hash exists.
func (s *Server) handleCreateRegistrationToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Description string `json:"description"`
	}
	_ = readJSON(r, &in)
	p := principalFrom(r)
	token, err := s.Runners.CreateRegistrationToken(r.Context(), p.OrgID, in.Description, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"token": token,
		"hint": "Shown once. On the runner host: " +
			"covey-runner register --url <this instance> --token <this token>",
	})
}

// handleDeleteRunner decommissions a registered runner. It takes only its local
// working copy with it, no platform state — everything that mattered is in the
// home store. If it was the organisation's last one, the built-in runner is the
// answer again at the next start.
func (s *Server) handleDeleteRunner(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	if err := s.Runners.Delete(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
