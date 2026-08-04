package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"covey/internal/egress"
)

// --- Egress: per-agent allowlist via templates + own hosts, plus monitoring ---
//
// The egress proxy (spec/06, principle #7) only permits connections to hosts on
// THAT agent's effective allowlist (Anthropic default + assigned templates +
// agent-owned hosts). Changes take effect within the proxy's cache TTL (~15 s).
// Every decision is logged (egress_log).

// handleEgressStatus returns the enforcement status, the org's configurable
// base allowlist and the ENV additions that can only be changed via config.
func (s *Server) handleEgressStatus(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	defaults, err := s.EgressStore.ListDefaultHosts(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enforced": s.EgressEnforced,
		"defaults": defaults,
		"env":      s.EgressDefaults,
	})
}

// --- Base allowlist (org-wide) ---

func (s *Server) handleAddEgressDefaultHost(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct{ Pattern, Note string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	h, err := s.EgressStore.AddDefaultHost(r.Context(), p.OrgID, in.Pattern, in.Note)
	if err != nil {
		egressBadOrErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteEgressDefaultHost(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.EgressStore.DeleteDefaultHost(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id.String()})
}

// --- Templates ---

func (s *Server) handleListEgressTemplates(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.EgressStore.ListTemplates(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateEgressTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	t, err := s.EgressStore.CreateTemplate(r.Context(), p.OrgID, in.Name, in.Description)
	if errors.Is(err, egress.ErrTemplateExists) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		egressBadOrErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteEgressTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.EgressStore.DeleteTemplate(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id.String()})
}

func (s *Server) handleAddEgressTemplateHost(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	tid, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct{ Pattern, Note string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	h, err := s.EgressStore.AddTemplateHost(r.Context(), p.OrgID, tid, in.Pattern, in.Note)
	if err != nil {
		egressBadOrErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteEgressTemplateHost(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.EgressStore.DeleteTemplateHost(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id.String()})
}

// --- Built-in catalogue ---

// handleListEgressBuiltins returns the catalogue together with a marker for
// which entries the org has already imported (matched by name).
func (s *Server) handleListEgressBuiltins(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	existing, err := s.EgressStore.ListTemplates(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	byName := map[string]uuid.UUID{}
	for _, t := range existing {
		byName[t.Name] = t.ID
	}
	type entry struct {
		egress.BuiltinTemplate
		Imported   bool       `json:"imported"`
		TemplateID *uuid.UUID `json:"template_id,omitempty"`
	}
	out := make([]entry, 0, len(egress.Builtins))
	for _, b := range egress.Builtins {
		e := entry{BuiltinTemplate: b}
		if id, ok := byName[b.Name]; ok {
			e.Imported = true
			e.TemplateID = &id
		}
		out = append(out, e)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleImportEgressBuiltin adopts a catalogue entry as an org-owned
// template.
func (s *Server) handleImportEgressBuiltin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	b, ok := egress.BuiltinBySlug(r.PathValue("slug"))
	if !ok {
		writeErr(w, http.StatusNotFound, "unknown built-in template")
		return
	}
	t, err := s.EgressStore.ImportBuiltin(r.Context(), p.OrgID, b)
	if errors.Is(err, egress.ErrTemplateExists) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// --- Assignment + agent-owned hosts ---

func (s *Server) handleAgentEgress(w http.ResponseWriter, r *http.Request) {
	// Checked by agentScoped (server.go) — the agent is already settled here.
	agentID := agentFrom(r).ID
	cfg, err := s.EgressStore.AgentConfig(r.Context(), agentID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleAssignEgressTemplate(w http.ResponseWriter, r *http.Request) {
	s.setAgentTemplate(w, r, true)
}

func (s *Server) handleUnassignEgressTemplate(w http.ResponseWriter, r *http.Request) {
	s.setAgentTemplate(w, r, false)
}

func (s *Server) setAgentTemplate(w http.ResponseWriter, r *http.Request, assigned bool) {
	// Checked by agentScoped (server.go) — the agent is already settled here.
	agentID := agentFrom(r).ID
	tid, err := uuid.Parse(r.PathValue("tid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid template id")
		return
	}
	if err := s.EgressStore.SetAgentTemplate(r.Context(), agentID, tid, assigned); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template_id": tid, "assigned": assigned})
}

func (s *Server) handleAddAgentEgressHost(w http.ResponseWriter, r *http.Request) {
	// Checked by agentScoped (server.go) — the agent is already settled here.
	agentID := agentFrom(r).ID
	var in struct{ Pattern, Note string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	h, err := s.EgressStore.AddAgentHost(r.Context(), agentID, in.Pattern, in.Note)
	if err != nil {
		egressBadOrErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) handleDeleteAgentEgressHost(w http.ResponseWriter, r *http.Request) {
	// Checked by agentScoped (server.go) — the agent is already settled here.
	agentID := agentFrom(r).ID
	hid, err := uuid.Parse(r.PathValue("hid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid host id")
		return
	}
	if err := s.EgressStore.DeleteAgentHost(r.Context(), agentID, hid); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": hid.String()})
}

// --- Log ---

func (s *Server) handleEgressLog(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var agentID uuid.UUID
	if a := r.URL.Query().Get("agent"); a != "" {
		id, err := uuid.Parse(a)
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid agent id")
			return
		}
		// This route is org-wide; the agent arrives as a query filter and not
		// from the path — agentScoped does not apply here, so membership has to
		// be checked by hand. Via the registry, not via our own SQL: the one
		// who knows the schema is the store.
		if a, err := s.Registry.Get(r.Context(), id); err != nil || a.OrgID != p.OrgID {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		agentID = id
	}
	blocked := r.URL.Query().Get("blocked") == "true"
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.EgressStore.ListLog(r.Context(), p.OrgID, agentID, blocked, limit)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, entries)
}

// handleEgressStats returns the 24h summary for the monitoring tiles.
func (s *Server) handleEgressStats(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	st, err := s.EgressStore.LogStats(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// --- Helpers ---

// agentInOrg parses {id} and makes sure the agent belongs to the caller's org.
// On failure a response has already been written (ok=false).

func egressBadOrErr(w http.ResponseWriter, err error) {
	if errors.Is(err, egress.ErrInvalidPattern) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mapErr(w, err)
}
