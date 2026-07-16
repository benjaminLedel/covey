package httpapi

import (
	"errors"
	"net/http"

	"github.com/google/uuid"

	"covey/internal/egress"
)

// --- Egress-Allowlist: von der Oberfläche gepflegte erlaubte Ziel-Hosts ---
//
// Der Egress-Proxy (spec/06, Designprinzip #7) lässt nur Verbindungen zu
// Hosts auf der Allowlist zu. Fest im Code erlaubt sind die Anthropic-Hosts
// (die Runtime), hier verwaltet der Betrieb die Zielsystem-Hosts (Zammad usw.).
// Änderungen wirken sofort — der laufende Proxy lädt die Liste neu.

type egressView struct {
	Enforced bool           `json:"enforced"`
	Defaults []string       `json:"defaults"`
	Entries  []egress.Entry `json:"entries"`
}

func (s *Server) handleListEgress(w http.ResponseWriter, r *http.Request) {
	entries, err := s.EgressStore.List(r.Context())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, egressView{
		Enforced: s.EgressEnforced,
		Defaults: s.EgressDefaults,
		Entries:  entries,
	})
}

func (s *Server) handleAddEgress(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Pattern string `json:"pattern"`
		Note    string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	e, err := s.EgressStore.Add(r.Context(), in.Pattern, in.Note)
	if err != nil {
		if errors.Is(err, egress.ErrInvalidPattern) {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	s.reloadEgress(r)
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleDeleteEgress(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.EgressStore.Delete(r.Context(), id); err != nil {
		mapErr(w, err)
		return
	}
	s.reloadEgress(r)
	writeJSON(w, http.StatusOK, map[string]string{"deleted": id.String()})
}

// reloadEgress aktualisiert den laufenden Proxy (falls einer läuft). Fehler
// werden nur geloggt — die Änderung ist in der DB persistiert und greift
// spätestens beim nächsten Start.
func (s *Server) reloadEgress(r *http.Request) {
	if s.ReloadEgress == nil {
		return
	}
	if err := s.ReloadEgress(r.Context()); err != nil {
		s.Log.Warn("egress-allowlist neu laden fehlgeschlagen", "err", err)
	}
}
