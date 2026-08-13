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
	targetstore "covey/internal/target/store"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// handleTargetWebhook is the event-router entrance for target systems
// (spec/13): plugin lookup (only enabled systems, fail-closed), signature
// verification, idempotent processing, correlation.
// URL: POST /api/webhooks/{system}/{agent} — the last path segment addresses the
// agent (slug or, as a substitute, its ID).
func (s *Server) handleTargetWebhook(w http.ResponseWriter, r *http.Request) {
	systemName := r.PathValue("system")

	// MVP: one organisation — the agent is resolved across orgs.
	ref := r.PathValue("agent")
	agent, err := s.findWebhookAgent(r, ref)
	if errors.Is(err, agents.ErrAmbiguousSlug) {
		// Lieber gar nicht zustellen als falsch: die Nachricht landete sonst
		// bei irgendeinem gleichnamigen Agenten einer fremden Organisation
		// (FR-003, Befund B).
		writeErr(w, http.StatusNotFound,
			"the slug "+ref+" exists in several organisations — address the webhook by the agent id instead")
		return
	}
	if err != nil {
		writeErr(w, http.StatusNotFound, "no agent with slug or id "+ref)
		return
	}
	noteWebhookAgent(r, agent) // request log: attribute the entry to the agent

	// Throttle only from here on, not before resolving the agent: the key IS
	// the agent. An unknown slug has already been answered with a 404 above and
	// costs nothing further.
	if !s.webhookLimiter.allow(agent.ID.String(), time.Now()) {
		s.Log.Warn("webhook rate limit hit", "agent", agent.Slug, "system", systemName)
		w.Header().Set("Retry-After", "60")
		writeErr(w, http.StatusTooManyRequests,
			"too many webhook calls for this agent — please redeliver later")
		return
	}

	sys, err := s.Targets.System(r.Context(), agent.OrgID, systemName)
	if err != nil {
		if errors.Is(err, targetstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "target system "+systemName+" unknown or disabled")
			return
		}
		mapErr(w, err)
		return
	}
	// Webhooks are an optional target-system capability (target.Webhooker) —
	// systems that ingest purely by polling/heartbeat (e.g. GitLab) have no
	// webhook entrance; fail-closed with a 404.
	hook, ok := sys.(target.Webhooker)
	if !ok {
		writeErr(w, http.StatusNotFound, "target system "+systemName+" has no webhook entrance (ingest via polling/heartbeat)")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	if !hook.VerifyWebhook(s.WebhookSecrets[systemName], body, r.Header) {
		writeErr(w, http.StatusUnauthorized, "invalid signature")
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

// --- Generic agent webhook: optional per-agent trigger (spec/03) ---
//
// For foreign systems without a target-system plugin (CI, cron, Zapier,
// monitoring): a secret token in the URL, POST creates a backlog task and wakes
// the agent. Managed via /api/v1/agents/{id}/webhook (manager roles).

// webhookView is the management view: enabled yes/no, token and the ready-made
// trigger URL.
//
// The address comes from the request and not from PublicURL: someone is meant
// to copy this URL and enter it into a foreign system — so it has to be
// reachable from OUTSIDE. PublicURL is the address under which the sandboxes
// reach the control plane, often a loopback; showing that here would simply be
// wrong. The request comes from the operator's browser and therefore carries
// exactly the address under which they reach the instance — and that is the one
// we are after.
func (s *Server) webhookView(r *http.Request, a agents.Agent) map[string]any {
	if a.WebhookToken == nil {
		return map[string]any{"enabled": false}
	}
	url := s.origin(r) + "/api/trigger/" + *a.WebhookToken
	return map[string]any{"enabled": true, "token": *a.WebhookToken, "url": url}
}

// handleGetAgentWebhook — GET /api/v1/agents/{id}/webhook
func (s *Server) handleGetAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	agent, err := s.Registry.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.webhookView(r, agent))
}

// handleEnableAgentWebhook — POST /api/v1/agents/{id}/webhook:
// enables the trigger resp. rotates the token (the old URL becomes invalid).
func (s *Server) handleEnableAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
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
	writeJSON(w, http.StatusOK, s.webhookView(r, agent))
}

// handleDisableAgentWebhook — DELETE /api/v1/agents/{id}/webhook.
func (s *Server) handleDisableAgentWebhook(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	if err := s.Registry.SetWebhookToken(r.Context(), id, nil); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// handleAgentTrigger — POST /api/trigger/{token} (no session auth, the token is
// the secret). Payload optionally as JSON {title, body, priority, dedup_key};
// everything else is taken over as raw text into the task body.
func (s *Server) handleAgentTrigger(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	agent, err := s.Registry.GetByWebhookToken(r.Context(), token)
	if err != nil {
		if errors.Is(err, agents.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "unknown token")
			return
		}
		mapErr(w, err)
		return
	}
	noteWebhookAgent(r, agent) // request log: attribute the entry to the agent
	if agent.Killed {
		writeErr(w, http.StatusConflict, "agent is stopped")
		return
	}
	// A draft's webhook is not live: the address exists, the agent behind it does
	// not work yet (spec/20). Rejected here rather than silently queued, so
	// whoever wired the trigger up learns about it now.
	if agent.Draft() {
		writeErr(w, http.StatusConflict, "agent has not been hired yet")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
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
		// Not JSON — the raw body is the task.
		in.Body = raw
	} else if in.Title == "" && in.Body == "" {
		// JSON without title/body (a foreign payload format) — throw nothing away.
		in.Body = raw
	}
	if in.Title == "" {
		in.Title = "Webhook trigger"
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
