// Runtime endpoints: the contracts an organisation holds, the capacity under
// each, and which agent works on which (spec/18-runtimes-capacity.md).
package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/daemon"
	"covey/internal/observability"
	"covey/internal/runtimes"
)

// usageWindow is the period utilisation is reported over for a credential
// WITHOUT a limit of its own. One with a limit is measured over its own window
// — anything else would put a number next to the limit that was not measured
// against it.
const usageWindow = 24 * time.Hour

type credentialView struct {
	runtimes.Credential
	Usage      observability.SlotConsumption `json:"usage"`
	WindowSecs int                           `json:"window_secs"`
	// Metered mirrors the engine's declaration so the interface can say what
	// the numbers mean: money spent, or quota drawn on that is paid for anyway.
	Metered bool `json:"metered"`
	// Reported is the PROVIDER's own figure, where the engine can ask for it
	// (spec/18). It beats our estimate and is labelled differently in the
	// interface, because one is a measurement and the other an inference.
	Reported *daemon.Usage `json:"reported,omitempty"`
}

type runtimeView struct {
	runtimes.Runtime
	Creds    []credentialView   `json:"creds"`
	Bindings []runtimes.Binding `json:"bindings"`
	// CanCarryBlocking is the engine's declared ability to resume a session.
	// Without it the runtime can hold agents that finish in one run and not one
	// that waits for an answer (spec/03).
	CanCarryBlocking bool `json:"can_carry_blocking"`
}

func (s *Server) view(r *http.Request, rt runtimes.Runtime) runtimeView {
	out := runtimeView{Runtime: rt, Creds: make([]credentialView, 0, len(rt.Credentials)),
		Bindings: []runtimes.Binding{}, CanCarryBlocking: runtimes.CanCarryBlocking(rt.Engine)}

	// Consumption per DISTINCT window, not per credential: each is measured
	// over its own, so one shared query would over-report every credential
	// whose window is narrower than the widest — right next to the limit it is
	// being compared against. In practice that is one or two queries.
	usage := map[int]observability.SlotConsumption{}
	windows := map[time.Duration]map[int]bool{}
	for _, c := range rt.Credentials {
		w := usageWindow
		if c.Limit.Active() {
			w = time.Duration(c.Limit.WindowSecs) * time.Second
		}
		if windows[w] == nil {
			windows[w] = map[int]bool{}
		}
		windows[w][c.Ord] = true
	}
	for w, ords := range windows {
		// Best effort: a failing figure must not topple the view — the runtime
		// has to stay administrable when the evaluation does not answer.
		rows, err := s.Obs.RuntimeUsage(r.Context(), rt.ID, w)
		if err != nil {
			continue
		}
		for _, c := range rows {
			if ords[c.Ord] {
				usage[c.Ord] = c
			}
		}
	}

	d, _ := daemon.Describe(rt.Engine)
	for _, c := range rt.Credentials {
		w := usageWindow
		if c.Limit.Active() {
			w = time.Duration(c.Limit.WindowSecs) * time.Second
		}
		cv := credentialView{Credential: c, WindowSecs: int(w.Seconds()),
			Usage: observability.SlotConsumption{Ord: c.Ord}}
		if dc, ok := d.Credential(c.Kind); ok {
			cv.Metered = dc.Metered()
		}
		if u, ok := usage[c.Ord]; ok {
			cv.Usage = u
		}
		if s.Orch != nil {
			if rep, ok := s.Orch.Usage(rt.ID, c.Ord); ok {
				cv.Reported = &rep
			}
		}
		out.Creds = append(out.Creds, cv)
	}
	if b, err := s.Runtimes.Bindings(r.Context(), rt.ID); err == nil {
		out.Bindings = b
	}
	return out
}

func (s *Server) handleListRuntimeInstances(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Runtimes.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := make([]runtimeView, 0, len(list))
	for _, rt := range list {
		out = append(out, s.view(r, rt))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateRuntime(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Engine      string `json:"engine"`
		DisplayName string `json:"display_name"`
		Model       string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil || in.Engine == "" || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "engine and display_name are required")
		return
	}
	if !daemon.IsRuntime(in.Engine) {
		writeErr(w, http.StatusBadRequest, "unknown engine: "+in.Engine)
		return
	}
	rt, err := s.Runtimes.Create(r.Context(), p.OrgID, in.Engine, in.DisplayName, in.Model)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.view(r, rt))
}

func (s *Server) handleUpdateRuntime(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		DisplayName string `json:"display_name"`
		Model       string `json:"model"`
	}
	if err := readJSON(r, &in); err != nil || in.DisplayName == "" {
		writeErr(w, http.StatusBadRequest, "display_name is required")
		return
	}
	if err := s.Runtimes.Update(r.Context(), p.OrgID, id, in.DisplayName, in.Model); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleSetRuntimeFallback declares the runtime to try next when every
// credential of this one is exhausted — a second contract, possibly a
// different engine, that keeps an agent working through a session limit or
// cooldown instead of waiting it out. fallback_runtime_id=null clears it.
func (s *Server) handleSetRuntimeFallback(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		FallbackRuntimeID *uuid.UUID `json:"fallback_runtime_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	if in.FallbackRuntimeID != nil && *in.FallbackRuntimeID == id {
		writeErr(w, http.StatusBadRequest, "a runtime cannot be its own fallback")
		return
	}
	if err := s.Runtimes.SetFallback(r.Context(), p.OrgID, id, in.FallbackRuntimeID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteRuntime(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Runtimes.Delete(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleAddRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Kind       string `json:"kind"`
		SecretKey  string `json:"secret_key"`
		SecretSlot int    `json:"secret_slot"`
		Label      string `json:"label"`
	}
	if err := readJSON(r, &in); err != nil || in.Kind == "" || in.SecretKey == "" {
		writeErr(w, http.StatusBadRequest, "kind and secret_key are required")
		return
	}
	ord, err := s.Runtimes.AddCredential(r.Context(), p.OrgID, id, in.Kind, in.SecretKey, in.SecretSlot, in.Label)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ord": ord})
}

func (s *Server) handlePatchRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	ord, err := strconv.Atoi(r.PathValue("ord"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid ord")
		return
	}
	var in struct {
		Label *string         `json:"label"`
		Limit *runtimes.Limit `json:"limit"`
		// Cooldown=false is the only accepted value: a park may be lifted by
		// hand ("the token works again, try it"), but setting one belongs to
		// the platform — a cooldown claims a measurement.
		Cooldown *bool `json:"cooldown"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body")
		return
	}
	if in.Label != nil {
		if err := s.Runtimes.SetLabel(r.Context(), p.OrgID, id, ord, *in.Label); err != nil {
			mapErr(w, err)
			return
		}
	}
	if in.Limit != nil {
		if u := in.Limit.Unit; u != "" && u != "usd" && u != "tokens" {
			writeErr(w, http.StatusBadRequest, `limit unit must be "usd" or "tokens"`)
			return
		}
		if err := s.Runtimes.SetLimit(r.Context(), p.OrgID, id, ord, *in.Limit); err != nil {
			mapErr(w, err)
			return
		}
	}
	if in.Cooldown != nil {
		if *in.Cooldown {
			writeErr(w, http.StatusBadRequest, "a cooldown is set by the platform, not by hand")
			return
		}
		if _, err := s.Runtimes.Get(r.Context(), p.OrgID, id); err != nil {
			mapErr(w, err)
			return
		}
		if err := s.Runtimes.Cooldown(r.Context(), id, ord, time.Time{}, ""); err != nil {
			mapErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteRuntimeCredential(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	ord, err := strconv.Atoi(r.PathValue("ord"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid ord")
		return
	}
	if err := s.Runtimes.RemoveCredential(r.Context(), p.OrgID, id, ord); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleReorderRuntimeCredentials sets the merit order — the sequence in which
// capacity is used up. It is a statement somebody makes on purpose: paid-for
// seats first, metered capacity for the peak.
func (s *Server) handleReorderRuntimeCredentials(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Order []int `json:"order"`
	}
	if err := readJSON(r, &in); err != nil || len(in.Order) == 0 {
		writeErr(w, http.StatusBadRequest, "order is required")
		return
	}
	if err := s.Runtimes.Reorder(r.Context(), p.OrgID, id, in.Order); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleAssignRuntime puts an agent on a contract.
func (s *Server) handleAssignRuntime(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	agentID, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		RuntimeID string `json:"runtime_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	rtID, err := uuid.Parse(in.RuntimeID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}
	if err := s.Runtimes.Assign(r.Context(), p.OrgID, agentID, rtID); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// attachDefaultRuntime puts a fresh agent on its engine's workplace, creating
// that workplace from the deposited credentials if it does not exist yet.
//
// Best effort throughout: an agent without a workplace is configurable and
// fixable, an agent that could not be created is not. What must not happen is
// silence — without a workplace every run fails on a missing credential, and
// the reason has to be findable in the log rather than in the code.
func (s *Server) attachDefaultRuntime(ctx context.Context, orgID uuid.UUID, a agents.Agent) {
	if s.Runtimes == nil || a.Runtime == "" {
		return
	}
	rt, err := s.Runtimes.EnsureDefault(ctx, orgID, a.Runtime)
	if err != nil {
		s.Log.Warn("workplace could not be set up — the agent has no capacity yet",
			"agent", a.Slug, "engine", a.Runtime, "err", err)
		return
	}
	if err := s.Runtimes.Assign(ctx, orgID, a.ID, rt.ID); err != nil {
		s.Log.Warn("agent could not be assigned to a workplace",
			"agent", a.Slug, "runtime", rt.ID, "err", err)
	}
}

// syncDefaultRuntime attaches a newly deposited LLM credential to its engine's
// workplace.
//
// The documented order is credential first, agent second — then creating the
// agent picks the secret up. Whoever does it the other way round, or adds a
// second seat later, would otherwise be left with a workplace that has no
// capacity and no hint as to why.
func (s *Server) syncDefaultRuntime(ctx context.Context, orgID uuid.UUID, key string) {
	if s.Runtimes == nil {
		return
	}
	for _, d := range daemon.Runtimes() {
		for _, c := range d.Credentials {
			if c.Secret != key {
				continue
			}
			if _, err := s.Runtimes.EnsureDefault(ctx, orgID, d.Name); err != nil {
				s.Log.Warn("workplace could not take up the credential",
					"engine", d.Name, "secret", key, "err", err)
			}
			return
		}
	}
}
