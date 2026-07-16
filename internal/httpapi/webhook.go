package httpapi

import (
	"fmt"
	"io"
	"net/http"

	"covey/internal/zammad"
)

// handleZammadWebhook ist der Event-Router-Eingang für Zammad (spec/13):
// HMAC-verifiziert, idempotent, korreliert über die Ticket-id.
// URL: POST /api/webhooks/zammad/{slug} — slug adressiert den Support-Agenten.
func (s *Server) handleZammadWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	if !zammad.VerifySignature(s.ZammadWebhookSecret, body, r.Header.Get("X-Hub-Signature")) {
		writeErr(w, http.StatusUnauthorized, "signatur ungültig")
		return
	}
	payload, err := zammad.ParseWebhook(body)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	// MVP: eine Organisation — der Agent wird org-übergreifend per Slug gefunden.
	slug := r.PathValue("slug")
	agent, err := s.findAgentBySlug(r, slug)
	if err != nil {
		writeErr(w, http.StatusNotFound, "kein agent mit slug "+slug)
		return
	}

	title := fmt.Sprintf("Zammad-Ticket #%s: %s", payload.Ticket.Number, payload.Ticket.Title)
	taskBody := fmt.Sprintf("Neues Ticket im Zammad (id=%d, nummer=%s).\nTitel: %s\n\nNachricht des Kunden:\n%s\n\nBearbeite das Ticket über den Action-Proxy (system zammad, ticket_id=%d).",
		payload.Ticket.ID, payload.Ticket.Number, payload.Ticket.Title, payload.Article.Body, payload.Ticket.ID)
	resumeInput := fmt.Sprintf("Kundenantwort auf Ticket #%d:\n%s", payload.Ticket.ID, payload.Article.Body)

	outcome, err := s.Orch.HandleZammadWebhook(r.Context(), agent,
		payload.DedupKey(), zammad.CorrelationKey(payload.Ticket.ID),
		title, taskBody, resumeInput, payload.IsCustomerMessage())
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
}
