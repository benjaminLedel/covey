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
	"covey/internal/audit"
	"covey/internal/backlog"
	"covey/internal/buildinfo"
	"covey/internal/dream"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/secrets"
	"covey/internal/skills"
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
	Dreams   *dream.Store
	Org      *org.Store
	Targets  *targetstore.Store
	// Skills sind die Fähigkeiten der Agenten (Bibliothek + agent-eigen).
	// nil = Feature abgeschaltet; die Skill-Routen antworten dann mit 503
	// (dieselbe Bedeutung wie orchestrator.Options.Skills == nil).
	Skills *skills.Store
	Orch   *orchestrator.Orchestrator
	WebFS  fs.FS // dist der SPA; nil = nur API
	Log    *slog.Logger

	// Egress (UI-verwaltet). EgressStore ist immer gesetzt; EgressEnforced
	// spiegelt die Prozess-Config, EgressDefaults sind die ENV-Zusätze aus
	// COVEY_EGRESS_ALLOW (nur per Config änderbar). Die fachliche Basis-
	// Allowlist liegt konfigurierbar in der DB (egress_default_hosts). Der
	// Proxy lädt Änderungen selbst nach (Resolver-Cache mit TTL).
	EgressStore    *egress.Store
	EgressEnforced bool
	EgressDefaults []string

	Templates *templates.Store

	// Audit hält die Verwaltungshandlungen von Menschen fest (internal/audit).
	// nil = kein Audit (Tests); die Middleware ist dann still.
	Audit *audit.Store

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
	// SiteURL ist die Adresse der öffentlichen Website (canonical, hreflang,
	// sitemap.xml). Leer = aus dem Request ableiten. Getrennt von PublicURL,
	// weil die eine nach außen zeigt und die andere zu den Sandboxen.
	SiteURL string
	// CookieSecure setzt das Secure-Flag auf dem Session-Cookie (HTTPS-only).
	CookieSecure bool

	// BaseCtx ist der Lebenszyklus des Servers. Hintergrund-Arbeit, die einen
	// Request überdauern soll (der Nachtlauf eines Agenten etwa), hängt daran
	// statt an context.Background() — sonst liefe sie nach einem Shutdown
	// weiter, ohne dass jemand auf sie wartet oder sie abbrechen könnte.
	// nil = kein Lebenszyklus bekannt (Tests); dann greift context.Background().
	BaseCtx context.Context

	// loginLimiter bremst Brute-Force auf /auth/login, webhookLimiter die
	// unauthentifizierten Webhook-Endpunkte (beide lazy in Handler init.).
	loginLimiter   *loginLimiter
	webhookLimiter *webhookLimiter

	// seo ist die Landkarte der öffentlichen Website aus dist/seo.json
	// (internal/httpapi/seo.go). Sie trägt die Adressen für sitemap.xml und
	// die Pfad-Präfixe der angemeldeten Oberfläche.
	seo seoIndex
}

func (s *Server) Handler() http.Handler {
	if s.loginLimiter == nil {
		s.loginLimiter = newLoginLimiter()
	}
	if s.webhookLimiter == nil {
		s.webhookLimiter = newWebhookLimiter()
	}
	s.seo = ladeSEOIndex(s.WebFS)
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

	// Erste Schritte als Checkliste über den echten Org-Zustand (onboarding.go).
	// Die Audit-Spur lesen dürfen die, die sie angeht: Plattform-Admin,
	// Security und Auditor. Agent-Owner und Controlling nicht — sie stehen
	// selbst darin.
	mux.Handle("GET /api/v1/audit", s.rbac([]string{identity.RolePlatformAdmin,
		identity.RoleSecurity, identity.RoleAuditor}, s.handleAuditLog))
	mux.Handle("GET /api/v1/onboarding", s.rbac(anyRole, s.handleOnboarding))
	mux.Handle("GET /api/v1/agents", s.rbac(anyRole, s.handleListAgents))
	mux.Handle("POST /api/v1/agents", s.rbac(manage, s.handleCreateAgent))
	mux.Handle("GET /api/v1/agents/{id}", s.agentScoped(anyRole, s.handleGetAgent))
	mux.Handle("DELETE /api/v1/agents/{id}", s.agentScoped(manage, s.handleDeleteAgent))
	mux.Handle("GET /api/v1/agents/{id}/config", s.agentScoped(anyRole, s.handleGetConfig))
	mux.Handle("GET /api/v1/agents/{id}/export", s.agentScoped(append(manage, identity.RoleSecurity), s.handleExportAgent))
	mux.Handle("GET /api/v1/agents/{id}/diagnostics", s.agentScoped(append(manage, identity.RoleSecurity), s.handleAgentDiagnostics))
	// Der Arbeitsplatz: das persistente Home als Dateibaum (files.go). Lesen
	// darf zusätzlich Security — wer einen Agenten untersucht, muss sehen, was
	// bei ihm liegt. Schreiben bleibt bei den Verwaltern: eine Datei im Home
	// ist Konfiguration des Agenten, kein Audit-Vorgang.
	mux.Handle("GET /api/v1/agents/{id}/files", s.agentScoped(append(manage, identity.RoleSecurity), s.handleListFiles))
	mux.Handle("GET /api/v1/agents/{id}/files/content", s.agentScoped(append(manage, identity.RoleSecurity), s.handleReadFile))
	mux.Handle("GET /api/v1/agents/{id}/files/download", s.agentScoped(append(manage, identity.RoleSecurity), s.handleDownloadFile))
	mux.Handle("GET /api/v1/agents/{id}/files/preview", s.agentScoped(append(manage, identity.RoleSecurity), s.handlePreviewFile))
	mux.Handle("GET /api/v1/agents/{id}/files/zip", s.agentScoped(append(manage, identity.RoleSecurity), s.handleZipFiles))
	mux.Handle("PUT /api/v1/agents/{id}/files/content", s.agentScoped(manage, s.handleWriteFile))
	mux.Handle("POST /api/v1/agents/{id}/files/upload", s.agentScoped(manage, s.handleUploadFiles))
	mux.Handle("POST /api/v1/agents/{id}/files/dir", s.agentScoped(manage, s.handleMkdir))
	mux.Handle("POST /api/v1/agents/{id}/files/move", s.agentScoped(manage, s.handleMoveFile))
	mux.Handle("DELETE /api/v1/agents/{id}/files", s.agentScoped(manage, s.handleDeleteFile))
	mux.Handle("GET /api/v1/skills/covey-agent.zip", s.rbac(anyRole, s.handleDownloadSkill))
	mux.Handle("POST /api/v1/agents/import", s.rbac(manage, s.handleImportAgent))
	mux.Handle("PUT /api/v1/agents/{id}/config", s.agentScoped(manage, s.handlePutConfig))
	// Bestehenden Agenten aus einem Bundle überschreiben — nur die Config-Dateien.
	mux.Handle("POST /api/v1/agents/{id}/config/import", s.agentScoped(manage, s.handleImportConfig))
	// KI-Assistent zum Anpassen von Agenten (Config-Copilot, FR-001): nur
	// verfügbar, wenn org-weit ein Claude-Credential hinterlegt ist.
	mux.Handle("GET /api/v1/assist/status", s.rbac(anyRole, s.handleAssistStatus))
	mux.Handle("POST /api/v1/agents/{id}/config/assist", s.agentScoped(manage, s.handleConfigAssist))
	mux.Handle("GET /api/v1/agents/{id}/heartbeats", s.agentScoped(anyRole, s.handleHeartbeats))
	mux.Handle("POST /api/v1/agents/{id}/heartbeats/{name}/fire", s.agentScoped(manage, s.handleFireHeartbeat))
	mux.Handle("GET /api/v1/agents/{id}/backlog", s.agentScoped(anyRole, s.handleBacklog))
	mux.Handle("POST /api/v1/agents/{id}/tasks", s.agentScoped(manage, s.handleCreateTask))
	mux.Handle("POST /api/v1/agents/{id}/wake", s.agentScoped(manage, s.handleWake))
	mux.Handle("POST /api/v1/agents/{id}/kill", s.agentScoped(append(manage, identity.RoleSecurity), s.handleKill))
	mux.Handle("POST /api/v1/agents/{id}/resume", s.agentScoped(append(manage, identity.RoleSecurity), s.handleResumeAgent))
	mux.Handle("POST /api/v1/agents/{id}/budget", s.agentScoped(manage, s.handleSetBudget))
	mux.Handle("PATCH /api/v1/agents/{id}/name", s.agentScoped(manage, s.handleRename))
	mux.Handle("PATCH /api/v1/agents/{id}/profile", s.agentScoped(manage, s.handleUpdateAgentProfile))
	mux.Handle("PATCH /api/v1/agents/{id}/slug", s.agentScoped(manage, s.handleSetSlug))
	mux.Handle("PATCH /api/v1/agents/{id}/runtime", s.agentScoped(manage, s.handleSetRuntime))
	mux.Handle("PATCH /api/v1/agents/{id}/model", s.agentScoped(manage, s.handleSetModel))
	mux.Handle("PATCH /api/v1/agents/{id}/max-turns", s.agentScoped(manage, s.handleSetMaxTurns))
	mux.Handle("PATCH /api/v1/agents/{id}/recording-level", s.agentScoped(manage, s.handleSetRecordingLevel))
	mux.Handle("PATCH /api/v1/agents/{id}/warm-sandbox", s.agentScoped(manage, s.handleSetWarmSandbox))
	mux.Handle("GET /api/v1/org/recording-level", s.rbac(anyRole, s.handleGetOrgRecording))
	mux.Handle("PATCH /api/v1/org/recording-level", s.rbac(securityRoles, s.handleSetOrgRecording))
	mux.Handle("PATCH /api/v1/agents/{id}/supervisor", s.agentScoped(manage, s.handleSetSupervisor))
	mux.Handle("PATCH /api/v1/agents/{id}/department", s.agentScoped(manage, s.handleSetAgentDepartment))
	mux.Handle("PATCH /api/v1/org/humans/{id}/department", s.rbac(manage, s.handleSetHumanDepartment))
	mux.Handle("PATCH /api/v1/org/humans/{id}/manager", s.rbac(manage, s.handleSetHumanManager))
	mux.Handle("GET /api/v1/departments", s.rbac(anyRole, s.handleListDepartments))
	mux.Handle("POST /api/v1/departments", s.rbac(manage, s.handleCreateDepartment))
	mux.Handle("PATCH /api/v1/departments/{id}/name", s.rbac(manage, s.handleRenameDepartment))
	mux.Handle("PATCH /api/v1/departments/{id}/color", s.rbac(manage, s.handleSetDepartmentColor))
	mux.Handle("DELETE /api/v1/departments/{id}", s.rbac(manage, s.handleDeleteDepartment))
	mux.Handle("POST /api/v1/departments/{id}/leads", s.rbac(manage, s.handleAddDepartmentLead))
	mux.Handle("DELETE /api/v1/departments/{id}/leads/{member}", s.rbac(manage, s.handleRemoveDepartmentLead))
	mux.Handle("GET /api/v1/agents/{id}/webhook", s.agentScoped(manage, s.handleGetAgentWebhook))
	mux.Handle("POST /api/v1/agents/{id}/webhook", s.agentScoped(manage, s.handleEnableAgentWebhook))
	mux.Handle("DELETE /api/v1/agents/{id}/webhook", s.agentScoped(manage, s.handleDisableAgentWebhook))
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
	// Was der Agent in welchem Zielsystem tun kann — Plugin, Zugang und
	// Aktionsliste an einer Stelle (targets.go).
	mux.Handle("GET /api/v1/agents/{id}/systems", s.agentScoped(anyRole, s.handleAgentSystems))
	mux.Handle("GET /api/v1/agents/{id}/tools/{system}", s.agentScoped(anyRole, s.handleGetAgentTools))
	mux.Handle("PUT /api/v1/agents/{id}/tools/{system}", s.agentScoped(securityRoles, s.handleSetAgentTools))
	mux.Handle("GET /api/v1/agents/{id}/recording", s.agentScoped(anyRole, s.handleRecording))
	mux.Handle("GET /api/v1/recordings/blobs/{id}", s.rbac(anyRole, s.handleRecordingBlob))
	mux.Handle("GET /api/v1/agents/{id}/cost", s.agentScoped(anyRole, s.handleCost))
	mux.Handle("GET /api/v1/agents/{id}/cost/series", s.agentScoped(anyRole, s.handleCostSeries))
	mux.Handle("GET /api/v1/cost/org", s.rbac(anyRole, s.handleOrgCost))
	mux.Handle("GET /api/v1/agents/{id}/memories", s.agentScoped(anyRole, s.handleMemories))
	mux.Handle("POST /api/v1/agents/{id}/memories", s.agentScoped(manage, s.handleCreateMemory))
	mux.Handle("GET /api/v1/agents/{id}/wiki/log", s.agentScoped(anyRole, s.handleWikiLog))
	mux.Handle("GET /api/v1/agents/{id}/wiki/health", s.agentScoped(anyRole, s.handleWikiHealth))
	mux.Handle("POST /api/v1/agents/{id}/wiki/consolidate", s.agentScoped(manage, s.handleWikiConsolidate))
	mux.Handle("POST /api/v1/agents/{id}/dreams", s.agentScoped(manage, s.handleStartDream))
	mux.Handle("GET /api/v1/agents/{id}/dreams", s.agentScoped(anyRole, s.handleListDreams))
	mux.Handle("POST /api/v1/dream-actions/{id}/undo", s.rbac(manage, s.handleUndoDreamAction))
	mux.Handle("PATCH /api/v1/memories/{id}", s.pageScoped(manage, s.handleUpdateMemory))
	mux.Handle("DELETE /api/v1/memories/{id}", s.pageScoped(manage, s.handleDeleteMemory))
	mux.Handle("POST /api/v1/tasks/{id}/cancel", s.taskScoped(manage, s.handleCancelTask))
	mux.Handle("POST /api/v1/tasks/{id}/retry", s.taskScoped(manage, s.handleRetryTask))
	mux.Handle("POST /api/v1/tasks/{id}/archive", s.taskScoped(manage, s.handleArchiveTask))
	mux.Handle("POST /api/v1/agents/{id}/backlog/cleanup", s.agentScoped(manage, s.handleCleanupBacklog))
	mux.Handle("POST /api/v1/tasks/{id}/stage", s.taskScoped(manage, s.handleMoveTask))
	mux.Handle("GET /api/v1/tasks/{id}/transitions", s.taskScoped(anyRole, s.handleTransitions))
	mux.Handle("GET /api/v1/tasks/{id}/notes", s.taskScoped(anyRole, s.handleTaskNotes))

	// Custom-Stages (Kanban-Overlay, pro Agent).
	mux.Handle("GET /api/v1/agents/{id}/stages", s.agentScoped(anyRole, s.handleListStages))
	mux.Handle("POST /api/v1/agents/{id}/stages", s.agentScoped(manage, s.handleCreateStage))
	mux.Handle("POST /api/v1/agents/{id}/stages/reorder", s.agentScoped(manage, s.handleReorderStages))
	mux.Handle("PATCH /api/v1/stages/{id}", s.stageScoped(manage, s.handleUpdateStage))
	mux.Handle("DELETE /api/v1/stages/{id}", s.stageScoped(manage, s.handleDeleteStage))

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
	mux.Handle("GET /api/v1/agents/{id}/egress", s.agentScoped(anyRole, s.handleAgentEgress))
	mux.Handle("PUT /api/v1/agents/{id}/egress/templates/{tid}", s.agentScoped(securityRoles, s.handleAssignEgressTemplate))
	mux.Handle("DELETE /api/v1/agents/{id}/egress/templates/{tid}", s.agentScoped(securityRoles, s.handleUnassignEgressTemplate))
	mux.Handle("POST /api/v1/agents/{id}/egress/hosts", s.agentScoped(securityRoles, s.handleAddAgentEgressHost))
	mux.Handle("DELETE /api/v1/agents/{id}/egress/hosts/{hid}", s.agentScoped(securityRoles, s.handleDeleteAgentEgressHost))
	mux.Handle("GET /api/v1/secrets", s.rbac(securityRoles, s.handleListSecrets))
	mux.Handle("PUT /api/v1/secrets/{key}", s.rbac(securityRoles, s.handlePutSecret))
	mux.Handle("PATCH /api/v1/secrets/{key}", s.rbac(securityRoles, s.handlePatchSecret))
	mux.Handle("DELETE /api/v1/secrets/{key}", s.rbac(securityRoles, s.handleDeleteSecret))
	mux.Handle("PUT /api/v1/secrets/{key}/agents/{agentID}", s.rbac(securityRoles, s.handleAssignSecret))
	mux.Handle("DELETE /api/v1/secrets/{key}/agents/{agentID}", s.rbac(securityRoles, s.handleUnassignSecret))
	mux.Handle("GET /api/v1/agents/{id}/secrets", s.agentScoped(securityRoles, s.handleListAgentSecrets))
	mux.Handle("PUT /api/v1/agents/{id}/secrets/{key}", s.agentScoped(securityRoles, s.handlePutAgentSecret))
	mux.Handle("PATCH /api/v1/agents/{id}/secrets/{key}", s.agentScoped(securityRoles, s.handlePatchAgentSecret))
	mux.Handle("DELETE /api/v1/agents/{id}/secrets/{key}", s.agentScoped(securityRoles, s.handleDeleteAgentSecret))
	mux.Handle("POST /api/v1/fleet/kill", s.rbac(securityRoles, s.handleFleetKill))
	mux.Handle("POST /api/v1/fleet/resume", s.rbac(securityRoles, s.handleFleetResume))
	mux.Handle("GET /api/v1/fleet", s.rbac(anyRole, s.handleFleetStatus))

	// Agenten-Skills: Org-Bibliothek, Verlinkung, agent-eigene Fähigkeiten
	// (agentskills.go). Lesen darf jede Rolle — Skills sind Prozeduren, keine
	// Geheimnisse, und stehen damit auf einer Stufe mit der Agenten-Config.
	// Die Route /api/v1/skills/covey-agent.zip weiter oben ist etwas anderes
	// (der Claude-Code-Skill zum Herunterladen) und geht als literales Segment
	// dem {id}-Platzhalter vor.
	mux.Handle("GET /api/v1/skills", s.rbac(anyRole, s.handleListSkills))
	mux.Handle("POST /api/v1/skills", s.rbac(manage, s.handleCreateSkill))
	mux.Handle("GET /api/v1/skills/{id}", s.rbac(anyRole, s.handleGetSkill))
	mux.Handle("PUT /api/v1/skills/{id}", s.rbac(manage, s.handlePutSkill))
	mux.Handle("DELETE /api/v1/skills/{id}", s.rbac(manage, s.handleDeleteSkill))
	mux.Handle("PUT /api/v1/skills/{id}/agents/{agentID}", s.rbac(manage, s.handleAssignSkill))
	mux.Handle("DELETE /api/v1/skills/{id}/agents/{agentID}", s.rbac(manage, s.handleUnassignSkill))
	mux.Handle("GET /api/v1/agents/{id}/skills", s.agentScoped(anyRole, s.handleAgentSkills))
	mux.Handle("POST /api/v1/agents/{id}/skills", s.agentScoped(manage, s.handleCreateAgentSkill))

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

	// Eingebettete SPA samt vorgerenderter öffentlicher Website.
	if s.WebFS != nil {
		mux.HandleFunc("GET /robots.txt", s.handleRobots)
		mux.HandleFunc("GET /sitemap.xml", s.handleSitemap)
		mux.Handle("/", s.spaHandler(s.WebFS))
	}
	// Die Audit-Spur liegt INNEN, direkt um den Mux: Sie braucht den
	// Principal, den die auth-Middleware je Route setzt, und den Statuscode,
	// den der Handler schreibt.
	// Die Schutz-Header auf ALLES legen, nicht nur auf die Oberfläche: Auch
	// eine 404 und jede API-Antwort kommen von derselben Origin, und ein
	// Endpunkt, der sie vergisst, ist genau die Lücke, die man nicht sucht.
	// Handler mit eigenen Vorstellungen (die Datei-Vorschau setzt eine
	// strengere CSP) überschreiben sie danach schlicht.
	return s.mitSchutzHeadern(s.mitAuditSpur(mux))
}

// mitSchutzHeadern setzt die Sicherheits-Kopfzeilen vor jeder Antwort.
func (s *Server) mitSchutzHeadern(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setzeSchutzHeader(w)
		// HSTS nur bei einer Instanz, die tatsächlich per HTTPS läuft
		// (CookieSecure ist derselbe Schalter). Auf einer lokalen
		// HTTP-Instanz würde der Header den Browser für Monate auf https
		// festnageln und den Zugang verbauen.
		if s.CookieSecure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// --- Hilfen ---

// baseCtx ist der Lebenszyklus des Servers, mit Rückfall für Tests.
func (s *Server) baseCtx() context.Context {
	if s.BaseCtx != nil {
		return s.BaseCtx
	}
	return context.Background()
}

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
		p, err := s.sessions().Principal(r.Context(), hashToken(cookie.Value))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session abgelaufen")
			return
		}
		// Den Akteur zusätzlich an die Audit-Middleware zurückmelden: Sie
		// liegt weiter außen und sieht den Context nicht, den wir hier anlegen.
		if h, ok := r.Context().Value(akteurKey).(*akteurHalter); ok {
			h.p = p
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

// agentScoped ist rbac PLUS der Nachweis, dass der Agent aus der URL zur
// Organisation des Anrufers gehört. Jede Route unter /agents/{id}/… läuft
// darüber.
//
// Der Grund für eine Middleware statt einer Zeile im Handler: Diese Prüfung
// war zuvor Sache jedes einzelnen Handlers — und bei 25 von 67 schlicht
// vergessen. Fremde Config, fremdes Backlog, fremdes Recording, fremde Kosten
// waren damit lesbar, wake/kill/budget sogar auslösbar. Eine Grenze, an die
// sich 67 Aufrufer erinnern müssen, ist keine Grenze; eine, an der man
// vorbeikommen MUSS, schon.
//
// Der aufgelöste Agent liegt danach im Context — die Handler brauchen ihn
// meist ohnehin und sparen sich die zweite Abfrage.
func (s *Server) agentScoped(roles []string, next http.HandlerFunc) http.Handler {
	return s.rbac(roles, func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "ungültige id")
			return
		}
		p := principalFrom(r)
		agent, err := s.Registry.Get(r.Context(), id)
		// Fremd oder nicht vorhanden ist dieselbe Antwort: Ob es einen Agenten
		// mit dieser ID irgendwo gibt, geht eine andere Organisation nichts an.
		if err != nil || agent.OrgID != p.OrgID {
			writeErr(w, http.StatusNotFound, "agent nicht gefunden")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), agentKey, agent)))
	})
}

// taskScoped/stageScoped/pageScoped sind dasselbe Muster wie agentScoped für
// die übrigen Objekte mit eigener ID. Aufgaben, Board-Spalten und Wiki-Seiten
// waren aus fremden Organisationen les- und veränderbar, weil die Prüfung auch
// hier Sache des Handlers war — und dort fehlte.
//
// Bewusst drei kleine Funktionen statt einer generischen: Die Frage „gehört
// das mir?" wird je Objekt anders beantwortet (Aufgaben tragen die
// Organisation selbst, Spalten und Seiten erst über ihren Agenten). Ein
// Generikum müsste das doch wieder aufdröseln.
func (s *Server) taskScoped(roles []string, next http.HandlerFunc) http.Handler {
	return s.idScoped(roles, next, func(r *http.Request, orgID, id uuid.UUID) bool {
		return s.Backlog.InOrg(r.Context(), orgID, id)
	})
}

func (s *Server) stageScoped(roles []string, next http.HandlerFunc) http.Handler {
	return s.idScoped(roles, next, func(r *http.Request, orgID, id uuid.UUID) bool {
		return s.Backlog.StageInOrg(r.Context(), orgID, id)
	})
}

func (s *Server) pageScoped(roles []string, next http.HandlerFunc) http.Handler {
	return s.idScoped(roles, next, func(r *http.Request, orgID, id uuid.UUID) bool {
		return s.Memory.PageInOrg(r.Context(), orgID, id)
	})
}

// idScoped ist der gemeinsame Rumpf: ID aus dem Pfad, Zugehörigkeit prüfen,
// sonst „nicht gefunden" — nie „verboten", denn die Existenz eines Objekts in
// einer anderen Organisation ist selbst schon eine Auskunft.
func (s *Server) idScoped(roles []string, next http.HandlerFunc,
	gehoert func(*http.Request, uuid.UUID, uuid.UUID) bool) http.Handler {
	return s.rbac(roles, func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "ungültige id")
			return
		}
		if !gehoert(r, principalFrom(r).OrgID, id) {
			writeErr(w, http.StatusNotFound, "nicht gefunden")
			return
		}
		next(w, r)
	})
}

// agentKey trägt den geprüften Agenten durch den Context.
const agentKey ctxKey = "agent"

// agentFrom liefert den von agentScoped geprüften Agenten. Nur in Handlern
// gültig, die über agentScoped eingehängt sind.
func agentFrom(r *http.Request) agents.Agent {
	a, _ := r.Context().Value(agentKey).(agents.Agent)
	return a
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
	if err := s.sessions().Create(r.Context(), hashToken(token), p.ID, time.Now().Add(s.SessionTTL)); err != nil {
		mapErr(w, err)
		return
	}
	// SameSite=Strict statt Lax: Covey ist ein Verwaltungswerkzeug, in das man
	// sich hineinnavigiert — es gibt keine Deep-Links von fremden Seiten, auf
	// die man Rücksicht nehmen müsste. Lax schickt das Cookie bei jeder
	// Top-Level-Navigation von außen mit; Strict tut das nicht und nimmt damit
	// die ganze Klasse von Angriffen aus dem Spiel, die mit einem Klick auf
	// einen fremden Link beginnen.
	http.SetCookie(w, &http.Cookie{Name: "covey_session", Value: token, Path: "/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: int(s.SessionTTL.Seconds())})
	writeJSON(w, http.StatusOK, p)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("covey_session"); err == nil {
		_ = s.sessions().Delete(r.Context(), hashToken(cookie.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: "covey_session", Value: "", Path: "/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, principalFrom(r))
}

func parseID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}
