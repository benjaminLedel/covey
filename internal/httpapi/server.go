// Package httpapi is the API/BFF part of the binary (spec/10): REST API with
// RBAC, daemon WebSocket, SSE live updates, Zammad webhook and the embedded
// admin SPA.
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
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/accounts"
	"covey/internal/agents"
	"covey/internal/audit"
	"covey/internal/backlog"
	"covey/internal/buildinfo"
	"covey/internal/config"
	"covey/internal/dream"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/homestore"
	"covey/internal/identity"
	"covey/internal/marketplace"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/runner"
	runnerstore "covey/internal/runner/store"
	"covey/internal/runtimes"
	"covey/internal/sandbox"
	"covey/internal/secrets"
	"covey/internal/settings"
	"covey/internal/skills"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"
	"covey/internal/waitlist"
	"covey/internal/workplaces"
)

type Server struct {
	Pool     *pgxpool.Pool
	Registry *agents.Registry
	Backlog  *backlog.Store
	Obs      *observability.Store
	Rails    *guardrails.Store
	Secrets  secrets.Store
	Runtimes *runtimes.Store
	Identity identity.Provider
	Memory   *memory.Store
	Dreams   *dream.Store
	Org      *org.Store
	Targets  *targetstore.Store
	// Marketplace is the plugin catalogue the store offers installs from
	// (spec/22). nil / empty URL = no catalogue configured.
	Marketplace *marketplace.Client
	// Workplaces is the published workplace catalogue (spec/16) — which image
	// belongs to which covey version. nil = none configured; then the
	// compiled defaults and the environment stand.
	Workplaces *sandbox.Source
	// OrgWorkplaces are the workplaces an organisation brought along itself —
	// an image of its own under a name of its own (spec/16).
	OrgWorkplaces *workplaces.Store
	// Settings are the instance's own switches (internal/settings).
	// nil = the defaults apply, which for signup.mode means: closed.
	Settings *settings.Store
	// Accounts and Waitlist carry self-registration (FR-002). nil = the public
	// sign-up endpoints answer 404 — an instance that cannot register anybody
	// does not advertise the attempt.
	Accounts *accounts.Store
	Waitlist *waitlist.Store
	// Skills are the agents' capabilities (library + agent-owned).
	// nil = feature switched off; the skill routes then answer with 503
	// (the same meaning as orchestrator.Options.Skills == nil).
	Skills *skills.Store
	Orch   *orchestrator.Orchestrator
	WebFS  fs.FS // dist of the SPA; nil = API only
	Log    *slog.Logger

	// Egress (UI-managed). EgressStore is always set; EgressEnforced mirrors
	// the process config, EgressDefaults are the ENV additions from
	// COVEY_EGRESS_ALLOW (changeable only via config). The functional base
	// allowlist sits configurably in the DB (egress_default_hosts). The proxy
	// reloads changes by itself (resolver cache with TTL).
	EgressStore    *egress.Store
	EgressEnforced bool
	EgressDefaults []string

	// Config is the process configuration — read by the platform diagnostics,
	// which answer what a restart would run into here (images, store, egress).
	// nil = the question is left unasked rather than answered by guessing.
	Config *config.Config

	// Runners are the execution nodes (spec/16). They authenticate with their
	// own token against /api/runner/v1/… — the only interface they have to the
	// platform. nil = the runner API answers 503 (tests).
	Runners *runnerstore.Store
	// RunnerPool is the control plane's side of the protocol: a foreign runner
	// that connects is taken into it. nil = only the built-in one (tests).
	RunnerPool *runner.Pool
	// RunnerDownloadBase is where a runner fetches its new binary from when it
	// is told to update itself. Empty = the releases of the public repository,
	// which is where install.sh gets it too. Set for an installation that does
	// not reach GitHub, or that publishes its own builds.
	RunnerDownloadBase string
	// Blobs is the home store. A remote runner reaches its blocks through the
	// runner API — it never gets the store's credentials (spec/16).
	Blobs homestore.BlobStore
	// storeSizes caches the store's fill level per organisation — walking the
	// block directory is a disk pass, and the dashboard asks on every visit.
	storeSizes storeSizeCache

	Templates *templates.Store

	// Audit records the administrative actions of humans (internal/audit).
	// nil = no audit (tests); the middleware then stays silent.
	Audit *audit.Store

	// ReqLog logs the HTTP requests at the edges (webhooks in, target-system
	// calls out). nil = switched off (COVEY_REQUEST_LOG=false).
	ReqLog *reqlogstore.Store

	// DataPlane answers whether a sandbox could be started at all — the first
	// steps read it so that a setup which cannot run anything says so on the
	// agent overview instead of in the recording of a task that never ran.
	// nil = the provider cannot say (tests, future providers); the question is
	// then left unasked rather than answered by guessing.
	DataPlane orchestrator.DataPlaneChecker
	dataPlane cachedCheck

	// WebhookSecrets: target-system name → signature secret
	// (ENV COVEY_<SYSTEM>_WEBHOOK_SECRET, e.g. COVEY_ZAMMAD_WEBHOOK_SECRET).
	WebhookSecrets map[string]string
	SessionTTL     time.Duration
	// SiteURL is this instance's externally reachable address: canonical and
	// sitemap.xml of the website, the webhook and trigger URLs to copy, the
	// target URL in the downloadable skill. Empty = derive it from the request
	// (origin() in seo.go), which suffices behind a clean reverse proxy.
	//
	// cfg.PublicURL deliberately does NOT belong here: that is the address over
	// which sandboxes reach the control plane — with the docker provider mostly
	// a loopback. Showing it in an HTTP response would always be wrong, and as
	// long as the server does not even know it, that cannot happen.
	SiteURL string
	// CookieSecure sets the Secure flag on the session cookie (HTTPS only).
	CookieSecure bool
	// TrustedProxies are the addresses whose X-Forwarded-For is believed —
	// empty (the default) means: none, the peer address counts. It decides who
	// a rate limit and an audit entry are attributed to (clientIP in
	// ratelimit.go, COVEY_TRUSTED_PROXIES in the config).
	TrustedProxies []netip.Prefix

	// BaseCtx is the server's lifecycle. Background work that is meant to
	// outlive a request (an agent's night run, say) hangs off it instead of
	// context.Background() — otherwise it would keep running after a shutdown
	// without anyone waiting for it or being able to cancel it.
	// nil = no lifecycle known (tests); context.Background() then applies.
	BaseCtx context.Context

	// loginLimiter slows brute force on /auth/login, webhookLimiter the
	// unauthenticated webhook endpoints (both initialized lazily in Handler).
	loginLimiter   *loginLimiter
	signupLimiter  *webhookLimiter
	webhookLimiter *webhookLimiter

	// routen is the route list from dist/app-routes.json
	// (internal/httpapi/approutes.go): which paths the SPA shell answers and
	// which ones are an honest 404.
	routen appRouten
}

func (s *Server) Handler() http.Handler {
	if s.loginLimiter == nil {
		s.loginLimiter = newLoginLimiter()
	}
	if s.signupLimiter == nil {
		s.signupLimiter = newSignupLimiter()
	}
	if s.webhookLimiter == nil {
		s.webhookLimiter = newWebhookLimiter()
	}
	s.routen = ladeAppRouten(s.WebFS)
	mux := http.NewServeMux()

	// Health/readiness (without auth).
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Pool.Ping(r.Context()); err != nil {
			http.Error(w, "db not reachable", http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte("ready"))
	})

	// Provenance of the running binary (version, commit, build time) — the
	// footer of the UI shows it. Signed in, not public: which commit runs on an
	// instance is nobody else's business.
	mux.Handle("GET /api/v1/version", s.auth(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, buildinfo.Get())
	}))

	// The public website's own question, without a session: does this
	// installation accept registrations (public.go).
	mux.HandleFunc("GET /api/v1/public/signup-state", s.handleSignupState)
	mux.HandleFunc("POST /api/v1/public/signup", s.handleSignup)

	// Auth.
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.HandleFunc("POST /api/v1/auth/logout", s.handleLogout)
	mux.Handle("GET /api/v1/auth/me", s.auth(s.handleMe))
	mux.Handle("PATCH /api/v1/auth/me", s.auth(s.handleUpdateMe))
	mux.Handle("GET /api/v1/auth/me/profile", s.auth(s.handleMyProfile))
	mux.Handle("GET /api/v1/auth/sessions", s.auth(s.handleListSessions))
	mux.Handle("DELETE /api/v1/auth/sessions", s.auth(s.handleRevokeOtherSessions))
	// API keys: readable with either badge, mintable and revocable only with
	// the session — see sessionOnly.
	mux.Handle("GET /api/v1/auth/api-keys", s.auth(s.handleListAPIKeys))
	mux.Handle("POST /api/v1/auth/api-keys", s.auth(s.sessionOnly(s.handleCreateAPIKey)))
	mux.Handle("DELETE /api/v1/auth/api-keys/{id}", s.auth(s.sessionOnly(s.handleDeleteAPIKey)))

	// Agents & backlog. All roles may read (role-scoped views in the MVP: same
	// data, different write rights).
	anyRole := []string{identity.RoleOrgAdmin, identity.RoleAgentOwner,
		identity.RoleSecurity, identity.RoleAuditor, identity.RoleControlling}
	manage := []string{identity.RoleOrgAdmin, identity.RoleAgentOwner}
	securityRoles := []string{identity.RoleOrgAdmin, identity.RoleSecurity}

	// Setup: the credential first, then the company, then the People department
	// (setup.go, spec/20). Everything here is also reachable by hand — the setup
	// buys order, not exclusivity.
	mux.Handle("GET /api/v1/setup/state", s.rbac(manage, s.handleSetupState))
	mux.Handle("POST /api/v1/setup/engine", s.rbac(manage, s.handleSetupEngine))
	mux.Handle("POST /api/v1/setup/org", s.rbac(manage, s.handleSetupOrg))
	mux.Handle("POST /api/v1/setup/people", s.rbac(manage, s.handleSetupPeople))

	// First steps as a checklist over the real org state (onboarding.go).
	// The audit trail may be read by those it concerns: platform admin,
	// security and auditor. Agent owner and controlling may not — they appear
	// in it themselves.
	mux.Handle("GET /api/v1/audit", s.rbac([]string{identity.RoleOrgAdmin,
		identity.RoleSecurity, identity.RoleAuditor}, s.handleAuditLog))
	mux.Handle("GET /api/v1/onboarding", s.rbac(anyRole, s.handleOnboarding))
	mux.Handle("GET /api/v1/agents", s.rbac(anyRole, s.handleListAgents))
	mux.Handle("POST /api/v1/agents", s.rbac(manage, s.handleCreateAgent))
	mux.Handle("GET /api/v1/agents/{id}", s.agentScoped(anyRole, s.handleGetAgent))
	mux.Handle("DELETE /api/v1/agents/{id}", s.agentScoped(manage, s.handleDeleteAgent))
	mux.Handle("GET /api/v1/agents/{id}/config", s.agentScoped(anyRole, s.handleGetConfig))
	mux.Handle("GET /api/v1/agents/{id}/export", s.agentScoped(append(manage, identity.RoleSecurity), s.handleExportAgent))
	mux.Handle("GET /api/v1/agents/{id}/diagnostics", s.agentScoped(append(manage, identity.RoleSecurity), s.handleAgentDiagnostics))
	mux.Handle("GET /api/v1/agents/{id}/lint", s.agentScoped(append(manage, identity.RoleSecurity), s.handleAgentLint))
	// Die Arbeitsakte (workrecord.go, spec/21). Sie folgt den Recordings und
	// nicht den Kostenzahlen: eine Summe sagt, was ausgegeben wurde, eine Akte
	// sagt, wie jemand gearbeitet hat.
	mux.Handle("GET /api/v1/agents/{id}/work-record", s.agentScoped(workRecordRoles(), s.handleWorkRecord))
	mux.Handle("GET /api/v1/agents/{id}/reviews", s.agentScoped(workRecordRoles(), s.handleAgentReviews))
	// The workplace: the persistent home as a file tree (files.go). Security
	// may read on top of that — whoever investigates an agent has to see what
	// lies with it. Writing stays with the administrators: a file in the home
	// is configuration of the agent, not an audit action.
	mux.Handle("GET /api/v1/agents/{id}/files", s.agentScoped(append(manage, identity.RoleSecurity), s.handleListFiles))
	mux.Handle("GET /api/v1/agents/{id}/files/content", s.agentScoped(append(manage, identity.RoleSecurity), s.handleReadFile))
	mux.Handle("GET /api/v1/agents/{id}/files/download", s.agentScoped(append(manage, identity.RoleSecurity), s.handleDownloadFile))
	mux.Handle("GET /api/v1/agents/{id}/files/preview", s.agentScoped(append(manage, identity.RoleSecurity), s.handlePreviewFile))
	mux.Handle("GET /api/v1/agents/{id}/files/zip", s.agentScoped(append(manage, identity.RoleSecurity), s.handleZipFiles))
	mux.Handle("GET /api/v1/agents/{id}/files/usage", s.agentScoped(append(manage, identity.RoleSecurity), s.handleFilesUsage))
	mux.Handle("PUT /api/v1/agents/{id}/files/content", s.agentScoped(manage, s.handleWriteFile))
	mux.Handle("POST /api/v1/agents/{id}/files/upload", s.agentScoped(manage, s.handleUploadFiles))
	mux.Handle("POST /api/v1/agents/{id}/files/dir", s.agentScoped(manage, s.handleMkdir))
	mux.Handle("POST /api/v1/agents/{id}/files/move", s.agentScoped(manage, s.handleMoveFile))
	mux.Handle("DELETE /api/v1/agents/{id}/files", s.agentScoped(manage, s.handleDeleteFile))
	mux.Handle("GET /api/v1/skills/covey-agent.zip", s.rbac(anyRole, s.handleDownloadSkill))
	mux.Handle("POST /api/v1/agents/import", s.rbac(manage, s.handleImportAgent))
	mux.Handle("PUT /api/v1/agents/{id}/config", s.agentScoped(manage, s.handlePutConfig))
	// Overwrite an existing agent from a bundle — the config files only.
	mux.Handle("POST /api/v1/agents/{id}/config/import", s.agentScoped(manage, s.handleImportConfig))
	// AI assistant for adapting agents (config copilot, FR-001): available only
	// when a Claude credential is stored org-wide.
	mux.Handle("GET /api/v1/assist/status", s.rbac(anyRole, s.handleAssistStatus))
	mux.Handle("POST /api/v1/agents/{id}/config/assist", s.agentScoped(manage, s.handleConfigAssist))
	mux.Handle("GET /api/v1/agents/{id}/heartbeats", s.agentScoped(anyRole, s.handleHeartbeats))
	mux.Handle("POST /api/v1/agents/{id}/heartbeats/{name}/fire", s.agentScoped(manage, s.handleFireHeartbeat))
	mux.Handle("GET /api/v1/agents/{id}/backlog", s.agentScoped(anyRole, s.handleBacklog))
	mux.Handle("POST /api/v1/agents/{id}/tasks", s.agentScoped(manage, s.handleCreateTask))
	mux.Handle("POST /api/v1/agents/{id}/wake", s.agentScoped(manage, s.handleWake))
	// Hiring: the one way out of the draft state, and only a human walks it
	// (hiring.go, spec/20).
	mux.Handle("POST /api/v1/agents/{id}/hire", s.agentScoped(manage, s.handleHire))
	mux.Handle("GET /api/v1/names/roll", s.rbac(manage, s.handleRollName))
	// The brief: the agent form ends in an assignment to the People department
	// (hiring.go, spec/20).
	mux.Handle("POST /api/v1/hiring/brief", s.rbac(manage, s.handleHiringBrief))
	mux.Handle("GET /api/v1/hiring/brief/{id}", s.rbac(manage, s.handleHiringBriefStatus))
	mux.Handle("POST /api/v1/agents/{id}/kill", s.agentScoped(append(manage, identity.RoleSecurity), s.handleKill))
	mux.Handle("POST /api/v1/agents/{id}/resume", s.agentScoped(append(manage, identity.RoleSecurity), s.handleResumeAgent))
	mux.Handle("POST /api/v1/agents/{id}/budget", s.agentScoped(manage, s.handleSetBudget))
	mux.Handle("PATCH /api/v1/agents/{id}/name", s.agentScoped(manage, s.handleRename))
	mux.Handle("PATCH /api/v1/agents/{id}/profile", s.agentScoped(manage, s.handleUpdateAgentProfile))
	mux.Handle("PATCH /api/v1/agents/{id}/slug", s.agentScoped(manage, s.handleSetSlug))
	mux.Handle("PATCH /api/v1/agents/{id}/runtime", s.agentScoped(manage, s.handleSetRuntime))
	mux.Handle("PATCH /api/v1/agents/{id}/model", s.agentScoped(manage, s.handleSetModel))
	mux.Handle("PATCH /api/v1/agents/{id}/effort", s.agentScoped(manage, s.handleSetEffort))
	mux.Handle("PATCH /api/v1/agents/{id}/max-turns", s.agentScoped(manage, s.handleSetMaxTurns))
	mux.Handle("PATCH /api/v1/agents/{id}/recording-level", s.agentScoped(manage, s.handleSetRecordingLevel))
	mux.Handle("PATCH /api/v1/agents/{id}/recording-retention", s.agentScoped(manage, s.handleSetRecordingRetention))
	mux.Handle("PATCH /api/v1/agents/{id}/warm-sandbox", s.agentScoped(manage, s.handleSetWarmSandbox))
	mux.Handle("PATCH /api/v1/agents/{id}/sandbox-image", s.agentScoped(manage, s.handleSetSandboxImage))
	mux.Handle("PATCH /api/v1/agents/{id}/runner-tags", s.agentScoped(manage, s.handleSetRunnerTags))
	mux.Handle("PATCH /api/v1/agents/{id}/services", s.agentScoped(manage, s.handleSetServices))

	// Runners (spec/16). Reading is for everyone who may look at the platform;
	// adding and decommissioning a host is a management act.
	mux.Handle("GET /api/v1/runners", s.rbac(anyRole, s.handleListRunners))
	mux.Handle("GET /api/v1/runners/health", s.rbac(anyRole, s.handleRunnerHealth))
	// The workplaces from the catalogue (spec/16) — readable for everyone who
	// may look at an agent, because that is where they are chosen.
	mux.Handle("GET /api/v1/workplaces", s.rbac(anyRole, s.handleListWorkplaces))
	// Das Image herholen, bevor der erste Agent darauf wartet. Wer Agenten
	// verwaltet, darf das — es beschafft, was die Instanz ohnehin startet, und
	// aendert an keiner Konfiguration etwas.
	mux.Handle("POST /api/v1/workplaces/{name}/pull", s.rbac(manage, s.handlePullWorkplace))
	// Ein eigenes Image anmelden — einmal, mit Namen und Beschreibung, statt
	// als freier Text an jedem Agenten (spec/16).
	mux.Handle("POST /api/v1/workplaces", s.rbac(manage, s.handleCreateWorkplace))
	mux.Handle("DELETE /api/v1/workplaces/{name}", s.rbac(manage, s.handleDeleteWorkplace))
	// Which images may run BESIDE a sandbox as services (spec/16). Under
	// manage, because extending the list is the privileged act — naming an
	// image, once the list stands, is not.
	mux.Handle("GET /api/v1/service-images", s.rbac(manage, s.handleListServiceImages))
	mux.Handle("POST /api/v1/service-images", s.rbac(manage, s.handleAddServiceImage))
	mux.Handle("DELETE /api/v1/service-images/{id}", s.rbac(manage, s.handleDeleteServiceImage))
	mux.Handle("POST /api/v1/runners/registration-tokens", s.rbac(manage, s.handleCreateRegistrationToken))
	mux.Handle("PATCH /api/v1/runners/{id}", s.rbac(manage, s.handleUpdateRunner))
	mux.Handle("POST /api/v1/runners/{id}/pull", s.rbac(manage, s.handlePullOnRunner))
	mux.Handle("POST /api/v1/runners/{id}/update", s.rbac(manage, s.handleUpdateRunnerBinary))
	mux.Handle("DELETE /api/v1/runners/{id}/update", s.rbac(manage, s.handleCancelRunnerUpdate))
	mux.Handle("GET /api/v1/runners/{id}/logs", s.rbac(anyRole, s.handleRunnerLogs))
	mux.Handle("POST /api/v1/runners/{id}/log-level", s.rbac(manage, s.handleSetRunnerLogLevel))
	mux.Handle("DELETE /api/v1/runners/{id}", s.rbac(manage, s.handleDeleteRunner))

	// The home store (spec/16, "Interface"). A store that grows quietly and
	// whose content nobody can see is an operational risk — you notice it when
	// the disk is full.
	mux.Handle("GET /api/v1/agents/{id}/home", s.agentScoped(append(manage, identity.RoleSecurity), s.handleAgentHome))
	mux.Handle("GET /api/v1/agents/{id}/placement", s.agentScoped(anyRole, s.handleAgentPlacement))
	// One state per agent, so there is nothing to list and nothing to pick from
	// (spec/16). What remains is forcing the sync that writes it.
	mux.Handle("POST /api/v1/agents/{id}/home/snapshots", s.agentScoped(manage, s.handleBackupNow))
	// Platform diagnostics: what a restart would run into, and which agent
	// configs need catching up after an upgrade. Both existed only as
	// subcommands, which is to say: only for whoever has a shell on the host.
	// identity.RolePlatformAdmin, which this line used to name, is what
	// RoleOrgAdmin was called before migration 0061 — so the rename, not a
	// change of who may ask.
	mux.Handle("GET /api/v1/platform/doctor", s.rbac([]string{identity.RoleOrgAdmin}, s.handleDoctor))
	// Die Bestaetigung, dass der Blockspeicher in der Sicherung liegt. Nicht
	// pruefbar, nur festzuhalten — und genau deshalb ein eigener Schritt, den
	// ein Mensch geht (siehe handleConfirmHomeStoreBackup).
	mux.Handle("POST /api/v1/platform/doctor/home-store-backup",
		s.rbac([]string{identity.RoleOrgAdmin}, s.handleConfirmHomeStoreBackup))
	mux.Handle("GET /api/v1/platform/lint", s.rbac(append(manage, identity.RoleSecurity), s.handleOrgLint))
	mux.Handle("GET /api/v1/platform/home-store", s.rbac(anyRole, s.handleGetStore))
	mux.Handle("POST /api/v1/platform/home-store/cleanup", s.rbac(manage, s.handleCleanupStore))
	mux.Handle("GET /api/v1/org/recording-level", s.rbac(anyRole, s.handleGetOrgRecording))
	mux.Handle("PATCH /api/v1/org/recording-level", s.rbac(securityRoles, s.handleSetOrgRecording))
	mux.Handle("PATCH /api/v1/org/recording-retention", s.rbac(securityRoles, s.handleSetOrgRecordingRetention))
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
	// The organisation's own master data: name and what this company does — the
	// context every agent works in (spec/20).
	mux.Handle("GET /api/v1/org", s.rbac(anyRole, s.handleGetOwnOrg))
	mux.Handle("PATCH /api/v1/org/description", s.rbac(manage, s.handleSetOwnOrgDescription))
	// Wo der Quelltext dieser Plattform liegt (spec/21). Stammdaten wie die
	// Unternehmensbeschreibung — wer sie pflegt, pflegt auch das.
	mux.Handle("PATCH /api/v1/org/platform-repo", s.rbac(manage, s.handleSetPlatformRepo))
	mux.Handle("GET /api/v1/org/chart", s.rbac(anyRole, s.handleOrgChart))
	mux.Handle("GET /api/v1/org/humans/{id}", s.rbac(anyRole, s.handleGetHuman))
	mux.Handle("GET /api/v1/org/profile-fields", s.rbac(anyRole, s.handleListProfileFields))
	mux.Handle("POST /api/v1/org/profile-fields", s.rbac([]string{identity.RoleOrgAdmin}, s.handleCreateProfileField))
	mux.Handle("PATCH /api/v1/org/profile-fields/{id}", s.rbac([]string{identity.RoleOrgAdmin}, s.handleRenameProfileField))
	mux.Handle("DELETE /api/v1/org/profile-fields/{id}", s.rbac([]string{identity.RoleOrgAdmin}, s.handleDeleteProfileField))
	mux.Handle("GET /api/v1/runtimes", s.rbac(anyRole, s.handleListRuntimes))
	// The configured workplaces: engine + capacity, assignable (spec/18).
	mux.Handle("GET /api/v1/runtime-instances", s.rbac(anyRole, s.handleListRuntimeInstances))
	mux.Handle("POST /api/v1/runtime-instances", s.rbac(securityRoles, s.handleCreateRuntime))
	mux.Handle("PUT /api/v1/runtime-instances/{id}", s.rbac(securityRoles, s.handleUpdateRuntime))
	mux.Handle("DELETE /api/v1/runtime-instances/{id}", s.rbac(securityRoles, s.handleDeleteRuntime))
	mux.Handle("POST /api/v1/runtime-instances/{id}/credentials", s.rbac(securityRoles, s.handleAddRuntimeCredential))
	mux.Handle("POST /api/v1/runtime-instances/{id}/credentials/order", s.rbac(securityRoles, s.handleReorderRuntimeCredentials))
	mux.Handle("PATCH /api/v1/runtime-instances/{id}/credentials/{ord}", s.rbac(securityRoles, s.handlePatchRuntimeCredential))
	mux.Handle("DELETE /api/v1/runtime-instances/{id}/credentials/{ord}", s.rbac(securityRoles, s.handleDeleteRuntimeCredential))
	mux.Handle("POST /api/v1/agents/{id}/runtime-instance", s.agentScoped(manage, s.handleAssignRuntime))
	mux.Handle("GET /api/v1/marketplace", s.rbac(anyRole, s.handleMarketplace))
	mux.Handle("POST /api/v1/marketplace/{name}/install", s.rbac(securityRoles, s.handleMarketplaceInstall))
	mux.Handle("GET /api/v1/targets", s.rbac(anyRole, s.handleListTargets))
	mux.Handle("POST /api/v1/targets", s.rbac(securityRoles, s.handleUploadTarget))
	mux.Handle("POST /api/v1/targets/mcp", s.rbac(securityRoles, s.handleCreateMCP))
	mux.Handle("POST /api/v1/targets/{name}/discover", s.rbac(securityRoles, s.handleDiscoverMCP))
	mux.Handle("GET /api/v1/targets/{name}/tools", s.rbac(anyRole, s.handleListMCPTools))
	mux.Handle("GET /api/v1/targets/{name}/setup", s.rbac(anyRole, s.handleTargetSetup))
	mux.Handle("POST /api/v1/targets/{name}/probe", s.rbac(securityRoles, s.handleTargetProbe))
	mux.Handle("PATCH /api/v1/targets/{name}", s.rbac(securityRoles, s.handleToggleTarget))
	mux.Handle("DELETE /api/v1/targets/{name}", s.rbac(securityRoles, s.handleDeleteTarget))
	// What the agent can do in which target system — plugin, access and action
	// list in one place (targets.go).
	mux.Handle("GET /api/v1/agents/{id}/systems", s.agentScoped(anyRole, s.handleAgentSystems))
	mux.Handle("GET /api/v1/agents/{id}/tools/{system}", s.agentScoped(anyRole, s.handleGetAgentTools))
	mux.Handle("PUT /api/v1/agents/{id}/tools/{system}", s.agentScoped(securityRoles, s.handleSetAgentTools))
	mux.Handle("GET /api/v1/agents/{id}/recording", s.agentScoped(anyRole, s.handleRecording))
	mux.Handle("GET /api/v1/recordings/blobs/{id}", s.rbac(anyRole, s.handleRecordingBlob))
	mux.Handle("GET /api/v1/agents/{id}/cost", s.agentScoped(anyRole, s.handleCost))
	mux.Handle("GET /api/v1/agents/{id}/cost/series", s.agentScoped(anyRole, s.handleCostSeries))
	mux.Handle("GET /api/v1/cost/org", s.rbac(anyRole, s.handleOrgCost))
	mux.Handle("GET /api/v1/cost/runs", s.rbac(anyRole, s.handleOrgRunCosts))
	mux.Handle("GET /api/v1/agents/{id}/cost/runs", s.agentScoped(anyRole, s.handleAgentRunCosts))
	// The price list: delivery next to the cost, same scope, same period
	// (spec/17-kpis.md).
	mux.Handle("GET /api/v1/cost/indicators", s.rbac(anyRole, s.handleOrgIndicators))
	// Die Kennzahlen EINES Agenten sind der Kennzahlen-Abschnitt der Akte —
	// dieselbe Grenze. Die org-weite Preisliste daneben bleibt offen: sie
	// gruppiert ueber Kennzahl-Schluessel, nicht ueber Personen (spec/17).
	mux.Handle("GET /api/v1/agents/{id}/cost/indicators", s.agentScoped(workRecordRoles(), s.handleAgentIndicators))
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

	// Custom stages (kanban overlay, per agent).
	mux.Handle("GET /api/v1/agents/{id}/stages", s.agentScoped(anyRole, s.handleListStages))
	mux.Handle("POST /api/v1/agents/{id}/stages", s.agentScoped(manage, s.handleCreateStage))
	mux.Handle("POST /api/v1/agents/{id}/stages/reorder", s.agentScoped(manage, s.handleReorderStages))
	mux.Handle("PATCH /api/v1/stages/{id}", s.stageScoped(manage, s.handleUpdateStage))
	mux.Handle("DELETE /api/v1/stages/{id}", s.stageScoped(manage, s.handleDeleteStage))

	// Trust layer.
	mux.Handle("GET /api/v1/approvals", s.rbac(anyRole, s.handleListApprovals))
	mux.Handle("POST /api/v1/approvals/{id}/decide", s.rbac(append(manage, identity.RoleSecurity), s.handleDecideApproval))

	// Der Posteingang (inbox.go): Freigaben und offene Punkte in EINER Liste,
	// sortier-, filter- und blätterbar. Alle Rollen dürfen ihn abrufen —
	// Controlling bekommt daraus nur die Freigaben, die Arbeitsakten-Seite
	// filtert der Handler heraus.
	mux.Handle("GET /api/v1/inbox", s.rbac(anyRole, s.handleInbox))

	// Die offenen Punkte aus dem Betrieb (improvements.go, spec/21): Vorschlag,
	// Befund, Issue. Lesen darf, wen es angeht — Controlling fehlt bewusst,
	// entschieden wird wie bei den Freigaben.
	mux.Handle("GET /api/v1/improvements", s.rbac(improvementReadRoles(), s.handleListImprovements))
	mux.Handle("GET /api/v1/improvements/{id}", s.rbac(improvementReadRoles(), s.handleGetImprovement))
	mux.Handle("POST /api/v1/improvements/{id}/decide",
		s.rbac(append(manage, identity.RoleSecurity), s.handleDecideImprovement))
	mux.Handle("GET /api/v1/guardrails", s.rbac(anyRole, s.handleListGuardrails))
	mux.Handle("POST /api/v1/guardrails", s.rbac(securityRoles, s.handleCreateGuardrail))
	mux.Handle("PATCH /api/v1/guardrails/{id}", s.rbac(securityRoles, s.handleUpdateGuardrail))
	mux.Handle("DELETE /api/v1/guardrails/{id}", s.rbac(securityRoles, s.handleDeleteGuardrail))
	mux.Handle("POST /api/v1/guardrails/test", s.rbac(anyRole, s.handleTestGuardrail))
	mux.Handle("GET /api/v1/guardrails/events", s.rbac(anyRole, s.handleGuardrailEvents))
	// Egress: status, templates, per-agent assignment, decision log.
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
	// Several values under one key (spec/04) — storage only; what they are used
	// for lives under /runtimes.
	mux.Handle("POST /api/v1/secrets/{key}/values", s.rbac(securityRoles, s.handleAddSecretValue))
	mux.Handle("DELETE /api/v1/secrets/{key}/values/{slot}", s.rbac(securityRoles, s.handleDeleteSecretValue))
	mux.Handle("PUT /api/v1/secrets/{key}/agents/{agentID}", s.rbac(securityRoles, s.handleAssignSecret))
	mux.Handle("DELETE /api/v1/secrets/{key}/agents/{agentID}", s.rbac(securityRoles, s.handleUnassignSecret))
	mux.Handle("GET /api/v1/agents/{id}/secrets", s.agentScoped(securityRoles, s.handleListAgentSecrets))
	mux.Handle("PUT /api/v1/agents/{id}/secrets/{key}", s.agentScoped(securityRoles, s.handlePutAgentSecret))
	mux.Handle("PATCH /api/v1/agents/{id}/secrets/{key}", s.agentScoped(securityRoles, s.handlePatchAgentSecret))
	mux.Handle("DELETE /api/v1/agents/{id}/secrets/{key}", s.agentScoped(securityRoles, s.handleDeleteAgentSecret))
	mux.Handle("POST /api/v1/fleet/kill", s.rbac(securityRoles, s.handleFleetKill))
	mux.Handle("POST /api/v1/fleet/resume", s.rbac(securityRoles, s.handleFleetResume))
	mux.Handle("GET /api/v1/fleet", s.rbac(anyRole, s.handleFleetStatus))

	// Agent skills: org library, linking, agent-owned capabilities
	// (agentskills.go). Every role may read — skills are procedures, not
	// secrets, and thus stand on the same level as the agent config.
	// The route /api/v1/skills/covey-agent.zip further up is something else
	// (the Claude Code skill to download) and, as a literal segment, takes
	// precedence over the {id} placeholder.
	mux.Handle("GET /api/v1/skills", s.rbac(anyRole, s.handleListSkills))
	mux.Handle("POST /api/v1/skills", s.rbac(manage, s.handleCreateSkill))
	mux.Handle("GET /api/v1/skills/{id}", s.rbac(anyRole, s.handleGetSkill))
	mux.Handle("PUT /api/v1/skills/{id}", s.rbac(manage, s.handlePutSkill))
	mux.Handle("DELETE /api/v1/skills/{id}", s.rbac(manage, s.handleDeleteSkill))
	mux.Handle("PUT /api/v1/skills/{id}/agents/{agentID}", s.rbac(manage, s.handleAssignSkill))
	mux.Handle("DELETE /api/v1/skills/{id}/agents/{agentID}", s.rbac(manage, s.handleUnassignSkill))
	mux.Handle("GET /api/v1/agents/{id}/skills", s.agentScoped(anyRole, s.handleAgentSkills))
	mux.Handle("POST /api/v1/agents/{id}/skills", s.agentScoped(manage, s.handleCreateAgentSkill))

	// Template library.
	mux.Handle("GET /api/v1/templates", s.rbac(anyRole, s.handleListTemplates))
	mux.Handle("GET /api/v1/templates/{id}", s.rbac(anyRole, s.handleGetTemplate))
	mux.Handle("POST /api/v1/templates", s.rbac(manage, s.handleSaveTemplate))
	mux.Handle("DELETE /api/v1/templates/{id}", s.rbac(manage, s.handleDeleteTemplate))
	mux.Handle("POST /api/v1/templates/{id}/instantiate", s.rbac(manage, s.handleInstantiateTemplate))

	// Administration: user and tenant management (org_admin only).
	adminOnly := []string{identity.RoleOrgAdmin}
	mux.Handle("GET /api/v1/users", s.rbac(adminOnly, s.handleListUsers))
	mux.Handle("POST /api/v1/users", s.rbac(adminOnly, s.handleCreateUser))
	mux.Handle("PATCH /api/v1/users/{id}", s.rbac(adminOnly, s.handleUpdateUser))
	mux.Handle("DELETE /api/v1/users/{id}", s.rbac(adminOnly, s.handleDeleteUser))
	// Die Mandanten gehören der Instanz, nicht einer Organisation: system_admin
	// statt org_admin (FR-003, Befund F). Die alten Adressen unter /orgs
	// gibt es nicht mehr — sie waren für jede Organisation erreichbar.
	mux.Handle("GET /api/v1/platform/orgs", s.platformAdmin(s.handleListOrgs))
	mux.Handle("POST /api/v1/platform/orgs", s.platformAdmin(s.handleCreateOrg))
	mux.Handle("PATCH /api/v1/platform/orgs/{id}", s.platformAdmin(s.handleUpdateOrg))
	mux.Handle("DELETE /api/v1/platform/orgs/{id}", s.platformAdmin(s.handleDeleteOrg))
	// Der Rest der Instanz-Verwaltung: die Anmeldungen selbst, die Schalter
	// der Installation und die Wartelisten-Codes (internal/httpapi/platform.go).
	mux.Handle("GET /api/v1/platform/accounts", s.platformAdmin(s.handleListAccounts))
	mux.Handle("PATCH /api/v1/platform/accounts/{id}", s.platformAdmin(s.handleSetAccountPlatformRole))
	mux.Handle("GET /api/v1/platform/settings", s.platformAdmin(s.handleListSettings))
	mux.Handle("PUT /api/v1/platform/settings/{key}", s.platformAdmin(s.handleSetSetting))
	mux.Handle("GET /api/v1/platform/waitlist-codes", s.platformAdmin(s.handleListWaitlistCodes))
	mux.Handle("POST /api/v1/platform/waitlist-codes", s.platformAdmin(s.handleCreateWaitlistCode))
	mux.Handle("DELETE /api/v1/platform/waitlist-codes/{hash}", s.platformAdmin(s.handleRevokeWaitlistCode))

	// Live updates.
	mux.Handle("GET /api/v1/events", s.auth(s.handleSSE))

	// Request log (platform diagnostics): the HTTP requests at the edges.
	mux.Handle("GET /api/v1/platform/requests", s.rbac(securityRoles, s.handleListRequests))
	mux.Handle("GET /api/v1/platform/requests/{id}", s.rbac(securityRoles, s.handleGetRequest))
	mux.Handle("DELETE /api/v1/platform/requests", s.rbac(securityRoles, s.handleClearRequests))

	// Machine endpoints (auth of their own: HMAC, trigger token or daemon JWT).
	// Both go through logIncoming — also (and especially) when they are
	// rejected: a webhook that fails on the signature is the most common
	// question when hooking up a target system.
	mux.HandleFunc("POST /api/webhooks/{system}/{agent}", s.logIncoming(s.handleTargetWebhook))
	mux.HandleFunc("POST /api/trigger/{token}", s.logIncoming(s.handleAgentTrigger))
	mux.HandleFunc("GET /api/daemon/ws", s.handleDaemonWS)

	// The runner API (spec/16): a runner's only interface to the platform,
	// authenticated with its own token and scoped to its organisation. Its
	// first user is the egress proxy, which used to read its allowlist from
	// Postgres itself.
	mux.Handle("GET /api/runner/v1/egress/allowlist", s.runnerAuth(s.handleRunnerAllowlist))
	mux.Handle("POST /api/runner/v1/egress/decisions", s.runnerAuth(s.handleRunnerDecisions))
	mux.Handle("GET /api/runner/ws", s.runnerAuth(s.handleRunnerWS))
	mux.Handle("GET /api/runner/v1/whoami", s.runnerAuth(s.handleRunnerWhoami))
	// Registration carries its own authentication: whoever registers has
	// nothing to log in with, and the registration token names the
	// organisation the runner will belong to.
	mux.HandleFunc("POST /api/runner/v1/register", s.handleRunnerRegister)
	// The home store for a remote runner. One path, three methods: is the block
	// there, give it to me, take it.
	for _, method := range []string{"HEAD", "GET", "PUT"} {
		mux.Handle(method+" /api/runner/v1/blocks/{hash}", s.runnerAuth(s.handleRunnerBlock))
	}
	// The bundled question sits beside the single one, not under {hash}: it is
	// about many blocks and belongs to no single hash.
	mux.Handle("POST /api/runner/v1/blocks-have", s.runnerAuth(s.handleRunnerBlocksHave))

	// This instance's installation script — deliberately without a login:
	// whoever fetches it has nothing yet to log in with. It ships its own
	// version along, so that a runner installed through it matches the server
	// (spec/16, "protocol version").
	mux.HandleFunc("GET /install.sh", s.handleInstallScript)

	// Embedded SPA together with the pre-rendered public website.
	if s.WebFS != nil {
		mux.HandleFunc("GET /robots.txt", s.handleRobots)
		mux.Handle("/", s.spaHandler(s.WebFS))
	}
	// The audit trail sits INSIDE, directly around the mux: it needs the
	// principal that the auth middleware sets per route, and the status code
	// that the handler writes.
	// Put the protective headers on EVERYTHING, not just on the interface: a
	// 404 and every API response come from the same origin too, and an endpoint
	// that forgets them is exactly the gap nobody goes looking for. Handlers
	// with ideas of their own (the file preview sets a stricter CSP) simply
	// override them afterwards.
	return s.mitSchutzHeadern(mitKompression(s.mitAuditSpur(mux)))
}

// mitSchutzHeadern sets the security headers before every response.
func (s *Server) mitSchutzHeadern(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setzeSchutzHeader(w)
		// HSTS only on an instance that actually runs over HTTPS (CookieSecure
		// is the same switch). On a local HTTP instance the header would nail
		// the browser to https for months and block the way in.
		if s.CookieSecure {
			w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// --- Helpers ---

// baseCtx is the server's lifecycle, with a fallback for tests.
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
		errors.Is(err, accounts.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		writeErr(w, http.StatusNotFound, "not found")
	case errors.Is(err, backlog.ErrInvalidTransition),
		errors.Is(err, org.ErrLastAdmin), errors.Is(err, org.ErrEmailTaken),
		errors.Is(err, accounts.ErrLastSystemAdmin), errors.Is(err, org.ErrManagerCycle):
		writeErr(w, http.StatusConflict, err.Error())
	default:
		writeErr(w, http.StatusInternalServerError, err.Error())
	}
}

// --- Session auth (builtin: cookie + hash in Postgres) ---

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
		// An API key comes first: whoever sends one means it, and a stale
		// cookie in the same client must not quietly take its place.
		if token, ok := bearerToken(r); ok {
			p, _, err := s.apiKeys().Principal(r.Context(), hashToken(token))
			if err != nil {
				writeErr(w, http.StatusUnauthorized, "api key invalid or expired")
				return
			}
			if h, ok := r.Context().Value(akteurKey).(*akteurHalter); ok {
				h.p = p
			}
			next(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
			return
		}
		cookie, err := r.Cookie("covey_session")
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "not signed in")
			return
		}
		p, expires, err := s.sessions().Principal(r.Context(), hashToken(cookie.Value))
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "session expired")
			return
		}
		// Sliding session: whoever is working here keeps their session. The
		// renewal only happens in the second half of the lifetime — otherwise
		// every request would write to the database, and an interface that
		// polls does a few of those per minute.
		if time.Until(expires) < s.SessionTTL/2 {
			if err := s.sessions().Renew(r.Context(), hashToken(cookie.Value), time.Now().Add(s.SessionTTL)); err == nil {
				// The cookie carries the same lifetime: a session extended only
				// in the database would still be dropped by the browser.
				s.setSessionCookie(w, cookie.Value, int(s.SessionTTL.Seconds()))
			}
		}
		// Report the actor back to the audit middleware on top of that: it sits
		// further out and does not see the context we create here.
		if h, ok := r.Context().Value(akteurKey).(*akteurHalter); ok {
			h.p = p
		}
		next(w, r.WithContext(context.WithValue(r.Context(), principalKey, p)))
	})
}

// bearerToken reads an "Authorization: Bearer covey_…" header. The prefix is
// part of the check: the daemon, the runner and the agent trigger all carry
// bearer tokens of their own on their own routes, and none of them should be
// mistaken for a human API key here.
func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		return "", false
	}
	tok := strings.TrimSpace(h[7:])
	if !strings.HasPrefix(tok, apiKeyPrefix) {
		return "", false
	}
	return tok, true
}

// sessionOnly bars an API key from the two moves with which a leaked
// credential would entrench itself: minting another key, and changing the
// password. Both need the browser session — and therefore the password.
//
// It is not a rights question and therefore not a role: the owner of the key
// may do all of this, just not with the key.
func (s *Server) sessionOnly(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if principalFrom(r).ViaAPIKey {
			writeErr(w, http.StatusForbidden,
				"an API key cannot do this — sign in in the browser (a key must not be able to mint or replace itself)")
			return
		}
		next(w, r)
	}
}

// rbac enforces the role-scoped rights (spec/09).
func (s *Server) rbac(roles []string, next http.HandlerFunc) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		p := principalFrom(r)
		// Signed in, but no seat: since accounts were split off from
		// memberships (FR-002), an account can exist before its organisation
		// does. That is not a lack of RIGHTS but a lack of CONTEXT, and it
		// needs an answer of its own — otherwise the interface reads the 403
		// as "wrong password", throws the session away and sends whoever just
		// registered back to the login form they came from.
		if !p.HasOrg() {
			writeJSON(w, http.StatusConflict, map[string]string{
				"error": "no_organization",
				"hint":  "this account does not belong to an organisation yet",
			})
			return
		}
		for _, role := range roles {
			if p.Role == role {
				next(w, r)
				return
			}
		}
		writeErr(w, http.StatusForbidden, "role "+p.Role+" has no rights here")
	})
}

// platformAdmin guards the instance level — everything that concerns not one
// organisation but the installation: the tenant list, the system settings, the
// waitlist codes.
//
// It hangs off auth and deliberately NOT off rbac. Two reasons, and both
// matter:
//
// The org roles answer "what may this person do INSIDE their organisation".
// org_admin is one of them, and every organisation hands it out to
// itself — using it here would mean the first self-registered tenant could
// delete all the others (FR-003, finding F). The instance level therefore sits
// on the account, where no organisation can reach it.
//
// And it must not require an organisation: whoever administers the
// installation is not necessarily a member of any of its tenants. rbac would
// turn that into a 409 "no_organization" — for the very person who is supposed
// to fix it.
func (s *Server) platformAdmin(next http.HandlerFunc) http.Handler {
	return s.auth(func(w http.ResponseWriter, r *http.Request) {
		if principalFrom(r).PlatformRole != accounts.RoleSystemAdmin {
			// 404 and not 403: whether this installation has an administration
			// at all is nobody's business who is not part of it.
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next(w, r)
	})
}

// agentScoped is rbac PLUS the proof that the agent from the URL belongs to
// the caller's organization. Every route under /agents/{id}/… runs through it.
//
// The reason for a middleware instead of one line in the handler: this check
// used to be every single handler's business — and in 25 out of 67 it was
// simply forgotten. Foreign config, foreign backlog, foreign recording, foreign
// cost were readable that way, wake/kill/budget even triggerable. A boundary
// that 67 callers have to remember is no boundary; one you MUST pass through
// is.
//
// The resolved agent then lies in the context — the handlers need it anyway
// most of the time and save themselves the second query.
func (s *Server) agentScoped(roles []string, next http.HandlerFunc) http.Handler {
	return s.rbac(roles, func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id")
			return
		}
		p := principalFrom(r)
		agent, err := s.Registry.Get(r.Context(), id)
		// Foreign or non-existent is the same answer: whether an agent with
		// this id exists somewhere is no other organization's business.
		if err != nil || agent.OrgID != p.OrgID {
			writeErr(w, http.StatusNotFound, "agent not found")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), agentKey, agent)))
	})
}

// taskScoped/stageScoped/pageScoped are the same pattern as agentScoped for
// the remaining objects with an id of their own. Tasks, board columns and wiki
// pages were readable and changeable from foreign organizations because the
// check was the handler's business here too — and was missing there.
//
// Deliberately three small functions instead of one generic: the question "is
// this mine?" is answered differently per object (tasks carry the organization
// themselves, columns and pages only via their agent). A generic one would have
// to untangle that again anyway.
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

// idScoped is the shared body: id from the path, check the affiliation,
// otherwise "not found" — never "forbidden", because the existence of an object
// in another organization is itself already a disclosure.
func (s *Server) idScoped(roles []string, next http.HandlerFunc,
	gehoert func(*http.Request, uuid.UUID, uuid.UUID) bool) http.Handler {
	return s.rbac(roles, func(w http.ResponseWriter, r *http.Request) {
		id, err := uuid.Parse(r.PathValue("id"))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "invalid id")
			return
		}
		if !gehoert(r, principalFrom(r).OrgID, id) {
			writeErr(w, http.StatusNotFound, "not found")
			return
		}
		next(w, r)
	})
}

// agentKey carries the checked agent through the context.
const agentKey ctxKey = "agent"

// agentFrom returns the agent checked by agentScoped. Valid only in handlers
// that are hooked in via agentScoped.
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
		writeErr(w, http.StatusBadRequest, "invalid request")
		return
	}
	now := time.Now()
	key := s.loginKey(r, in.Email)
	if s.loginLimiter.blocked(key, now) {
		writeErr(w, http.StatusTooManyRequests, "too many failed attempts — please try again later")
		return
	}
	p, err := s.Identity.AuthenticateHuman(r.Context(), identity.Credentials{Email: in.Email, Password: in.Password})
	if err != nil {
		s.loginLimiter.fail(key, now)
		writeErr(w, http.StatusUnauthorized, "invalid credentials")
		return
	}
	s.loginLimiter.reset(key)
	buf := make([]byte, 32)
	rand.Read(buf)
	token := hex.EncodeToString(buf)
	if err := s.sessions().Create(r.Context(), hashToken(token), p.AccountID, p.ID, time.Now().Add(s.SessionTTL)); err != nil {
		mapErr(w, err)
		return
	}
	s.setSessionCookie(w, token, int(s.SessionTTL.Seconds()))
	writeJSON(w, http.StatusOK, p)
}

// setSessionCookie writes the session cookie — at sign-in, at every renewal and
// (with maxAge -1) at sign-out. One place, because the flags belong together:
// three copies of them are three chances to lose one.
//
// SameSite=Strict instead of Lax: covey is an administration tool you navigate
// into — there are no deep links from foreign pages that would need any
// consideration. Lax sends the cookie along on every top-level navigation from
// outside; Strict does not, and thereby takes the whole class of attacks that
// begin with a click on a foreign link out of play.
func (s *Server) setSessionCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: "covey_session", Value: token, Path: "/",
		HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie("covey_session"); err == nil {
		_ = s.sessions().Delete(r.Context(), hashToken(cookie.Value))
	}
	s.setSessionCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, principalFrom(r))
}

func parseID(r *http.Request) (uuid.UUID, error) {
	return uuid.Parse(r.PathValue("id"))
}
