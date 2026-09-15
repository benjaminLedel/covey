// Package integration runs the complete vertical slice from spec/11 as a test:
// real Postgres (pgvector), real HTTP server, real WebSocket, in-process daemon
// with mock runtime, fake Zammad. Only the LLM is replaced.
package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/accounts"
	"covey/internal/agents"
	"covey/internal/audit"
	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/db"
	"covey/internal/dream"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/homestore"
	"covey/internal/httpapi"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/mail"
	"covey/internal/memory"
	"covey/internal/notify"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/runner"
	runnerstore "covey/internal/runner/store"
	"covey/internal/runtimes"
	"covey/internal/sandboxfs"
	secbuiltin "covey/internal/secrets/builtin"
	"covey/internal/secrets/sealbox"
	"covey/internal/settings"
	"covey/internal/skills"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"
	"covey/internal/voice"
	"covey/internal/waitlist"
	"covey/internal/workplaces"

	_ "github.com/benjaminLedel/covey-plugin-pack/browser"
	_ "github.com/benjaminLedel/covey-plugin-pack/confluence"
	_ "github.com/benjaminLedel/covey-plugin-pack/dev"
	_ "github.com/benjaminLedel/covey-plugin-pack/email"
	// gitlab ist im Testbinary registriert, weil die Organisation es als
	// Zielsystem aktiviert — und weil target/store Zeilen fuer Plugins
	// verwirft, die dieses Binary nicht einkompiliert hat. Ohne den Import
	// waere die Aktivierung in newStack eine Zeile ohne Wirkung.
	_ "covey/internal/target/mcp"
	"covey/migrations"
	_ "github.com/benjaminLedel/covey-plugin-pack/gitlab"
	_ "github.com/benjaminLedel/covey-plugin-pack/jira"
	_ "github.com/benjaminLedel/covey-plugin-pack/nextcloud"
	_ "github.com/benjaminLedel/covey-plugin-pack/sharepoint"

	_ "github.com/benjaminLedel/covey-plugin-pack/teams"
	_ "github.com/benjaminLedel/covey-plugin-pack/zammad"
)

// The test Postgres. `make dev-db` puts it on port 5433; a CI runner that
// offers the database under a different host or port says so in
// COVEY_TEST_DATABASE_URL. The per-test database is created beside the one
// named here, so the DSN has to point at a database the user may connect to
// and must carry the credentials for CREATE DATABASE.
var adminDBURL = envOr("COVEY_TEST_DATABASE_URL", "postgres://covey:covey@localhost:5433/covey?sslmode=disable")

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// testDBURL is adminDBURL with the database name swapped for the throwaway one
// a test works in — and with the connection pool pinned small.
//
// Small on purpose. pgx sizes the pool by the core count, so a developer
// machine gets sixteen connections and a two-core server gets four, and a
// whole class of fault is invisible at sixteen: code that holds a connection
// and asks the same pool for a second one. A registration did exactly that —
// a transaction, and a mail send inside it that read its settings from the
// pool — and six at once wedged every connection with nothing to break the
// tie (#241). It surfaced only once CI ran the suite on a two-core runner.
//
// Four is the floor a real instance has, not a number below anything anybody
// runs. The whole suite passes at it and takes eight per cent longer, which is
// a cheap price for making that class fail here rather than on somebody's
// instance.
func testDBURL(dbName string) string {
	u, err := url.Parse(adminDBURL)
	if err != nil {
		return adminDBURL
	}
	u.Path = "/" + dbName
	q := u.Query()
	q.Set("pool_max_conns", "4")
	u.RawQuery = q.Encode()
	return u.String()
}

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

// AgentFiles satisfies orchestrator.FileAccess: in-process too, the home lives
// on disk. Without it the file browser (spec/02) could not be checked in the
// vertical slice — it hangs on exactly this port.
func (p *inprocProvider) AgentFiles(agentID uuid.UUID) (sandboxfs.Tree, error) {
	return sandboxfs.New(p.homeBase+"/"+agentID.String(), -1, -1)
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
	t        *testing.T
	pool     *pgxpool.Pool
	registry *agents.Registry
	backlog  *backlog.Store
	obs      *observability.Store
	rails    *guardrails.Store
	secrets  *secbuiltin.Store
	runtimes *runtimes.Store
	mem      *memory.Store
	targets  *targetstore.Store
	egress   *egress.Store
	runners  *runnerstore.Store
	skills   *skills.Store
	voices   *voice.Store
	reqlog   *reqlogstore.Store
	// workplaces carries the organisation's own workplaces and the allowlist of
	// images that may run beside a sandbox (spec/16).
	workplaces *workplaces.Store
	templates  *templates.Store
	audit      *audit.Store
	// settings are the instance's own switches. The stack holds the same
	// store the server uses, so a test can prove the mailer (mail_test.go)
	// without building a second one over the same table.
	settings *settings.Store
	dreams   *dream.Store
	orch     *orchestrator.Orchestrator
	// srv is the HTTP server itself, so a test can equip it with something the
	// basic stack deliberately leaves out — a plugin catalogue, for instance.
	srv        *httpapi.Server
	stopRunner context.CancelFunc
	http       *httptest.Server
	orgID      uuid.UUID
	adminID    uuid.UUID
	homeBase   string
	cancel     context.CancelFunc
}

// setRunnerPool hands the pool and the home store to the HTTP server, so that
// a remote runner can connect and reach its blocks. Done after the fact
// because the pool only comes into being with the provider.
func (s *stack) setRunnerPool(pool *runner.Pool, blobs homestore.BlobStore) {
	s.srv.RunnerPool = pool
	s.srv.Blobs = blobs
}

// cancelRunner cuts the connection to the built-in runner — a host that is
// down, a maintenance window. What the tests want to know is what remains
// answerable then.
func (s *stack) cancelRunner() {
	if s.stopRunner != nil {
		s.stopRunner()
	}
}

// errorsAs is errors.As, in a spelling the tests can pass a **T to without
// importing errors everywhere.
func errorsAs(err error, target any) bool { return errors.As(err, target) }

const webhookSecret = "test-webhook-secret"

// stackOpts lets a test replace the pieces of the data plane. Empty = the
// normal stack: in-process daemon, mock runtime.
type stackOpts struct {
	// provider replaces the sandbox provider. Used by the runner tests, which
	// want the real pool in front of a fake docker instead of the shortcut.
	provider func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider
	// readyTimeout shortens the wait for the daemon. A test that checks
	// something is reported INSTEAD of waited out must not itself wait it out.
	readyTimeout time.Duration
	// afterOrch runs once the orchestrator exists — for wiring that points
	// back at it (the runner reporting a dead sandbox, say).
	afterOrch func(*orchestrator.Orchestrator)
	// staleAfter shortens the patience with a busy status nothing backs. Five
	// minutes is right in production and is not a test.
	staleAfter time.Duration
}

// tmpl is the migrated database every test database is copied from.
//
// A test database used to be an empty one plus every migration: about 300 ms
// on a developer machine, a good deal more on a shared runner, and paid once
// per test — several hundred times a run. CREATE DATABASE … TEMPLATE copies a
// migrated one in about 20 ms. The template is built once per test process, on
// first use, and TestMain drops it. Its name is unique per process, because
// `go test ./...` runs other packages against the same server at the same time.
var tmpl struct {
	once sync.Once
	name string
	err  error
}

func migratedTemplate(ctx context.Context, admin *pgxpool.Pool) (string, error) {
	tmpl.once.Do(func() {
		name := "covey_tpl_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
		if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
			tmpl.err = err
			return
		}
		// Named before it is migrated, so that TestMain drops a half-built one
		// as well.
		tmpl.name = name
		pool, err := db.Connect(ctx, testDBURL(name))
		if err != nil {
			tmpl.err = err
			return
		}
		_, tmpl.err = db.MigrateUp(ctx, pool, migrations.FS)
		pool.Close()
	})
	return tmpl.name, tmpl.err
}

// copyTemplate creates dbName as a copy of the template. Postgres refuses while
// a session is still connected to the template (SQLSTATE 55006), and the
// backends of a pool that was just closed take a moment to go — so that one
// refusal is retried for a few seconds, and every other error returns at once.
func copyTemplate(ctx context.Context, admin *pgxpool.Pool, dbName, tpl string) error {
	var err error
	for range 50 {
		_, err = admin.Exec(ctx, "CREATE DATABASE "+dbName+" TEMPLATE "+tpl)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "55006" {
			return err
		}
		time.Sleep(100 * time.Millisecond)
	}
	return err
}

func newStack(t *testing.T) *stack {
	t.Helper()
	return newStackWith(t, stackOpts{})
}

func newStackWith(t *testing.T, opts stackOpts) *stack {
	t.Helper()
	ctx := context.Background()

	admin, err := db.Connect(ctx, adminDBURL)
	if err != nil {
		t.Skipf("no test Postgres at %s: %v", adminDBURL, err)
	}
	tpl, err := migratedTemplate(ctx, admin)
	if err != nil {
		t.Fatalf("building the migrated template database: %v", err)
	}
	dbName := "covey_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if err := copyTemplate(ctx, admin, dbName, tpl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		admin.Exec(context.Background(), "DROP DATABASE "+dbName+" WITH (FORCE)")
		admin.Close()
	})

	pool, err := db.Connect(ctx, testDBURL(dbName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

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
		runtimes: runtimes.New(pool, secretStore),
		mem:      memory.NewStore(pool, memory.HashEmbedder{}),
	}

	s.targets = targetstore.NewStore(pool)
	s.egress = egress.NewStore(pool)
	s.runners = runnerstore.NewStore(pool)
	s.skills = skills.NewStore(pool)
	s.voices = voice.New(pool)
	s.workplaces = workplaces.New(pool)
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
	box, err := sealbox.New(masterKey)
	if err != nil {
		t.Fatal(err)
	}
	s.settings = settings.New(pool, box)
	s.dreams = dream.NewStore(pool, s.mem, log)
	s.homeBase = t.TempDir()

	var provider orchestrator.SandboxProvider = &inprocProvider{homeBase: s.homeBase, log: log}
	if opts.provider != nil {
		provider = opts.provider(s.homeBase, log)
	}
	readyTimeout := 10 * time.Second
	if opts.readyTimeout > 0 {
		readyTimeout = opts.readyTimeout
	}

	s.orch = orchestrator.New(orchestrator.Options{
		Pool: pool, Registry: s.registry, Backlog: s.backlog, Obs: s.obs,
		Rails: s.rails, Secrets: secretStore, Runtimes: s.runtimes, Identity: idp, Memory: s.mem,
		Targets:        s.targets,
		Skills:         s.skills,
		Voices:         s.voices,
		Workplaces:     s.workplaces,
		ReqLog:         s.reqlog,
		Notify:         notify.New(pool).WithSettings(s.settings),
		Provider:       provider,
		DaemonTokenTTL: 5 * time.Minute,
		TickInterval:   300 * time.Millisecond,
		ReadyTimeout:   readyTimeout,
		StaleAfter:     opts.staleAfter,
		Log:            log,
	})
	if opts.afterOrch != nil {
		opts.afterOrch(s.orch)
	}

	srv := &httpapi.Server{
		Pool: pool, Registry: s.registry, Backlog: s.backlog, Obs: s.obs,
		Rails: s.rails, Secrets: secretStore, Runtimes: s.runtimes, Identity: idp, Memory: s.mem,
		Org: org.NewStore(pool), Targets: s.targets,
		// Self-registration (FR-002): the public endpoints answer 404 without
		// these, so a stack that lacks them would test the wrong thing.
		Settings: s.settings, Accounts: accounts.New(pool), Waitlist: waitlist.New(pool),
		// The mailer as in production — an SMTP sender over the stored
		// settings. Without a host configured it refuses, which is exactly
		// what an unconfigured instance does.
		Mail: mail.New(s.settings),
		// The notification switches (#169). Without the store the endpoints
		// answer 503, and a test would be checking the wrong thing.
		Notify:    notify.New(pool).WithSettings(s.settings),
		Templates: s.templates, Dreams: s.dreams, Audit: s.audit,
		Skills: s.skills,
		// The voice library (spec/24): without the store the endpoints answer
		// 503, and the test would be checking the absence of a feature.
		Voices:      s.voices,
		EgressStore: s.egress,
		Runners:     s.runners,
		// The organisation's own workplaces and its service-image allowlist:
		// wired here because these endpoints are the only way either is
		// administered, and an unwired store answers 503 to a test that meant
		// to check what the store does.
		OrgWorkplaces: s.workplaces,
		ReqLog:        s.reqlog,
		Orch:          s.orch, Log: log,
		WebhookSecrets: map[string]string{"zammad": webhookSecret, "jira": webhookSecret},
		SessionTTL:     time.Hour,
	}
	s.srv = srv
	s.http = httptest.NewServer(srv.Handler())
	t.Cleanup(s.http.Close)
	s.orch.PublicWSURL = strings.Replace(s.http.URL, "http", "ws", 1) + "/api/daemon/ws"

	orchCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel

	// Abbrechen UND abwarten. Die Reihenfolge der Eintragung ist hier die
	// umgekehrte der Ausführung: t.Cleanup läuft zuletzt-zuerst, also wird das
	// Warten ZUERST eingetragen, damit es NACH dem cancel läuft — und vor dem
	// Löschen des Verzeichnisses, das t.TempDir() weiter oben eingetragen hat
	// (das läuft als allerletztes).
	//
	// Ohne das Warten löschte der Test sein Home-Verzeichnis, während eine
	// Sitzung noch hineinschrieb: "TempDir RemoveAll cleanup: directory not
	// empty", bei einem Test, dessen eigene Prüfungen alle gehalten hatten.
	orchFertig := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-orchFertig:
		case <-time.After(45 * time.Second):
			t.Error("der Orchestrator hört nicht auf — irgendetwas läuft nach dem Abbruch weiter")
		}
	})
	t.Cleanup(cancel)
	go func() {
		defer close(orchFertig)
		_ = s.orch.Run(orchCtx)
	}()
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
	// Login sits on the account, the seat points at it (P1) — a stack that
	// created only the seat would have an admin who cannot sign in.
	accountID := uuid.New()
	if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		VALUES ($1,'admin@test.local',$2,'Admin',now())`, accountID, hash); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,'admin@test.local','Admin',$4,'org_admin')`,
		s.adminID, s.orgID, accountID, hash); err != nil {
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

// waitFactor stretches every wall-clock budget in this suite. The budgets were
// written on a developer machine; a shared CI runner does the same work at a
// third of the speed — the same package took 227 s on one run and 560 s on the
// next, and the test that starts a real Chrome then lost a race it wins
// everywhere else (#244).
//
// Stretching costs nothing while a test passes: waitFor returns the moment the
// condition holds, not when the timer runs out. Only a genuine failure waits
// longer. The default is 1, so a local `make test-integration` is unchanged.
var waitFactor = func() float64 {
	f, err := strconv.ParseFloat(os.Getenv("COVEY_TEST_WAIT_FACTOR"), 64)
	if err != nil || f < 1 {
		return 1
	}
	return f
}()

// waitFor polls a condition until it holds or the time limit is exceeded.
func waitFor(t *testing.T, what string, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Duration(float64(timeout) * waitFactor))
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout: %s", what)
}

// waitForOr is waitFor with a witness. "timeout" alone says that something did
// not happen, never what did — and on a CI runner nobody can attach a debugger
// to, that difference is the whole diagnosis.
func waitForOr(t *testing.T, what string, timeout time.Duration, cond func() bool, witness func() string) {
	t.Helper()
	deadline := time.Now().Add(time.Duration(float64(timeout) * waitFactor))
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timeout: %s — %s", what, witness())
}

// recordingSummary renders an agent's recording in one line per event: what it
// did, whether it worked, and the error if it did not.
func (s *stack) recordingSummary(agentID uuid.UUID) string {
	rows, err := s.pool.Query(context.Background(), `SELECT kind,
		coalesce(payload->>'action', ''), coalesce(payload->>'ok', ''), coalesce(payload->>'error', '')
		FROM recording_events WHERE agent_id=$1 ORDER BY id`, agentID)
	if err != nil {
		return "unreadable: " + err.Error()
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var kind, action, ok, errText string
		if err := rows.Scan(&kind, &action, &ok, &errText); err != nil {
			return "unreadable: " + err.Error()
		}
		line := kind
		if action != "" {
			line += " " + action
		}
		if ok != "" {
			line += " ok=" + ok
		}
		if errText != "" {
			line += " error=" + errText
		}
		out = append(out, line)
	}
	if len(out) == 0 {
		return "no events at all"
	}
	return strings.Join(out, " | ")
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

// mitglied legt einen Sitz samt Login an. Seit P1 gehoeren beide zusammen: ein
// Mensch ohne Konto koennte sich nicht anmelden, und genau das haben die Tests
// vorher unbemerkt gebaut.
func (s *stack) mitglied(t *testing.T, email, name, rolle, passwort string) uuid.UUID {
	t.Helper()
	hash, err := identbuiltin.HashPassword(passwort)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	accountID := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		VALUES ($1,$2,$3,$4,now()) ON CONFLICT (email) DO NOTHING`, accountID, email, hash, name); err != nil {
		t.Fatal(err)
	}
	if err := s.pool.QueryRow(ctx, `SELECT id FROM accounts WHERE email=$1`, email).Scan(&accountID); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	if _, err := s.pool.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`, id, s.orgID, accountID, email, name, hash, rolle); err != nil {
		t.Fatal(err)
	}
	return id
}

// alsSystemadmin erhebt den Stack-Admin auf die Instanz-Ebene. Nötig überall
// dort, wo ein Test eine ZWEITE Organisation braucht, um Isolation zu prüfen:
// Mandanten anzulegen ist seit P2 keine Frage der Organisations-Rolle mehr
// (FR-003, Befund F).
func (s *stack) alsSystemadmin(t *testing.T) {
	t.Helper()
	if err := accounts.New(s.pool).SetPlatformRole(context.Background(),
		"admin@test.local", accounts.RoleSystemAdmin); err != nil {
		t.Fatal(err)
	}
}
