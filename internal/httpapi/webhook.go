package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"covey/internal/agents"
	"covey/internal/target"
	targetstore "covey/internal/target/store"
)

// handleTargetWebhook ist der Event-Router-Eingang für Zielsysteme (spec/13):
// Plugin-Lookup (nur aktivierte Systeme, fail-closed), Signatur-Prüfung,
// idempotente Verarbeitung, Korrelation.
// URL: POST /api/webhooks/{system}/{agent} — der letzte Pfad-Teil adressiert
// den Agenten (Slug oder, ersatzweise, seine ID).
func (s *Server) handleTargetWebhook(w http.ResponseWriter, r *http.Request) {
	systemName := r.PathValue("system")

	// MVP: eine Organisation — der Agent wird org-übergreifend aufgelöst.
	ref := r.PathValue("agent")
	agent, err := s.findWebhookAgent(r, ref)
	if err != nil {
		writeErr(w, http.StatusNotFound, "kein agent mit slug oder id "+ref)
		return
	}
	noteWebhookAgent(r, agent) // Request-Log: Eintrag dem Agenten zuordnen

	// Erst ab hier bremsen, nicht schon vor der Agenten-Auflösung: Der
	// Schlüssel IST der Agent. Ein unbekannter Slug ist oben bereits mit 404
	// beantwortet und kostet nichts weiter.
	if !s.webhookLimiter.allow(agent.ID.String(), time.Now()) {
		s.Log.Warn("webhook-rate-limit greift", "agent", agent.Slug, "system", systemName)
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests,
			"zu viele Webhook-Aufrufe für diesen Agenten — bitte später erneut zustellen")
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
	// Webhook ist eine optionale Zielsystem-Capability (target.Webhooker) —
	// Systeme, die rein per Polling/Heartbeat aufnehmen (z. B. GitLab), haben
	// keinen Webhook-Eingang; fail-closed mit 404.
	hook, ok := sys.(target.Webhooker)
	if !ok {
		writeErr(w, http.StatusNotFound, "zielsystem "+systemName+" hat keinen webhook-eingang (aufnahme per polling/heartbeat)")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	if !hook.VerifyWebhook(s.WebhookSecrets[systemName], body, r.Header) {
		writeErr(w, http.StatusUnauthorized, "signatur ungültig")
		return
	}
	ev, err := hook.ParseWebhook(body)
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

// --- Generischer Agent-Webhook: optionaler Trigger je Agent (spec/03) ---
//
// Für Fremdsysteme ohne Zielsystem-Plugin (CI, Cron, Zapier, Monitoring):
// ein geheimes Token in der URL, POST legt eine Backlog-Aufgabe an und weckt
// den Agenten. Verwaltung über /api/v1/agents/{id}/webhook (Manager-Rollen).

// webhookView ist die Verwaltungs-Sicht: aktiviert ja/nein, Token und die
// fertige Trigger-URL (absolut, wenn PublicURL konfiguriert ist).
func (s *Server) webhookView(a agents.Agent) map[string]any {
	if a.WebhookToken == nil {
		return map[string]any{"enabled": false}
	}
	url := "/api/trigger/" + *a.WebhookToken
	if s.PublicURL != "" {
		url = strings.TrimSuffix(s.PublicURL, "/") + url
	}
	return map[string]any{"enabled": true, "token": *a.WebhookToken, "url": url}
}

// handleGetAgentWebhook — GET /api/v1/agents/{id}/webhook
func (s *Server) handleGetAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.webhookView(agent))
}

// handleEnableAgentWebhook — POST /api/v1/agents/{id}/webhook:
// aktiviert den Trigger bzw. rotiert das Token (alte URL wird ungültig).
func (s *Server) handleEnableAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	buf := make([]byte, 32)
	rand.Read(buf)
	token := hex.EncodeToString(buf)
	if err := s.Registry.SetWebhookToken(r.Context(), id, &token); err != nil {
		mapErr(w, err)
		return
	}
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.webhookView(agent))
}

// handleDisableAgentWebhook — DELETE /api/v1/agents/{id}/webhook.
func (s *Server) handleDisableAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	if err := s.Registry.SetWebhookToken(r.Context(), id, nil); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// handleAgentTrigger — POST /api/trigger/{token} (ohne Session-Auth, das Token
// ist das Geheimnis). Payload optional als JSON {title, body, priority,
// dedup_key}; alles andere wird als Roh-Text in den Aufgaben-Body übernommen.
func (s *Server) handleAgentTrigger(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	agent, err := s.Registry.GetByWebhookToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "unbekanntes token")
			return
		}
		mapErr(w, err)
		return
	}
	noteWebhookAgent(r, agent) // Request-Log: Eintrag dem Agenten zuordnen
	if agent.Killed {
		writeErr(w, http.StatusConflict, "agent ist gestoppt")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Priority int    `json:"priority"`
		DedupKey string `json:"dedup_key"`
	}
	raw := strings.TrimSpace(string(body))
	if err := json.Unmarshal(body, &in); err != nil {
		// Kein JSON — der Roh-Body ist die Aufgabe.
		in.Body = raw
	} else if in.Title == "" && in.Body == "" {
		// JSON ohne title/body (fremdes Payload-Format) — nichts wegwerfen.
		in.Body = raw
	}
	if in.Title == "" {
		in.Title = "Webhook-Trigger"
	}
	if in.Priority < 1 || in.Priority > 5 {
		in.Priority = 3
	}

	outcome, err := s.Orch.HandleAgentTrigger(r.Context(), agent, in.Title, in.Body, in.Priority, in.DedupKey)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"outcome": outcome})
}
