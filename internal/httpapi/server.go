// Package httpapi ist der API/BFF-Teil des Binaries (spec/10): REST-API mit
// RBAC, Daemon-WebSocket, SSE-Live-Updates, Zammad-Webhook und die
// eingebettete Admin-SPA.
package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/buildinfo"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/secrets"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"
)

type Server struct {
	Pool     *pgxpool.Pool
	Registry *agents.Registry
	Backlog  *backlog.Store
	Obs      *observability.Store
	Rails    *guardrails.Store
	Secrets  secrets.Store
	Identity identity.Provider
	Memory   *memory.Store
	Org      *org.Store
	Targets  *targetstore.Store
	Orch     *orchestrator.Orchestrator
	WebFS    fs.FS // dist der SPA; nil = nur API
	Log      *slog.Logger

	// Egress (UI-verwaltet). EgressStore ist immer gesetzt; EgressEnforced
	// spiegelt die Prozess-Config, EgressDefaults sind die ENV-Zusätze aus
	// COVEY_EGRESS_ALLOW (nur per Config änderbar). Die fachliche Basis-
	// Allowlist liegt konfigurierbar in der DB (egress_default_hosts). Der
	// Proxy lädt Änderungen selbst nach (Resolver-Cache mit TTL).
	EgressStore    *egress.Store
	EgressEnforced bool
	EgressDefaults []string

	Templates *templates.Store

	// ReqLog protokolliert die HTTP-Requests an den Rändern (Webhooks rein,
	// Zielsystem-Aufrufe raus). nil = abgeschaltet (COVEY_REQUEST_LOG=false).
	ReqLog *reqlogstore.Store

	// WebhookSecrets: Zielsystem-Name → Signatur-Secret
	// (ENV COVEY_<SYSTEM>_WEBHOOK_SECRET, z. B. COVEY_ZAMMAD_WEBHOOK_SECRET).
	WebhookSecrets map[string]string
	SessionTTL     time.Duration
	// PublicURL füllt den {public_url}-Platzhalter in den Einrichtungs-
	// Anleitungen der Zielsystem-Plugins (Webhook-Endpunkte).
	PublicURL string
	// CookieSecure setzt das Secure-Flag auf dem Session-Cookie (HTTPS-only).
	CookieSecure bool

	// loginLimiter bremst Brute-Force auf /auth/login (lazy in Handler init.).
	loginLimiter *loginLimiter
	// wikiRuns hält die laufenden Wiki-Wartungsläufe je Agent (lazy, s. o.).
	wikiRuns *wikiRunStore
}

func (s *Server) Handler() http.Handler {
	if s.loginLimiter == nil {
		s.loginLimiter = newLoginLimiter()
	}
	if s.wikiRuns == nil {
		s.wikiRuns = newWikiRunStore()
	}
	mux := http.NewServeMux()

	// Health/Readiness (ohne Auth).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "db nicht erreichbar", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ready"))
	})

	// Herkunft des laufenden Binaries (Version, Commit, Bauzeit) — der Fuß
	// der UI zeigt sie an. Angemeldet, nicht öffentlich: welcher Commit auf
	// einer Instanz läuft, geht Dritte nichts an.
	mux.Handle("GET /api/v1/version", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, buildinfo.Get())
	}))

	// Auth.
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.Handle("GET /api/v1/auth/me", s.auth(s.handleMe))
	mux.Handle("PATCH /api/v1/auth/me", s.auth(s.handleUpdateMe))
	mux.Handle("GET /api/v1/auth/me/profile", s.auth(s.handleMyProfile))
	mux.Handle("GET /api/v1/auth/sessions", s.auth(s.handleListSessions))
	mux.Handle("DELETE /api/v1/auth/sessions", s.auth(s.handleRevokeOtherSessions))

	// Agenten & Backlog. Lesen dürfen alle Rollen (rollen-gescopte Sichten
	// im MVP: gleiche Daten, unterschiedliche Schreibrechte).
	anyRole := []string{identity.RolePlatformAdmin, identity.RoleAgentOwner,
		identity.RoleSecurity, identity.RoleAuditor, identity.RoleControlling}
	manage := []string{identity.RolePlatformAdmin, identity.RoleAgentOwner}
	securityRoles := []string{identity.RolePlatformAdmin, identity.RoleSecurity}

	mux.Handle("GET /api/v1/agents", s.rbac(anyRole, s.handleListAgents))
	mux.Handle("POST /api/v1/agents", s.rbac(manage, s.handleCreateAgent))
	mux.Handle("GET /api/v1/agents/{id}", s.rbac(anyRole, s.handleGetAgent))
	mux.Handle("DELETE /api/v1/agents/{id}", s.rbac(manage, s.handleDeleteAgent))
	mux.Handle("GET /api/v1/agents/{id}/config", s.rbac(anyRole, s.handleGetConfig))
	mux.Handle("GET /api/v1/agents/{id}/export", s.rbac(append(manage, identity.RoleSecurity), s.handleExportAgent))
	mux.Handle("GET /api/v1/agents/{id}/diagnostics", s.rbac(append(manage, identity.RoleSecurity), s.handleAgentDiagnostics))
	mux.Handle("GET /api/v1/skills/covey-agent.zip", s.rbac(anyRole, s.handleDownloadSkill))
	mux.Handle("POST /api/v1/agents/import", s.rbac(manage, s.handleImportAgent))
	mux.Handle("PUT /api/v1/agents/{id}/config", s.rbac(manage, s.handlePutConfig))
	// Bestehenden Agenten aus einem Bundle überschreiben — nur die Config-Dateien.
	mux.Handle("POST /api/v1/agents/{id}/config/import", s.rbac(manage, s.handleImportConfig))
	// KI-Assistent zum Anpassen von Agenten (Config-Copilot, FR-001): nur
	// verfügbar, wenn org-weit ein Claude-Credential hinterlegt ist.
	mux.Handle("GET /api/v1/assist/status", s.rbac(anyRole, s.handleAssistStatus))
	mux.Handle("POST /api/v1/agents/{id}/config/assist", s.rbac(manage, s.handleConfigAssist))
	mux.Handle("GET /api/v1/agents/{id}/heartbeats", s.rbac(anyRole, s.handleHeartbeats))
	mux.Handle("POST /api/v1/agents/{id}/heartbeats/{name}/fire", s.rbac(manage, s.handleFireHeartbeat))
	mux.Handle("GET /api/v1/agents/{id}/backlog", s.rbac(anyRole, s.handleBacklog))
	mux.Handle("POST /api/v1/agents/{id}/tasks", s.rbac(manage, s.handleCreateTask))
	mux.Handle("POST /api/v1/agents/{id}/wake", s.rbac(manage, s.handleWake))
	mux.Handle("POST /api/v1/agents/{id}/kill", s.rbac(append(manage, identity.RoleSecurity), s.handleKill))
	mux.Handle("POST /api/v1/agents/{id}/resume", s.rbac(append(manage, identity.RoleSecurity), s.handleResumeAgent))
	mux.Handle("POST /api/v1/agents/{id}/budget", s.rbac(manage, s.handleSetBudget))
	mux.Handle("PATCH /api/v1/agents/{id}/name", s.rbac(manage, s.handleRename))
	mux.Handle("PATCH /api/v1/agents/{id}/profile", s.rbac(manage, s.handleUpdateAgentProfile))
	mux.Handle("PATCH /api/v1/agents/{id}/slug", s.rbac(manage, s.handleSetSlug))
	mux.Handle("PATCH /api/v1/agents/{id}/runtime", s.rbac(manage, s.handleSetRuntime))
	mux.Handle("PATCH /api/v1/agents/{id}/model", s.rbac(manage, s.handleSetModel))
	mux.Handle("PATCH /api/v1/agents/{id}/max-turns", s.rbac(manage, s.handleSetMaxTurns))
	mux.Handle("PATCH /api/v1/agents/{id}/recording-level", s.rbac(manage, s.handleSetRecordingLevel))
	mux.Handle("PATCH /api/v1/agents/{id}/warm-sandbox", s.rbac(manage, s.handleSetWarmSandbox))
	mux.Handle("GET /api/v1/org/recording-level", s.rbac(anyRole, s.handleGetOrgRecording))
	mux.Handle("PATCH /api/v1/org/recording-level", s.rbac(securityRoles, s.handleSetOrgRecording))
	mux.Handle("PATCH /api/v1/agents/{id}/supervisor", s.rbac(manage, s.handleSetSupervisor))
	mux.Handle("PATCH /api/v1/agents/{id}/department", s.rbac(manage, s.handleSetAgentDepartment))
	mux.Handle("PATCH /api/v1/org/humans/{id}/department", s.rbac(manage, s.handleSetHumanDepartment))
	mux.Handle("PATCH /api/v1/org/humans/{id}/manager", s.rbac(manage, s.handleSetHumanManager))
	mux.Handle("GET /api/v1/departments", s.rbac(anyRole, s.handleListDepartments))
	mux.Handle("POST /api/v1/departments", s.rbac(manage, s.handleCreateDepartment))
	mux.Handle("PATCH /api/v1/departments/{id}/name", s.rbac(manage, s.handleRenameDepartment))
	mux.Handle("PATCH /api/v1/departments/{id}/color", s.rbac(manage, s.handleSetDepartmentColor))
	mux.Handle("DELETE /api/v1/departments/{id}", s.rbac(manage, s.handleDeleteDepartment))
	mux.Handle("POST /api/v1/departments/{id}/leads", s.rbac(manage, s.handleAddDepartmentLead))
	mux.Handle("DELETE /api/v1/departments/{id}/leads/{member}", s.rbac(manage, s.handleRemoveDepartmentLead))
	mux.Handle("GET /api/v1/agents/{id}/webhook", s.rbac(manage, s.handleGetAgentWebhook))
	mux.Handle("POST /api/v1/agents/{id}/webhook", s.rbac(manage, s.handleEnableAgentWebhook))
	mux.Handle("DELETE /api/v1/agents/{id}/webhook", s.rbac(manage, s.handleDisableAgentWebhook))
	mux.Handle("GET /api/v1/org/chart", s.rbac(anyRole, s.handleOrgChart))
	mux.Handle("GET /api/v1/org/humans/{id}", s.rbac(anyRole, s.handleGetHuman))
	mux.Handle("GET /api/v1/org/profile-fields", s.rbac(anyRole, s.handleListProfileFields))
	mux.Handle("POST /api/v1/org/profile-fields", s.rbac([]string{identity.RolePlatformAdmin}, s.handleCreateProfileField))
	mux.Handle("PATCH /api/v1/org/profile-fields/{id}", s.rbac([]string{identity.RolePlatformAdmin}, s.handleRenameProfileField))
	mux.Handle("DELETE /api/v1/org/profile-fields/{id}", s.rbac([]string{identity.RolePlatformAdmin}, s.handleDeleteProfileField))
	mux.Handle("GET /api/v1/runtimes", s.rbac(anyRole, s.handleListRuntimes))
	mux.Handle("GET /api/v1/targets", s.rbac(anyRole, s.handleListTargets))
	mux.Handle("POST /api/v1/targets", s.rbac(securityRoles, s.handleUploadTarget))
	mux.Handle("POST /api/v1/targets/mcp", s.rbac(securityRoles, s.handleCreateMCP))
	mux.Handle("POST /api/v1/targets/{name}/discover", s.rbac(securityRoles, s.handleDiscoverMCP))
	mux.Handle("GET /api/v1/targets/{name}/tools", s.rbac(anyRole, s.handleListMCPTools))
	mux.Handle("PATCH /api/v1/targets/{name}", s.rbac(securityRoles, s.handleToggleTarget))
	mux.Handle("DELETE /api/v1/targets/{name}", s.rbac(securityRoles, s.handleDeleteTarget))
	mux.Handle("GET /api/v1/agents/{id}/tools/{system}", s.rbac(anyRole, s.handleGetAgentTools))
	mux.Handle("PUT /api/v1/agents/{id}/tools/{system}", s.rbac(securityRoles, s.handleSetAgentTools))
	mux.Handle("GET /api/v1/agents/{id}/recording", s.rbac(anyRole, s.handleRecording))
	mux.Handle("GET /api/v1/recordings/blobs/{id}", s.rbac(anyRole, s.handleRecordingBlob))
	mux.Handle("GET /api/v1/agents/{id}/cost", s.rbac(anyRole, s.handleCost))
	mux.Handle("GET /api/v1/agents/{id}/cost/series", s.rbac(anyRole, s.handleCostSeries))
	mux.Handle("GET /api/v1/cost/org", s.rbac(anyRole, s.handleOrgCost))
	mux.Handle("GET /api/v1/agents/{id}/memories", s.rbac(anyRole, s.handleMemories))
	mux.Handle("POST /api/v1/agents/{id}/memories", s.rbac(manage, s.handleCreateMemory))
	mux.Handle("GET /api/v1/agents/{id}/wiki/log", s.rbac(anyRole, s.handleWikiLog))
	mux.Handle("GET /api/v1/agents/{id}/wiki/health", s.rbac(anyRole, s.handleWikiHealth))
	mux.Handle("POST /api/v1/agents/{id}/wiki/consolidate", s.rbac(manage, s.handleWikiConsolidate))
	mux.Handle("POST /api/v1/agents/{id}/wiki/maintain", s.rbac(manage, s.handleWikiMaintainStart))
	mux.Handle("GET /api/v1/agents/{id}/wiki/maintain", s.rbac(anyRole, s.handleWikiMaintainStatus))
	mux.Handle("DELETE /api/v1/agents/{id}/wiki/maintain", s.rbac(manage, s.handleWikiMaintainDismiss))
	mux.Handle("PATCH /api/v1/memories/{id}", s.rbac(manage, s.handleUpdateMemory))
	mux.Handle("DELETE /api/v1/memories/{id}", s.rbac(manage, s.handleDeleteMemory))
	mux.Handle("POST /api/v1/tasks/{id}/cancel", s.rbac(manage, s.handleCancelTask))
	mux.Handle("POST /api/v1/tasks/{id}/retry", s.rbac(manage, s.handleRetryTask))
	mux.Handle("POST /api/v1/tasks/{id}/archive", s.rbac(manage, s.handleArchiveTask))
	mux.Handle("POST /api/v1/agents/{id}/backlog/cleanup", s.rbac(manage, s.handleCleanupBacklog))
	mux.Handle("POST /api/v1/tasks/{id}/stage", s.rbac(manage, s.handleMoveTask))
	mux.Handle("GET /api/v1/tasks/{id}/transitions", s.rbac(anyRole, s.handleTransitions))
	mux.Handle("GET /api/v1/tasks/{id}/notes", s.rbac(anyRole, s.handleTaskNotes))

	// Custom-Stages (Kanban-Overlay, pro Agent).
	mux.Handle("GET /api/v1/agents/{id}/stages", s.rbac(anyRole, s.handleListStages))
	mux.Handle("POST /api/v1/agents/{id}/stages", s.rbac(manage, s.handleCreateStage))
	mux.Handle("POST /api/v1/agents/{id}/stages/reorder", s.rbac(manage, s.handleReorderStages))
	mux.Handle("PATCH /api/v1/stages/{id}", s.rbac(manage, s.handleUpdateStage))
	mux.Handle("DELETE /api/v1/stages/{id}", s.rbac(manage, s.handleDeleteStage))

	// Vertrauensschicht.
	mux.Handle("GET /api/v1/approvals", s.rbac(anyRole, s.handleListApprovals))
	mux.Handle("POST /api/v1/approvals/{id}/decide", s.rbac(append(manage, identity.RoleSecurity), s.handleDecideApproval))
	mux.Handle("GET /api/v1/guardrails", s.rbac(anyRole, s.handleListGuardrails))
	mux.Handle("POST /api/v1/guardrails", s.rbac(securityRoles, s.handleCreateGuardrail))
	mux.Handle("PATCH /api/v1/guardrails/{id}", s.rbac(securityRoles, s.handleUpdateGuardrail))
	mux.Handle("DELETE /api/v1/guardrails/{id}", s.rbac(securityRoles, s.handleDeleteGuardrail))
	mux.Handle("POST /api/v1/guardrails/test", s.rbac(anyRole, s.handleTestGuardrail))
	mux.Handle("GET /api/v1/guardrails/events", s.rbac(anyRole, s.handleGuardrailEvents))
	// Egress: Status, Templates, Zuweisung pro Agent, Entscheidungs-Log.
	mux.Handle("GET /api/v1/egress", s.rbac(anyRole, s.handleEgressStatus))
	mux.Handle("POST /api/v1/egress/defaults", s.rbac(securityRoles, s.handleAddEgressDefaultHost))
	mux.Handle("DELETE /api/v1/egress/defaults/{id}", s.rbac(securityRoles, s.handleDeleteEgressDefaultHost))
	mux.Handle("GET /api/v1/egress/templates", s.rbac(anyRole, s.handleListEgressTemplates))
	mux.Handle("POST /api/v1/egress/templates", s.rbac(securityRoles, s.handleCreateEgressTemplate))
	mux.Handle("DELETE /api/v1/egress/templates/{id}", s.rbac(securityRoles, s.handleDeleteEgressTemplate))
	mux.Handle("POST /api/v1/egress/templates/{id}/hosts", s.rbac(securityRoles, s.handleAddEgressTemplateHost))
	mux.Handle("DELETE /api/v1/egress/template-hosts/{id}", s.rbac(securityRoles, s.handleDeleteEgressTemplateHost))
	mux.Handle("GET /api/v1/egress/log", s.rbac(anyRole, s.handleEgressLog))
	mux.Handle("GET /api/v1/egress/stats", s.rbac(anyRole, s.handleEgressStats))
	mux.Handle("GET /api/v1/egress/builtin", s.rbac(anyRole, s.handleListEgressBuiltins))
	mux.Handle("POST /api/v1/egress/builtin/{slug}", s.rbac(securityRoles, s.handleImportEgressBuiltin))
	mux.Handle("GET /api/v1/agents/{id}/egress", s.rbac(anyRole, s.handleAgentEgress))
	mux.Handle("PUT /api/v1/agents/{id}/egress/templates/{tid}", s.rbac(securityRoles, s.handleAssignEgressTemplate))
	mux.Handle("DELETE /api/v1/agents/{id}/egress/templates/{tid}", s.rbac(securityRoles, s.handleUnassignEgressTemplate))
	mux.Handle("POST /api/v1/agents/{id}/egress/hosts", s.rbac(securityRoles, s.handleAddAgentEgressHost))
	mux.Handle("DELETE /api/v1/agents/{id}/egress/hosts/{hid}", s.rbac(securityRoles, s.handleDeleteAgentEgressHost))
	mux.Handle("GET /api/v1/secrets", s.rbac(securityRoles, s.handleListSecrets))
	mux.Handle("PUT /api/v1/secrets/{key}", s.rbac(securityRoles, s.handlePutSecret))
	mux.Handle("PATCH /api/v1/secrets/{key}", s.rbac(securityRoles, s.handlePatchSecret))
	mux.Handle("DELETE /api/v1/secrets/{key}", s.rbac(securityRoles, s.handleDeleteSecret))
	mux.Handle("PUT /api/v1/secrets/{key}/agents/{agentID}", s.rbac(securityRoles, s.handleAssignSecret))
	mux.Handle("DELETE /api/v1/secrets/{key}/agents/{agentID}", s.rbac(securityRoles, s.handleUnassignSecret))
	mux.Handle("GET /api/v1/agents/{id}/secrets", s.rbac(securityRoles, s.handleListAgentSecrets))
	mux.Handle("PUT /api/v1/agents/{id}/secrets/{key}", s.rbac(securityRoles, s.handlePutAgentSecret))
	mux.Handle("PATCH /api/v1/agents/{id}/secrets/{key}", s.rbac(securityRoles, s.handlePatchAgentSecret))
	mux.Handle("DELETE /api/v1/agents/{id}/secrets/{key}", s.rbac(securityRoles, s.handleDeleteAgentSecret))
	mux.Handle("POST /api/v1/fleet/kill", s.rbac(securityRoles, s.handleFleetKill))
	mux.Handle("POST /api/v1/fleet/resume", s.rbac(securityRoles, s.handleFleetResume))
	mux.Handle("GET /api/v1/fleet", s.rbac(anyRole, s.handleFleetStatus))

	// Vorlagen-Bibliothek.
	mux.Handle("GET /api/v1/templates", s.rbac(anyRole, s.handleListTemplates))
	mux.Handle("GET /api/v1/templates/{id}", s.rbac(anyRole, s.handleGetTemplate))
	mux.Handle("POST /api/v1/templates", s.rbac(manage, s.handleSaveTemplate))
	mux.Handle("DELETE /api/v1/templates/{id}", s.rbac(manage, s.handleDeleteTemplate))
	mux.Handle("POST /api/v1/templates/{id}/instantiate", s.rbac(manage, s.handleInstantiateTemplate))

	// Administration: Benutzer- und Mandanten-Verwaltung (nur platform_admin).
	adminOnly := []string{identity.RolePlatformAdmin}
	mux.Handle("GET /api/v1/users", s.rbac(adminOnly, s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.rbac(adminOnly, s.handleCreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", s.rbac(adminOnly, s.handleUpdateUser))
	mux.Handle("DELETE /api/v1/users/{id}", s.rbac(adminOnly, s.handleDeleteUser))
	mux.Handle("GET /api/v1/orgs", s.rbac(adminOnly, s.handleListOrgs))
	mux.Handle("POST /api/v1/orgs", s.rbac(adminOnly, s.handleCreateOrg))
	mux.Handle("PATCH /api/v1/orgs/{id}", s.rbac(adminOnly, s.handleUpdateOrg))
	mux.Handle("DELETE /api/v1/orgs/{id}", s.rbac(adminOnly, s.handleDeleteOrg))

	// Live-Updates.
	mux.Handle("GET /api/v1/events", s.auth(s.handleSSE))

	// Request-Log (Plattform-Diagnose): die HTTP-Requests an den Rändern.
	mux.Handle("GET /api/v1/platform/requests", s.rbac(securityRoles, s.handleListRequests))
	mux.Handle("GET /api/v1/platform/requests/{id}", s.rbac(securityRoles, s.handleGetRequest))
	mux.Handle("DELETE /api/v1/platform/requests", s.rbac(securityRoles, s.handleClearRequests))

	// Maschinen-Endpunkte (eigene Auth: HMAC, Trigger-Token bzw. Daemon-JWT).
	// Beide gehen durch logIncoming — auch (und gerade) wenn sie abgelehnt
	// werden: ein Webhook, der an der Signatur scheitert, ist die häufigste
	// Frage beim Anbinden eines Zielsystems.
	mux.HandleFunc("POST /api/webhooks/{system}/{agent}", s.logIncoming(s.handleTargetWebhook))
	mux.HandleFunc("POST /api/trigger/{token}", s.logIncoming(s.handleAgentTrigger))
	mux.HandleFunc("GET /api/daemon/ws", s.handleDaemonWS)

	// Eingebettete SPA mit Fallback auf index.html (client-seitiges Routing).
	if s.WebFS != nil {
		mux.Handle("/", spaHandler(s.WebFS))
	}
	return mux
}

// --- Hilfen ---

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 4<<20)).Decode(v)
}

func mapErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, agents.ErrNotFound), errors.Is(err, backlog.ErrNotFound),
		errors.Is(err, observability.ErrNotFound), errors.Is(err, secrets.ErrNotFound),
		errors.Is(err, org.ErrNotFound), errors.Is(err, org.ErrDeptNotFound),
		errors.Is(err, pgx.ErrNoRows):
		writeErr(w, http.StatusNotFound, "nicht gefunden")
	case errors.Is(err, backlog.ErrInvalidTransition),
		errors.Is(err, org.ErrLastAdmin), errors.Is(err, org.ErrEmailTaken),
		errors.Is(err, org.ErrManagerCycle):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// --- Session-Auth (builtin: Cookie + Hash in Postgres) ---

type ctxKey string

const principalKey ctxKey = "principal"

func principalFrom(r *http.Request) identity.Principal {
	p, _ := r.Context().Value(principalKey).(identity.Principal)
	return p
}

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func (s *Server) auth(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("covey_session")
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "nicht angemeldet")
			return
		}
		var p identity.Principal
		err = s.Pool.QueryRow(r.Context(), `SELECT h.id, h.org_id, h.email, h.display_name, h.role
			FROM http_sessions s JOIN humans h ON h.id=s.human_id
			WHERE s.token_hash=$1 AND s.expires_at > now()`, hashToken(cookie.Value)).
			Scan(&p.ID, &p.OrgID, &p.Email, &p.DisplayName, &p.Role)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session abgelaufen")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

// rbac erzwingt die rollen-gescopten Rechte (spec/09).
func (s *Server) rbac(roles []string, next http.HandlerFunc) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		for _, role := range roles {
			if p.Role == role {
				next(w, r)
				return
			}
		}
		writeErr(w, http.StatusForbidden, "rolle "+p.Role+" hat hier keine Rechte")
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "ungültiger request")
		return
	}
	now := time.Now()
	key := loginKey(r, in.Email)
	if s.loginLimiter.blocked(key, now) {
		writeErr(w, http.StatusTooManyRequests, "zu viele Fehlversuche — bitte später erneut versuchen")
		return
	}
	p, err := s.Identity.AuthenticateHuman(r.Context(), identity.Credentials{Email: in.Email, Password: in.Password})
	if err != nil {
		s.loginLimiter.fail(key, now)
		writeErr(w, http.StatusUnauthorized, "ungültige Zugangsdaten")
		return
	}
	s.loginLimiter.reset(key)
	buf := make([]byte, 32)
	rand.Read(buf)
	token := hex.EncodeToString(buf)
	if _, err := s.Pool.Exec(r.Context(), `INSERT INTO http_sessions (token_hash, human_id, expires_at)
		VALUES ($1,$2,$3)`, hashToken(token), p.ID, time.Now().Add(s.SessionTTL)); err != nil {
		mapErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "covey_session", Value: token, Path: "/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: int(s.SessionTTL.Seconds())})
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("covey_session"); err == nil {
		s.Pool.Exec(r.Context(), "DELETE FROM http_sessions WHERE token_hash=$1", hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "covey_session", Value: "", Path: "/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, principalFrom(r))
}

func parseID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}
