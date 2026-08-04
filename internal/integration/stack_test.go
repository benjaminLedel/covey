// Package integration runs the complete vertical slice from spec/11 as a test:
// real Postgres (pgvector), real HTTP server, real WebSocket, in-process daemon
// with mock runtime, fake Zammad. Only the LLM is replaced.
package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/audit"
	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/db"
	"covey/internal/dream"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/httpapi"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	reqlogstore "covey/internal/reqlog/store"
	secbuiltin "covey/internal/secrets/builtin"
	"covey/internal/skills"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"

	_ "covey/internal/target/browser"
	_ "covey/internal/target/dev"
	_ "covey/internal/target/email"
	"covey/migrations"

	_ "covey/internal/target/teams"
	_ "covey/internal/target/zammad"
)

const adminDBURL = "postgres://covey:covey@localhost:5433/covey?sslmode=disable"

// inprocProvider starts the daemon in-process instead of as a subprocess — the
// connection still goes through the real WebSocket endpoint.
type inprocProvider struct {
	homeBase string
	log      *slog.Logger
}

func (p *inprocProvider) Start(ctx context.Context, spec orchestrator.SandboxSpec) (orchestrator.Sandbox, error) {
	runCtx, cancel := context.WithCancel(context.Background())
	home := p.homeBase + "/" + spec.AgentID.String()
	os.MkdirAll(home, 0o700)
	client := daemon.NewClient(spec.Env["COVEY_WS_URL"], spec.Env["COVEY_DAEMON_TOKEN"],
		spec.Env["COVEY_AGENT_ID"], home, p.log)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = client.Run(runCtx)
	}()
	return &inprocSandbox{cancel: cancel, done: done}, nil
}

// AgentHome satisfies orchestrator.FileAccess: in-process too, the home lives on
// disk. Without it the file browser (spec/02) could not be checked in the
// vertical slice — it hangs on exactly this port.
func (p *inprocProvider) AgentHome(agentID uuid.UUID) (orchestrator.Home, error) {
	return orchestrator.Home{Path: p.homeBase + "/" + agentID.String(), UID: -1, GID: -1}, nil
}

type inprocSandbox struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *inprocSandbox) Stop(ctx context.Context) error {
	s.cancel()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
	}
	return nil
}

type stack struct {
	t         *testing.T
	pool      *pgxpool.Pool
	registry  *agents.Registry
	backlog   *backlog.Store
	obs       *observability.Store
	rails     *guardrails.Store
	secrets   *secbuiltin.Store
	mem       *memory.Store
	targets   *targetstore.Store
	egress    *egress.Store
	skills    *skills.Store
	reqlog    *reqlogstore.Store
	templates *templates.Store
	audit     *audit.Store
	dreams    *dream.Store
	orch      *orchestrator.Orchestrator
	http      *httptest.Server
	orgID     uuid.UUID
	adminID   uuid.UUID
	homeBase  string
	cancel    context.CancelFunc
}

const webhookSecret = "test-webhook-secret"

func newStack(t *testing.T) *stack {
	t.Helper()
	ctx := context.Background()

	admin, err := db.Connect(ctx, adminDBURL)
	if err != nil {
		t.Skipf("no test Postgres at localhost:5433: %v", err)
	}
	dbName := "covey_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+dbName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
		admin.Close()
	})

	pool, err := db.Connect(ctx, fmt.Sprintf("postgres://covey:covey@localhost:5433/%s?sslmode=disable", dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		t.Fatal(err)
	}

	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	idp, err := identbuiltin.New(pool)
	if err != nil {
		t.Fatal(err)
	}
	masterKey, _ := secbuiltin.GenerateMasterKey()
	secretStore, err := secbuiltin.New(pool, masterKey)
	if err != nil {
		t.Fatal(err)
	}

	s := &stack{
		t: t, pool: pool,
		registry: agents.NewRegistry(pool),
		backlog:  backlog.NewStore(pool),
		obs:      observability.NewStore(pool),
		rails:    guardrails.NewStore(pool),
		secrets:  secretStore,
		mem:      memory.NewStore(pool, memory.HashEmbedder{}),
	}

	s.targets = targetstore.NewStore(pool)
	s.egress = egress.NewStore(pool)
	s.skills = skills.NewStore(pool)
	// Request log as in production, but without reqlog.SetDefault: the sink is
	// process-wide, and stacks running in parallel would push their entries at
	// each other. What goes through orchestrator and HTTP server (sandbox
	// requests, incoming webhooks) is enough for the vertical slice.
	s.reqlog = reqlogstore.NewStore(pool, log, true, time.Hour)
	// Templates and dreams belong to an instance's basic equipment (main.go always
	// sets them). If they were missing from the test stack, their endpoints would
	// run into a nil pointer instead of the code that is supposed to be checked.
	s.templates = templates.NewStore(pool)
	s.audit = audit.NewStore(pool)
	s.dreams = dream.NewStore(pool, s.mem, log)
	s.homeBase = t.TempDir()

	s.orch = orchestrator.New(orchestrator.Options{
		Pool: pool, Registry: s.registry, Backlog: s.backlog, Obs: s.obs,
		Rails: s.rails, Secrets: secretStore, Identity: idp, Memory: s.mem,
		Targets:        s.targets,
		Skills:         s.skills,
		ReqLog:         s.reqlog,
		Provider:       &inprocProvider{homeBase: s.homeBase, log: log},
		DaemonTokenTTL: 5 * time.Minute,
		TickInterval:   300 * time.Millisecond,
		ReadyTimeout:   10 * time.Second,
		Log:            log,
	})

	srv := &httpapi.Server{
		Pool: pool, Registry: s.registry, Backlog: s.backlog, Obs: s.obs,
		Rails: s.rails, Secrets: secretStore, Identity: idp, Memory: s.mem,
		Org: org.NewStore(pool), Targets: s.targets,
		Templates: s.templates, Dreams: s.dreams, Audit: s.audit,
		Skills:      s.skills,
		EgressStore: s.egress,
		ReqLog:      s.reqlog,
		Orch:        s.orch, Log: log,
		WebhookSecrets: map[string]string{"zammad": webhookSecret},
		SessionTTL:     time.Hour,
	}
	s.http = httptest.NewServer(srv.Handler())
	t.Cleanup(s.http.Close)
	s.orch.PublicWSURL = strings.Replace(s.http.URL, "http", "ws", 1) + "/api/daemon/ws"

	orchCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	t.Cleanup(cancel)
	go s.orch.Run(orchCtx)
	go s.reqlog.Run(orchCtx)

	// Organization + admin.
	s.orgID = uuid.New()
	if _, err := pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Test-Org')", s.orgID); err != nil {
		t.Fatal(err)
	}
	// Target-system activation is opt-in (fail-closed) — the test org enables its
	// built-ins explicitly, as a real organization would in the UI.
	if _, err := pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled)
		VALUES ($1,'zammad','builtin',TRUE), ($1,'gitlab','builtin',TRUE), ($1,'teams','builtin',TRUE)`, s.orgID); err != nil {
		t.Fatal(err)
	}
	hash, _ := identbuiltin.HashPassword("admin-passwort")
	s.adminID = uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,'admin@test.local','Admin',$3,'platform_admin')`, s.adminID, s.orgID, hash); err != nil {
		t.Fatal(err)
	}
	return s
}

// newSupportAgent creates the standard test agent (mock runtime, Zammad access).
func (s *stack) newSupportAgent(slug string) agents.Agent {
	s.t.Helper()
	agent, err := s.registry.Create(context.Background(), s.orgID, slug, "Support-Agent", "mock", &s.adminID)
	if err != nil {
		s.t.Fatal(err)
	}
	_, err = s.registry.SaveConfig(context.Background(), agent.ID, map[string]string{
		"SOUL.md":   "# Support-Agent\n\n## Role\nSupport.",
		"ACCESS.md": "- system: zammad scope: read,write",
	}, &s.adminID)
	if err != nil {
		s.t.Fatal(err)
	}
	return agent
}

// fakeZammad is the target system: it records all requests.
type fakeZammad struct {
	mu       sync.Mutex
	replies  []map[string]any
	updates  []map[string]any
	requests []string
	srv      *httptest.Server
}

func newFakeZammad(t *testing.T) *fakeZammad {
	f := &fakeZammad{}
	mux := httptest.NewServer(httpHandler(f))
	f.srv = mux
	t.Cleanup(mux.Close)
	return f
}

func (f *fakeZammad) record(kind string, m map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch kind {
	case "reply":
		f.replies = append(f.replies, m)
	case "update":
		f.updates = append(f.updates, m)
	}
}

func (f *fakeZammad) replyCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.replies)
}

func (f *fakeZammad) lastUpdate() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.updates) == 0 {
		return nil
	}
	return f.updates[len(f.updates)-1]
}

// waitFor polls a condition until it holds or the time limit is exceeded.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

func (s *stack) taskState(id uuid.UUID) string {
	task, err := s.backlog.Get(context.Background(), id)
	if err != nil {
		return "?"
	}
	return task.State
}

func (s *stack) agentStatus(id uuid.UUID) string {
	a, err := s.registry.Get(context.Background(), id)
	if err != nil {
		return "?"
	}
	return a.Status
}

func signWebhook(body []byte) string {
	mac := hmac.New(sha1.New, []byte(webhookSecret))
	mac.Write(body)
	return "sha1=" + hex.EncodeToString(mac.Sum(nil))
}
