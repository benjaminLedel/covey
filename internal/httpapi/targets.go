package httpapi

import (
	"errors"
	"io"
	"net/http"

	targetstore "covey/internal/target/store"
)

// --- Zielsystem-Plugins: Built-ins aktivieren/deaktivieren, Manifeste hochladen ---

// handleListTargets liefert die Zielsysteme der Organisation: kompilierte
// Built-ins (Registry) plus hochgeladene Manifest-Plugins, mit Aktivierungs-Status.
func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Targets.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleUploadTarget nimmt eine Plugin-Datei (JSON-Manifest) entgegen,
// validiert sie fail-closed und speichert sie als Custom-Zielsystem.
func (s *Server) handleUploadTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	m, err := s.Targets.PutManifest(r.Context(), p.OrgID, raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": m.Name, "kind": "custom"})
}

// handleToggleTarget aktiviert/deaktiviert ein Plugin für die Organisation.
func (s *Server) handleToggleTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "feld enabled (true|false) fehlt")
		return
	}
	if err := s.Targets.SetEnabled(r.Context(), p.OrgID, name, *in.Enabled); err != nil {
		if errors.Is(err, targetstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "enabled": *in.Enabled})
}

// handleDeleteTarget entfernt ein Custom-Plugin (Built-ins sind nicht löschbar).
func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	if err := s.Targets.DeleteManifest(r.Context(), p.OrgID, name); err != nil {
		if errors.Is(err, targetstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}
