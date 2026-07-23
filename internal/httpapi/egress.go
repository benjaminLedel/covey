package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"

	"covey/internal/egress"
)

// --- Egress: per-Agent-Allowlist über Templates + eigene Hosts, plus Monitoring ---
//
// Der Egress-Proxy (spec/06, Prinzip #7) lässt pro Agent nur Verbindungen zu
// Hosts auf DESSEN effektiver Allowlist zu (Anthropic-Default + zugewiesene
// Templates + agent-eigene Hosts). Änderungen greifen innerhalb der Cache-TTL
// des Proxy (~15 s). Jede Entscheidung wird protokolliert (egress_log).

// handleEgressStatus liefert Enforcement-Status, die konfigurierbare
// Basis-Allowlist der Org und die nur per Config änderbaren ENV-Zusätze.
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

// --- Basis-Allowlist (org-weit) ---

func (s *Server) handleAddEgressDefaultHost(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct{ Pattern, Note string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültiger request")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct{ Pattern, Note string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
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
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.EgressStore.DeleteTemplateHost(r.Context(), p.OrgID, id); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id.String()})
}

// --- Built-in-Katalog ---

// handleListEgressBuiltins liefert den Katalog samt Kennzeichnung, welche
// Einträge die Org (per Namensgleichheit) schon übernommen hat.
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

// handleImportEgressBuiltin übernimmt einen Katalog-Eintrag als org-eigenes
// Template.
func (s *Server) handleImportEgressBuiltin(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	b, ok := egress.BuiltinBySlug(r.PathValue("slug"))
	if !ok {
		writeErr(w, http.StatusNotFound, "unbekanntes built-in-template")
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

// --- Zuweisung + agent-eigene Hosts ---

func (s *Server) handleAgentEgress(w http.ResponseWriter, r *http.Request) {
	agentID, ok := s.agentInOrg(w, r)
	if !ok {
		return
	}
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
	agentID, ok := s.agentInOrg(w, r)
	if !ok {
		return
	}
	tid, err := uuid.Parse(r.PathValue("tid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige template-id")
		return
	}
	if err := s.EgressStore.SetAgentTemplate(r.Context(), agentID, tid, assigned); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"template_id": tid, "assigned": assigned})
}

func (s *Server) handleAddAgentEgressHost(w http.ResponseWriter, r *http.Request) {
	agentID, ok := s.agentInOrg(w, r)
	if !ok {
		return
	}
	var in struct{ Pattern, Note string }
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
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
	agentID, ok := s.agentInOrg(w, r)
	if !ok {
		return
	}
	hid, err := uuid.Parse(r.PathValue("hid"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige host-id")
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
			writeErr(w, http.StatusBadRequest, "ungültige agent-id")
			return
		}
		// Nur Agenten der eigenen Org.
		if !s.agentBelongsToOrg(r, id) {
			writeErr(w, http.StatusNotFound, "nicht gefunden")
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

// handleEgressStats liefert die 24h-Zusammenfassung für die Monitoring-Kacheln.
func (s *Server) handleEgressStats(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	st, err := s.EgressStore.LogStats(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// --- Helfer ---

// agentInOrg parst {id} und stellt sicher, dass der Agent zur Org des Aufrufers
// gehört. Bei Fehlschlag ist bereits eine Antwort geschrieben (ok=false).
func (s *Server) agentInOrg(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige agent-id")
		return uuid.Nil, false
	}
	if !s.agentBelongsToOrg(r, id) {
		writeErr(w, http.StatusNotFound, "nicht gefunden")
		return uuid.Nil, false
	}
	return id, true
}

func (s *Server) agentBelongsToOrg(r *http.Request, agentID uuid.UUID) bool {
	p := principalFrom(r)
	var one int
	err := s.Pool.QueryRow(r.Context(),
		`SELECT 1 FROM agents WHERE id=$1 AND org_id=$2`, agentID, p.OrgID).Scan(&one)
	return err == nil
}

func egressBadOrErr(w http.ResponseWriter, err error) {
	if errors.Is(err, egress.ErrInvalidPattern) {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	mapErr(w, err)
}
