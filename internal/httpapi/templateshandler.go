package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"covey/internal/templates"
)

// langFrom liest die UI-Sprache für lokalisierte (mitgelieferte) Vorlagen:
// Query-Param ?lang= zuerst, sonst der Accept-Language-Header, sonst "de".
func langFrom(r *http.Request) string {
	if l := strings.TrimSpace(r.URL.Query().Get("lang")); l != "" {
		return l
	}
	if l := strings.TrimSpace(r.Header.Get("Accept-Language")); l != "" {
		return l
	}
	return "de"
}

func cloneRequestWithBody(r *http.Request, body []byte) (*http.Request, error) {
	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, r.URL.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header = r.Header
	return req, nil
}

// handleListTemplates — GET /api/v1/templates
func (s *Server) handleListTemplates(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Templates.List(r.Context(), p.OrgID, langFrom(r))
	if err != nil {
		mapErr(w, err)
		return
	}
	if list == nil {
		list = []templates.Template{}
	}
	writeJSON(w, http.StatusOK, list)
}

// handleGetTemplate — GET /api/v1/templates/{id}
func (s *Server) handleGetTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	t, err := s.Templates.Get(r.Context(), p.OrgID, id, langFrom(r))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

// handleSaveTemplate — POST /api/v1/templates
// Body: {name, description, from_agent_id} — baut das Bundle aus dem Agenten.
func (s *Server) handleSaveTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		FromAgentID string `json:"from_agent_id"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		writeErr(w, http.StatusBadRequest, "name fehlt")
		return
	}
	agentID, err := uuid.Parse(in.FromAgentID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "from_agent_id fehlt oder ungültig")
		return
	}

	b, err := s.buildBundle(r.Context(), p.OrgID, agentID, false)
	if err != nil {
		mapErr(w, err)
		return
	}
	// ExportedAt gehört nicht in die gespeicherte Vorlage.
	b.ExportedAt = nil

	bundleJSON, err := json.Marshal(b)
	if err != nil {
		mapErr(w, err)
		return
	}
	t, err := s.Templates.Create(r.Context(), p.OrgID, in.Name, strings.TrimSpace(in.Description), bundleJSON, &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// handleDeleteTemplate — DELETE /api/v1/templates/{id}
func (s *Server) handleDeleteTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.Templates.Delete(r.Context(), p.OrgID, id); err != nil {
		if errors.Is(err, templates.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "vorlage nicht gefunden")
			return
		}
		if errors.Is(err, templates.ErrReadOnly) {
			writeErr(w, http.StatusForbidden, "mitgelieferte vorlage kann nicht gelöscht werden")
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleInstantiateTemplate — POST /api/v1/templates/{id}/instantiate
// Body: {slug, display_name} — legt aus der Vorlage einen neuen Agenten an.
func (s *Server) handleInstantiateTemplate(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	var in struct {
		Slug        string `json:"slug"`
		DisplayName string `json:"display_name"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	in.Slug = strings.TrimSpace(in.Slug)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.Slug == "" {
		writeErr(w, http.StatusBadRequest, "slug fehlt")
		return
	}

	tmpl, err := s.Templates.Get(r.Context(), p.OrgID, id, langFrom(r))
	if err != nil {
		if errors.Is(err, templates.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "vorlage nicht gefunden")
			return
		}
		mapErr(w, err)
		return
	}

	var b agentBundle
	if err := json.Unmarshal(tmpl.Bundle, &b); err != nil {
		writeErr(w, http.StatusInternalServerError, "vorlage nicht lesbar")
		return
	}
	b.Agent.Slug = in.Slug
	if in.DisplayName != "" {
		b.Agent.DisplayName = in.DisplayName
	}
	// Webhook nicht automatisch aktivieren — der Token wäre neu, kein Mehrwert.
	b.Agent.WebhookEnabled = false

	// Importlogik wiederverwenden: Request-Kontext als Träger des Principals.
	bundleJSON, err := json.Marshal(b)
	if err != nil {
		mapErr(w, err)
		return
	}
	fakeReq, err := cloneRequestWithBody(r, bundleJSON)
	if err != nil {
		mapErr(w, err)
		return
	}
	s.handleImportAgent(w, fakeReq)
}
