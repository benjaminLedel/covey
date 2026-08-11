// covey is the one binary of the control plane (spec/10): API/BFF +
// orchestration core + embedded frontend + embedded migrations.
// Subcommands: serve, migrate (up|down), bootstrap, passwd, config lint.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"

	"covey/internal/agents"
	"covey/internal/audit"
	"covey/internal/backlog"
	"covey/internal/buildinfo"
	"covey/internal/claudeapi"
	"covey/internal/config"
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
	"covey/internal/reqlog"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/runtimes"
	secbuiltin "covey/internal/secrets/builtin"
	"covey/internal/skills"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"
	"covey/migrations"
	"covey/web"

	// Compiled-in target system plugins: blank import = shipped. Whoever wants
	// to build Covey without a system removes its line — the rest stays as it is.
	_ "covey/internal/target/browser"
	_ "covey/internal/target/dev"
	_ "covey/internal/target/email"
	_ "covey/internal/target/github"
	_ "covey/internal/target/gitlab"
	_ "covey/internal/target/ios"
	_ "covey/internal/target/nextcloud"
	_ "covey/internal/target/sharepoint"
	_ "covey/internal/target/teams"
	_ "covey/internal/target/vulndb"
	_ "covey/internal/target/zammad"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	// version runs before the config: "which build is this?" has to give an
	// answer even when the environment is incomplete.
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Println("covey " + buildinfo.String())
		// Covey is a network service under AGPL-3.0: whoever offers a modified
		// version to others owes them the source. Naming the address right here
		// turns that from a research task into a trifle for operators.
		fmt.Println("AGPL-3.0 · source: " + buildinfo.SourceURL)
		return
	}
	cfg, err := config.FromEnv()
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch os.Args[1] {
	case "serve":
		err = runServe(ctx, cfg, log)
	case "migrate":
		err = runMigrate(ctx, cfg, os.Args[2:], log)
	case "bootstrap":
		err = runBootstrap(ctx, cfg, log)
	case "passwd":
		err = runPasswd(ctx, cfg, os.Args[2:], log)
	case "egress-proxy":
		err = runEgressProxy(ctx, cfg, log)
	case "config":
		err = runConfigLint(ctx, cfg, os.Args[2:])
	case "genkey":
		var key string
		if key, err = secbuiltin.GenerateMasterKey(); err == nil {
			fmt.Println(key)
		}
	default:
		usage()
		os.Exit(2)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Error(os.Args[1], "err", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `covey — control plane for AI agents

  covey migrate up|down   apply migrations / roll one back
  covey bootstrap         create organization + admin + demo agent (idempotent)
  covey passwd <email>    set a user's password anew (emergency reset)
  covey serve             start API + orchestrator + admin UI
  covey egress-proxy      egress allowlist proxy (network isolation mode, in the container)
  covey config lint       check agent configs for known pitfalls (changes nothing)
  covey genkey            generate a new COVEY_MASTER_KEY
  covey version           version, commit and build time of this binary

Configuration via ENV: COVEY_DATABASE_URL, COVEY_LISTEN_ADDR, COVEY_PUBLIC_URL,
COVEY_MASTER_KEY, COVEY_SANDBOX_IMAGE, COVEY_DATA_DIR, COVEY_ZAMMAD_WEBHOOK_SECRET,
COVEY_ADMIN_EMAIL, COVEY_ADMIN_PASSWORD (bootstrap), COVEY_COOKIE_SECURE …`)
}

func runMigrate(ctx context.Context, cfg config.Config, args []string, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	dir := "up"
	if len(args) > 0 {
		dir = args[0]
	}
	switch dir {
	case "up":
		n, err := db.MigrateUp(ctx, pool, migrations.FS)
		if err != nil {
			return err
		}
		log.Info("migrations applied", "count", n)
		return nil
	case "down":
		return db.MigrateDown(ctx, pool, migrations.FS)
	default:
		return fmt.Errorf("unknown direction %q (up|down)", dir)
	}
}

// runBootstrap idempotently creates the organization, the admin login and the
// demo support agent including config-as-code and the default guard-rails.
func runBootstrap(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		return err
	}

	// Organization.
	var orgID uuid.UUID
	err = pool.QueryRow(ctx, "SELECT id FROM organizations LIMIT 1").Scan(&orgID)
	if err != nil {
		orgID = uuid.New()
		if _, err := pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,$2)",
			orgID, "Demo organization"); err != nil {
			return err
		}
		// Seed the base allowlist (configurable through the egress UI).
		if _, err := pool.Exec(ctx, `INSERT INTO egress_default_hosts (org_id, pattern, note)
			VALUES ($1, 'api.anthropic.com', 'LLM endpoint of the Claude runtime')
			ON CONFLICT DO NOTHING`, orgID); err != nil {
			return err
		}
		log.Info("organization created", "id", orgID)
	}

	// The admin human.
	adminEmail := strings.ToLower(getenvDefault("COVEY_ADMIN_EMAIL", "admin@covey.local"))
	adminPass := os.Getenv("COVEY_ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = "covey-admin"
		log.Warn("bootstrap: COVEY_ADMIN_PASSWORD not set — using the well-known default password 'covey-admin'. Change it before any non-local use (set COVEY_ADMIN_PASSWORD or change the password in the UI).")
	}
	var adminID uuid.UUID
	err = pool.QueryRow(ctx, "SELECT id FROM humans WHERE email=$1", adminEmail).Scan(&adminID)
	if err != nil {
		// No human with the bootstrap e-mail. If a platform admin already
		// exists (renamed or created under a different address), NO new one is
		// created — otherwise every deploy would resurrect an admin that was
		// deleted or changed in the UI. A new one is created only when the
		// organization has no platform admin at all (fresh installation or
		// lockout recovery).
		err = pool.QueryRow(ctx, `SELECT id, email FROM humans WHERE org_id=$1 AND role='platform_admin'
			ORDER BY created_at LIMIT 1`, orgID).Scan(&adminID, &adminEmail)
		if err != nil {
			hash, err := identbuiltin.HashPassword(adminPass)
			if err != nil {
				return err
			}
			adminID = uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
				VALUES ($1,$2,$3,'Platform Admin',$4,'platform_admin')`,
				adminID, orgID, adminEmail, hash); err != nil {
				return err
			}
			log.Info("admin created", "email", adminEmail)
		}
	}

	// A demo agent including SOUL.md & co — but only into an organisation that
	// has none yet. Tied to "are there any agents at all" and no longer to one
	// fixed slug: whoever already runs a workforce does not need a stranger
	// reappearing on the overview after every deploy, and whoever is starting
	// out should not face an empty page.
	registry := agents.NewRegistry(pool)
	workforce, err := registry.List(ctx, orgID)
	if err != nil {
		return err
	}
	var agent agents.Agent
	if len(workforce) == 0 {
		agent, err = registry.Create(ctx, orgID, "demo", "Demo agent", "claude-code", &adminID)
		if err != nil {
			return err
		}
		if _, err := registry.SaveConfig(ctx, agent.ID, defaultDemoConfig(), &adminID); err != nil {
			return err
		}
		log.Info("demo agent created", "id", agent.ID)
	} else {
		agent = workforce[0]
	}
	// Make sure the default board exists (idempotent) — also for a demo agent
	// that already came out of an earlier bootstrap round.
	if err := backlog.NewStore(pool).SeedDefaultStages(ctx, agent.ID); err != nil {
		return err
	}

	// And a workplace. Without one the demo agent reaches no credential, so its
	// first run fails at the login even though the token has long been
	// deposited — the checklist would tick off cleanly and lead nowhere.
	// EnsureDefault is idempotent and takes in every agent still without an
	// assignment, so a bootstrap repeated after the fact fixes an installation
	// that came from an older version.
	//
	// Not fatal: an organisation, an admin and an agent are the point of this
	// command; the workplace can be added in the UI, but a bootstrap that
	// aborts halfway leaves nobody able to log in at all.
	if secretStore, serr := secbuiltin.New(pool, cfg.MasterKeyHex); serr != nil {
		log.Warn("no workplace set up: the secret store is unavailable", "err", serr)
	} else if _, rerr := runtimes.New(pool, secretStore).EnsureDefault(ctx, orgID, agent.Runtime); rerr != nil {
		log.Warn("no workplace set up — agents reach no credential until one exists",
			"engine", agent.Runtime, "err", rerr)
	}

	// Default guard-rails (fail-closed base set, spec/06).
	rails := guardrails.NewStore(pool)
	existing, err := rails.List(ctx, orgID)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		defaults := []guardrails.Rule{
			{OrgID: orgID, ScopeLevel: "global", RuleType: guardrails.RuleRequireApproval,
				Pattern: "zammad:reply_external"}, // external replies only with approval
			{OrgID: orgID, ScopeLevel: "global", RuleType: guardrails.RuleDenySystem,
				Pattern: "hr*"}, // HR systems are off limits for agents
			{OrgID: orgID, ScopeLevel: "global", RuleType: guardrails.RuleDenyAction,
				Pattern: "*:delete*"}, // destructive actions are hard-denied
		}
		for _, r := range defaults {
			if _, err := rails.Create(ctx, r); err != nil {
				return err
			}
		}
		log.Info("default guard-rails created", "count", len(defaults))
	}
	log.Info("bootstrap done", "login", adminEmail)
	return nil
}

// runPasswd sets a user's password anew — the emergency route when even the
// platform admin is locked out (the builtin provider deliberately has no
// self-service reset). The password never comes from argv (process list!) but
// from the terminal without echo or as a single line from stdin. All of the
// user's running sessions are invalidated.
func runPasswd(ctx context.Context, cfg config.Config, args []string, log *slog.Logger) error {
	if len(args) != 1 {
		return errors.New("usage: covey passwd <email>")
	}
	email := strings.ToLower(strings.TrimSpace(args[0]))

	pw, err := readNewPassword()
	if err != nil {
		return err
	}
	if len(pw) < 8 {
		return errors.New("password needs at least 8 characters")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var id uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM humans WHERE email=$1", email).Scan(&id); err != nil {
		return fmt.Errorf("no user with e-mail %q", email)
	}
	hash, err := identbuiltin.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "UPDATE humans SET password_hash=$1 WHERE id=$2", hash, id); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "DELETE FROM http_sessions WHERE human_id=$1", id); err != nil {
		return err
	}
	log.Info("password set anew, all sessions invalidated", "email", email)
	return nil
}

// readNewPassword reads the new password: interactively twice without echo,
// non-interactively (pipe) as a single line from stdin.
func readNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "New password: ")
		first, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(os.Stderr, "Repeat: ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(first) != string(second) {
			return "", errors.New("passwords do not match")
		}
		return string(first), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read password from stdin: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// defaultDemoConfig is the agent a fresh installation starts with.
//
// Deliberately WITHOUT a target system. Its predecessor was a support agent
// wired to Zammad, and it was a dead end for everyone who had no Zammad: the
// first steps led up to "watch it work", and the agent could not do a single
// thing it had been described as doing. An agent that works on the task text
// and writes down what it found needs nothing but a credential — and it
// demonstrates the same loop (task → run → recording → memory).
func defaultDemoConfig() map[string]string {
	return map[string]string{
		"SOUL.md": `# Demo agent

## Role
The first agent of this installation. There to be given work and to show
what a run looks like from the inside.

## Assignment
Work on the task as it is written. If it names no target system, do the
work with what is in the sandbox — think, research in your memory, write.
Record the result in the task itself, so a human can read it there.

## Tone
Terse and factual. Say what you did and what you could not do.

## Limits
- No target system is connected yet. Do not act as if one were.
- Where information is missing, say so rather than inventing it.`,
		"CAPABILITIES.md": `# Capabilities

- Responsible for: whatever is put in this agent's backlog.
- Not responsible for: anything with real-world effects — this agent has no
  target system, and it is meant for getting to know the platform.

Replace this file as soon as the agent has a real job. Ready-made bundles for
common jobs (coding, QA, research, log triage) are in examples/.`,
		"PLAYBOOKS.md": `# Playbooks

## Working through a task
1. Read the task and check your memory: has this come up before?
2. Do the work. Without a target system that means: think it through and
   write the result down.
3. Note the result in the task, in a form a human can read.
4. Write down what is worth keeping for next time — the wiki memory is
   yours, and it is what makes the second run better than the first.`,
		"ORG.md": `# Organization

Supervisor: the platform admin (human). Ask there when a task is unclear.`,
	}
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// egressBaseAllow is the infrastructure add-on from COVEY_EGRESS_ALLOW (ENV) —
// changeable only via config, shown in the UI as "ENV" (e.g.
// host.docker.internal for the proxy container). Everything domain-related —
// the LLM endpoint included — sits configurably in the DB: the org's base
// allowlist (egress_default_hosts, seeded with api.anthropic.com), templates
// and the agent's own hosts.
func egressBaseAllow(cfg config.Config) []string {
	return append([]string{}, cfg.EgressAllow...)
}

// rewriteDBForContainer bends a loopback DB URL onto host.docker.internal so
// the egress proxy container reaches the Postgres instance on the host.
// Non-loopback hosts (a real DB deployment) stay untouched.
func rewriteDBForContainer(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return dsn
	}
	host := "host.docker.internal"
	if port := u.Port(); port != "" {
		host += ":" + port
	}
	u.Host = host
	return u.String()
}

// runEgressProxy runs the egress allowlist proxy as a standalone process
// (network isolation mode: it runs in the proxy container as the isolated
// sandbox's only way out). The allowlist comes from DB + ENV + code default and
// is reloaded periodically so UI changes take effect without a restart.
func runEgressProxy(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	// The DB may not be reachable yet at startup: in network mode the internal
	// network is attached to the bridge network only AFTER the container has
	// started. Retry until then — the process stays alive (container
	// "running") so that `docker network connect` has something to work with.
	var store *egress.Store
	for store == nil {
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Warn("egress-proxy: DB not reachable yet, retrying", "err", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
			continue
		}
		defer pool.Close()
		store = egress.NewStore(pool)
	}

	resolver := egress.NewDBResolver(ctx, store, egressBaseAllow(cfg), 15*time.Second, log)
	proxy := egress.New(resolver, log)
	addr, err := proxy.Start(cfg.EgressProxyAddr)
	if err != nil {
		return err
	}
	defer proxy.Close()
	log.Info("covey egress-proxy", "addr", addr, "base", egressBaseAllow(cfg))
	<-ctx.Done()
	return nil
}

// startEgressProxy binds the cooperative proxy (inside the control plane
// process) to a free port (all interfaces, so the container reaches it via
// host.docker.internal). Returns the container-side base proxy URL (without
// credentials — the provider appends the per-sandbox token per agent) plus a
// close function.
func startEgressProxy(ctx context.Context, cfg config.Config, store *egress.Store, log *slog.Logger) (string, func(), error) {
	resolver := egress.NewDBResolver(ctx, store, egressBaseAllow(cfg), 15*time.Second, log)
	proxy := egress.New(resolver, log)
	addr, err := proxy.Start(":0")
	if err != nil {
		return "", nil, err
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		_ = proxy.Close()
		return "", nil, err
	}
	containerURL := "http://host.docker.internal:" + port
	log.Info("egress enforcement active (cooperative)", "proxy", containerURL, "base", egressBaseAllow(cfg))
	return containerURL, func() { _ = proxy.Close() }, nil
}

// buildEmbedder picks the embedding for the wiki memory (spec/05). "builtin"
// is the offline fallback without an API key; every other provider is
// validated strictly — a broken configuration should abort startup instead of
// falling back to the weaker hash embedding unnoticed.
func buildEmbedder(cfg config.Config, log *slog.Logger) (memory.Embedder, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.EmbeddingProvider))
	if provider == "" || provider == "builtin" {
		log.Warn("wiki embedding: built-in hash active — the vector search only measures word overlap; " +
			"for semantic retrieval set COVEY_EMBEDDING_PROVIDER=voyage|openai + COVEY_EMBEDDING_API_KEY")
		return memory.HashEmbedder{}, nil
	}
	e, err := memory.NewAPIEmbedder(provider, cfg.EmbeddingModel, cfg.EmbeddingAPIKey, cfg.EmbeddingURL, log)
	if err != nil {
		return nil, err
	}
	log.Info("wiki embedding active", "model", e.Name())
	return e, nil
}

// startReembed brings existing pages up to the active embedding model in the
// background. In the background because the serve startup should not wait for
// hundreds of calls; until then the affected pages simply cannot be found
// through the vector search (the query filters on the active model), but they
// are not lost.
//
// With retries, because a self-hosted embedding service is typically still
// loading its model on first startup: without following up, the wiki would
// stay unfindable until the next restart of the control plane, even though the
// service is ready two minutes later.
const reembedRetryDelay = time.Minute
const reembedRetries = 30

func startReembed(ctx context.Context, mem *memory.Store, log *slog.Logger) {
	stale, err := mem.StaleCount(ctx)
	if err != nil || stale == 0 {
		return
	}
	log.Info("wiki embedding: bringing existing pages up to date", "pages", stale, "model", mem.EmbedderName())
	go func() {
		for attempt := 0; attempt < reembedRetries; attempt++ {
			n, err := mem.ReembedStale(ctx, 50)
			if err == nil {
				log.Info("wiki embedding: existing pages brought up to date", "pages", n)
				return
			}
			log.Warn("wiki embedding: catching up interrupted, retrying",
				"reembedded", n, "in", reembedRetryDelay, "err", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(reembedRetryDelay):
			}
		}
		log.Error("wiki embedding: existing pages could not be brought up to date — " +
			"the wiki search does not find the affected pages")
	}()
}

func runServe(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.MasterKeyHex == "" {
		return fmt.Errorf("COVEY_MASTER_KEY missing (generate one with `covey genkey`)")
	}
	// Make hardening hints for non-local deployments visible.
	for _, warn := range cfg.SecurityWarnings() {
		log.Warn("security: " + warn)
	}
	// An address the sandboxes cannot reach shuts down the entire data plane —
	// that belongs at startup, not in the timeout of every single agent
	// session.
	for _, warn := range cfg.DataPlaneWarnings() {
		log.Warn("data-plane: " + warn)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Auto-migrate at startup — the advisory lock serializes HA instances.
	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		return err
	}

	idp, err := identbuiltin.New(pool)
	if err != nil {
		return err
	}
	secretStore, err := secbuiltin.New(pool, cfg.MasterKeyHex)
	if err != nil {
		return err
	}

	registry := agents.NewRegistry(pool)
	// Platform-wide wiki cleanup heartbeat (COVEY_WIKI_CLEANUP): the control
	// plane periodically files a backlog task for every agent in which it tends
	// its wiki (merge duplicates, fix dead links — spec/05). Overridable per
	// agent via HEARTBEAT.md; an empty schedule = off.
	if hb, enabled, err := agents.WikiCleanupHeartbeat(cfg.WikiCleanup); err != nil {
		return err
	} else if enabled {
		registry.SystemHeartbeats = append(registry.SystemHeartbeats, hb)
		sched := hb.DailyAt
		if sched == "" {
			sched = hb.Every.String()
		}
		log.Info("wiki cleanup heartbeat active", "schedule", sched)
	}
	// Call this even with the feature switched off — it clears out orphaned system heartbeats.
	if err := registry.ReconcileSystemHeartbeats(ctx); err != nil {
		return err
	}
	backlogStore := backlog.NewStore(pool)
	obs := observability.NewStore(pool)
	rails := guardrails.NewStore(pool)
	// The embedding behind the wiki retrieval (spec/05). The built-in hash only
	// measures word overlap; only a real provider turns the vector search into
	// a semantic one. A misconfiguration aborts startup instead of silently
	// falling back to the weaker embedding — otherwise nobody notices.
	embedder, err := buildEmbedder(cfg, log)
	if err != nil {
		return err
	}
	mem := memory.NewStore(pool, embedder)
	startReembed(ctx, mem, log)
	dreams := dream.NewStore(pool, mem, log)
	targets := targetstore.NewStore(pool)
	egressStore := egress.NewStore(pool)
	templateStore := templates.NewStore(pool)
	auditStore := audit.NewStore(pool)
	skillStore := skills.NewStore(pool)

	// Egress enforcement can only be enforced with real network isolation (docker).
	egressEnforced := cfg.EgressEnforce && cfg.SandboxProvider == "docker"

	var provider orchestrator.SandboxProvider
	switch cfg.SandboxProvider {
	case "docker":
		dp := &orchestrator.DockerProvider{Image: cfg.SandboxImage, DataDir: cfg.DataDir}
		if egressEnforced {
			switch cfg.EgressIsolation {
			case "network":
				// Hard isolation: sandbox without internet, the proxy container
				// as its only way out. The proxy reads the allowlist from the DB
				// itself (live reload by polling) — hence no host-side reload.
				dp.EgressIsolation = "network"
				dp.EgressProxyImage = "covey-egress:latest"
				dp.EgressProxyEnv = map[string]string{
					"COVEY_DATABASE_URL":      rewriteDBForContainer(cfg.DatabaseURL),
					"COVEY_EGRESS_ALLOW":      strings.Join(append(append([]string{}, cfg.EgressAllow...), "host.docker.internal"), ","),
					"COVEY_EGRESS_PROXY_ADDR": ":8888",
				}
				log.Info("egress enforcement: hard network isolation active", "proxy-image", dp.EgressProxyImage)
			default:
				// Cooperative: proxy inside the control plane process, container via HTTP_PROXY.
				proxyURL, closeProxy, err := startEgressProxy(ctx, cfg, egressStore, log)
				if err != nil {
					return err
				}
				defer closeProxy()
				dp.EgressProxyURL = proxyURL
			}
		}
		provider = dp
	default:
		return fmt.Errorf("sandbox provider %q: only 'docker' is implemented", cfg.SandboxProvider)
	}

	// Ask the data plane at startup whether it could start a sandbox at all.
	// Without this the answer arrives at the first wake, inside the recording of
	// a task — the one place where nobody looks who is still setting the
	// platform up. A warning and not an abort: an instance whose image is built
	// afterwards is a normal case, and everything that is not a run (config,
	// backlog, org chart) works meanwhile.
	dataPlane, _ := provider.(orchestrator.DataPlaneChecker)
	if dataPlane != nil {
		for _, problem := range dataPlane.Check(ctx) {
			log.Warn("data-plane: " + problem)
		}
	}

	// Request log: the HTTP requests at the edges of the platform (spec/06).
	// The store is at the same time the default sink — that way the target
	// system calls the control plane makes itself (work checks, JWKS fetch) are
	// logged too, without every call site knowing about it.
	var reqLog *reqlogstore.Store
	if cfg.RequestLog {
		reqLog = reqlogstore.NewStore(pool, log, cfg.RequestLogBodies, cfg.RequestLogRetention)
		reqlog.SetDefault(reqLog.Sink(nil, nil, nil))
		go reqLog.Run(ctx)
		log.Info("request log active", "retention", cfg.RequestLogRetention, "bodies", cfg.RequestLogBodies)
	}

	// The capacity layer: which contract an agent works on, and which of its
	// credentials this run uses (spec/18). It reads values from the secret
	// store and decides nothing about them itself.
	runtimeStore := runtimes.New(pool, secretStore)

	wsURL := strings.Replace(cfg.PublicURL, "http", "ws", 1) + "/api/daemon/ws"
	orch := orchestrator.New(orchestrator.Options{
		Pool: pool, Registry: registry, Backlog: backlogStore, Obs: obs,
		Rails: rails, Secrets: secretStore, Identity: idp, Memory: mem,
		Runtimes:       runtimeStore,
		Targets:        targets,
		Skills:         skillStore,
		Egress:         egressStore,
		ReqLog:         reqLog,
		Provider:       provider,
		PublicWSURL:    wsURL,
		DaemonTokenTTL: cfg.DaemonTokenTTL,
		TickInterval:   cfg.TickInterval,
		BoardRetention: cfg.BoardRetention,
		RuntimeTools:   cfg.RuntimeTools,
		Log:            log,
	})

	dist, err := web.Dist()
	if err != nil {
		return err
	}
	srv := &httpapi.Server{
		BaseCtx: ctx,
		Audit:   auditStore,
		Pool:    pool, Registry: registry, Backlog: backlogStore, Obs: obs,
		Rails: rails, Secrets: secretStore, Runtimes: runtimeStore, Identity: idp, Memory: mem, Dreams: dreams,
		Org: org.NewStore(pool), Targets: targets, Templates: templateStore,
		Skills: skillStore,
		Orch:   orch, WebFS: dist, Log: log,
		WebhookSecrets: cfg.WebhookSecrets,
		SessionTTL:     cfg.SessionTTL,
		SiteURL:        cfg.SiteURL,
		CookieSecure:   cfg.CookieSecure,
		EgressStore:    egressStore,
		EgressEnforced: egressEnforced,
		EgressDefaults: egressBaseAllow(cfg),
		ReqLog:         reqLog,
		DataPlane:      dataPlane,
	}

	httpServer := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: srv.Handler(),
		// Against Slowloris: whoever opens a connection and sends the headers
		// drop by drop used to tie up a connection indefinitely.
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		// ReadTimeout and WriteTimeout deliberately stay OFF:
		// a file upload into the agent home may take long (read), and the SSE
		// stream (/api/v1/events) as well as the daemon WebSocket run for hours
		// (write). A timeout there would tear down the interface and the
		// sandbox connection every couple of minutes.
	}

	go func() {
		if err := orch.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("orchestrator", "err", err)
		}
	}()

	// The agents' night rest: once a night each of them tidies up its wiki. The
	// credential comes per agent from the organization — without one there is
	// no dreaming, quietly and without an error.
	if at := strings.TrimSpace(cfg.DreamAt); at != "" && at != "off" {
		go dreams.RunNightly(ctx, at, func(ctx context.Context, agentID uuid.UUID) (dream.Credential, bool) {
			a, err := registry.Get(ctx, agentID)
			if err != nil {
				return dream.Credential{}, false
			}
			cred, oauth, ok := claudeapi.ResolveOrg(ctx, secretStore, a.OrgID)
			return dream.Credential{Value: cred, OAuth: oauth}, ok
		}, log)
	}
	// Egress log retention: clear out old decisions periodically.
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := egressStore.CleanupLog(ctx, 30*24*time.Hour); err == nil && n > 0 {
					log.Info("egress log cleaned up", "deleted", n)
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		// Graceful shutdown: finish running requests, close the daemons.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("covey serve", "addr", cfg.ListenAddr, "public", cfg.PublicURL, "build", buildinfo.String())
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
