package httpapi

import (
	"errors"
	"io"
	"net/http"

	targetstore "covey/internal/target/store"
)

// handleTargetWebhook ist der Event-Router-Eingang für Zielsysteme (spec/13):
// Plugin-Lookup (nur aktivierte Systeme, fail-closed), Signatur-Prüfung,
// idempotente Verarbeitung, Korrelation.
// URL: POST /api/webhooks/{system}/{slug} — slug adressiert den Agenten.
func (s *Server) handleTargetWebhook(w http.ResponseWriter, r *http.Request) {
	systemName := r.PathValue("system")

	// MVP: eine Organisation — der Agent wird org-übergreifend per Slug gefunden.
	slug := r.PathValue("slug")
	agent, err := s.findAgentBySlug(r, slug)
	if err != nil {
		writeErr(w, http.StatusNotFound, "kein agent mit slug "+slug)
		return
	}

	sys, err := s.Targets.System(r.Context(), agent.OrgID, systemName)
	if err != nil {
		if errors.Is(err, targetstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "zielsystem "+systemName+" unbekannt oder deaktiviert")
			return
		}
		mapErr(w, err)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	if !sys.VerifyWebhook(s.WebhookSecrets[systemName], body, r.Header) {
		writeErr(w, http.StatusUnauthorized, "signatur ungültig")
		return
	}
	ev, err := sys.ParseWebhook(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	outcome, err := s.Orch.HandleWebhook(r.Context(), agent, systemName, ev)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
}
