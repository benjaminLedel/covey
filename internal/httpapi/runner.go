package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

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
// handleUpdateRunner assigns tags and images to a runner from the interface.
// Both used to come out of the host's config.toml, sent once at connect:
// changing them meant editing a file on the machine and restarting the runner,
// and no operator could see why an agent was not being served.
//
// tags are additive to what the host reports about itself; images replace the
// reported claim, and an empty list is the decision "no claim" — this host
// provides every workplace and fetches what it does not have, which is what
// docker run does anyway.
func (s *Server) handleUpdateRunner(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Tags        *[]string `json:"tags"`
		Images      *[]string `json:"images"`
		Name        *string   `json:"name"`
		Description *string   `json:"description"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	patch := runnerstore.Patch{Name: trimmed(in.Name), Description: trimmed(in.Description)}
	if in.Tags != nil {
		tags := clean(*in.Tags)
		patch.ExtraTags = &tags
	}
	if in.Images != nil {
		images := clean(*in.Images)
		patch.Images = &images
	}
	rn, err := s.Runners.Update(r.Context(), p.OrgID, id, patch)
	if err != nil {
		mapErr(w, err)
		return
	}
	// A connected runner learns it now and not at the next reconnect: "why is
	// it not taking anything, I gave it the tag" is the question this exists
	// to answer.
	if s.RunnerPool != nil {
		s.RunnerPool.SetCapabilities(id, rn.ExtraTags, rn.AssignedImages, rn.ImagesDecided)
	}
	writeJSON(w, http.StatusOK, rn)
}

// trimmed passes a name through without the blanks a text field collects. nil
// stays nil: not sent is not the same as emptied.
func trimmed(v *string) *string {
	if v == nil {
		return nil
	}
	out := strings.TrimSpace(*v)
	return &out
}

// clean drops what a text field leaves behind: empty entries and stray blanks.
func clean(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// handlePullOnRunner fetches an image onto one host, deliberately and while
// somebody is watching. Without it the first wake there is the moment it turns
// out that the pull does not work — a private registry without credentials
// answers in seconds here and in the middle of a run otherwise.
func (s *Server) handlePullOnRunner(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Workplace string `json:"workplace"`
		Image     string `json:"image"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	want := strings.TrimSpace(in.Workplace)
	if want == "" {
		want = strings.TrimSpace(in.Image)
	}
	if want == "" {
		writeErr(w, http.StatusBadRequest, "workplace or image missing")
		return
	}
	if s.RunnerPool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no runner pool")
		return
	}
	// The runner has to belong to this organisation — the pool checks it too,
	// but a 404 here says the right thing instead of "not connected".
	if _, err := s.Runners.ByID(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	image, err := s.RunnerPool.PullOn(r.Context(), p.OrgID, id, want)
	if err != nil {
		// The registry's own words: "no credentials", "manifest unknown" and a
		// timeout call for different things, and a wrapped "pull failed" would
		// hide which.
		writeJSON(w, http.StatusOK, map[string]any{"image": image, "ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"image": image, "ok": true})
}

// handleRunnerHealth answers what stands between this organisation's runners
// and a running sandbox. The check existed before this — it ran at startup into
// the log and inside the onboarding view, which disappears as soon as the five
// steps are done. So an instance that had been working for weeks reported
// nothing when its data plane fell over: on covey.work every wake failed for
// half an hour with "no runner holds the image", and the only place that said
// so was the recording of a task nobody was looking at.
//
// It belongs where somebody looks when agents stop running: the runner view.
func (s *Server) handleRunnerHealth(w http.ResponseWriter, r *http.Request) {
	if s.DataPlane == nil {
		writeJSON(w, http.StatusOK, map[string]any{"ready": true, "problems": []string{}})
		return
	}
	problems := s.dataPlaneProblems(r.Context())
	if problems == nil {
		problems = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ready": len(problems) == 0, "problems": problems})
}

func (s *Server) handleListRunners(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Runners.ListForOrg(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}

	// The row says what a runner IS, the pool what it is DOING. Only both
	// together answer the question somebody comes to this page with: is
	// anything running, and can it carry more.
	live := map[uuid.UUID]runner.Live{}
	if s.RunnerPool != nil {
		live = s.RunnerPool.LiveFor(p.OrgID)
	}
	out := make([]RunnerView, 0, len(list))
	for _, rn := range list {
		view := RunnerView{Runner: rn}
		if l, ok := live[rn.ID]; ok {
			view.Live = &l
		}
		out = append(out, view)
	}
	// Free disk is asked, not remembered: it changes while nobody is looking,
	// and a cached figure is the kind that reassures right up to the moment the
	// disk is full. But asked of all hosts at once and briefly — see
	// capacityWait. Serially and with the full answer timeout this loop was
	// what made the runner page take half a minute to appear whenever one host
	// was busy.
	ctx, cancel := context.WithTimeout(r.Context(), capacityWait)
	defer cancel()
	var wg sync.WaitGroup
	for i := range out {
		if out[i].Live == nil {
			continue
		}
		wg.Add(1)
		go func(v *RunnerView) {
			defer wg.Done()
			if cap, err := s.RunnerPool.Capacity(ctx, v.ID); err == nil {
				v.Capacity = &cap
			}
		}(&out[i])
	}
	wg.Wait()
	writeJSON(w, http.StatusOK, out)
}

// capacityWait is how long the list waits for the hosts' disk figures. A runner
// answers this in milliseconds when it is idle — and not at all while it is
// inside a start, because a start blocks its read loop and a start may be a
// multi-gigabyte pull. The figure is worth showing; it is not worth a page that
// does not come. What is missing shows as an empty column, next to a row that
// is otherwise complete.
const capacityWait = 2 * time.Second

// RunnerView is one runner as the runner page shows it: what it is, and — while
// it is connected — what it is carrying.
type RunnerView struct {
	runnerstore.Runner
	Live     *runner.Live           `json:"live,omitempty"`
	Capacity *runner.CapacityReport `json:"capacity,omitempty"`
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
