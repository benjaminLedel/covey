// covey is the one binary of the control plane (spec/10): API/BFF +
// orchestration core + embedded frontend + embedded migrations.
// Subcommands: serve, migrate (up|down), bootstrap, passwd, config lint.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"golang.org/x/term"

	"covey/internal/accounts"
	"covey/internal/agents"
	"covey/internal/audit"
	"covey/internal/backlog"
	"covey/internal/buildinfo"
	"covey/internal/config"
	"covey/internal/db"
	"covey/internal/dream"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/homestore"
	"covey/internal/httpapi"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/llm"
	"covey/internal/marketplace"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	"covey/internal/reqlog"

	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/runner"
	runnerstore "covey/internal/runner/store"
	"covey/internal/runtimes"
	"covey/internal/sandbox"
	secbuiltin "covey/internal/secrets/builtin"
	"covey/internal/settings"
	"covey/internal/skills"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"
	"covey/internal/waitlist"
	orgworkplaces "covey/internal/workplaces"
	"covey/migrations"
	"covey/web"

	// Compiled-in target system plugins: blank import = shipped. Whoever wants
	// to build Covey without a system removes its line — the rest stays as it is.
	_ "github.com/benjaminLedel/covey-plugin-pack/browser"
	_ "github.com/benjaminLedel/covey-plugin-pack/dev"
	_ "github.com/benjaminLedel/covey-plugin-pack/email"
	_ "github.com/benjaminLedel/covey-plugin-pack/github"
	_ "github.com/benjaminLedel/covey-plugin-pack/gitlab"
	_ "github.com/benjaminLedel/covey-plugin-pack/k8s"
	_ "github.com/benjaminLedel/covey-plugin-pack/nextcloud"
	_ "github.com/benjaminLedel/covey-plugin-pack/sharepoint"
	_ "github.com/benjaminLedel/covey-plugin-pack/teams"
	_ "github.com/benjaminLedel/covey-plugin-pack/vulndb"
	_ "github.com/benjaminLedel/covey-plugin-pack/zammad"
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
	// plugin lint runs before the config as well: it checks a file, and a file
	// can be checked on a machine that has no database, no master key and no
	// business having either — a catalogue's CI container, for instance.
	case "plugin":
		if err := runPlugin(os.Args[2:]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
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
	case "settings":
		err = runSettings(ctx, cfg, os.Args[2:], log)
	case "waitlist":
		err = runWaitlist(ctx, cfg, os.Args[2:])
	case "system-admin":
		err = runSystemAdmin(ctx, cfg, os.Args[2:])
	case "doctor":
		err = runDoctor(ctx, cfg, os.Args[2:])
	case "home-store":
		err = runHomeStore(ctx, cfg, os.Args[2:], log)
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
  covey plugin lint <f>   check a target-system plugin file (manifest or MCP config)
  covey settings [k v]    show the instance's settings, or set one (e.g. signup.mode waitlist)
  covey waitlist          list waitlist codes | new [-label L] [-uses N] [-days D] | revoke <hash>
  covey system-admin      list | add <email> | remove <email> — the instance level, not an org role
  covey doctor            what a restart or upgrade would run into here (changes nothing)
  covey home-store cleanup [--apply]   retention + sweep over every org's home store
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
		// No human with the bootstrap e-mail. If an org admin already
		// exists (renamed or created under a different address), NO new one is
		// created — otherwise every deploy would resurrect an admin that was
		// deleted or changed in the UI. A new one is created only when the
		// organization has no org admin at all (fresh installation or
		// lockout recovery).
		err = pool.QueryRow(ctx, `SELECT id, email FROM humans WHERE org_id=$1 AND role='org_admin'
			ORDER BY created_at LIMIT 1`, orgID).Scan(&adminID, &adminEmail)
		if err != nil {
			hash, err := identbuiltin.HashPassword(adminPass)
			if err != nil {
				return err
			}
			adminID = uuid.New()
			// The login sits on the account (P1); the seat points at it. A
			// bootstrap that created only the seat would leave an admin who
			// cannot sign in — and that is exactly the situation bootstrap
			// exists to prevent.
			accountID := uuid.New()
			if _, err := pool.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at, platform_role)
				VALUES ($1,$2,$3,'Platform Admin',now(),'system_admin')
				ON CONFLICT (email) DO NOTHING`, accountID, adminEmail, hash); err != nil {
				return err
			}
			if err := pool.QueryRow(ctx, `SELECT id FROM accounts WHERE email=$1`, adminEmail).Scan(&accountID); err != nil {
				return err
			}
			if _, err := pool.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
				VALUES ($1,$2,$3,$4,'Platform Admin',$5,'org_admin')`,
				adminID, orgID, accountID, adminEmail, hash); err != nil {
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
// org admin is locked out (the builtin provider deliberately has no
// self-service reset). The password never comes from argv (process list!) but
// from the terminal without echo or as a single line from stdin. All of the
// user's running sessions are invalidated.
// runSettings shows the instance's settings and changes one of them. The same
// switches the System page will offer later (FR-002) — the command exists
// because the first of them has to be flippable before there is a page, and
// because an installation that has locked itself out of its own interface
// still has a terminal.
//
// Validation is not repeated here: it sits in the store, so the CLI cannot
// permit what the API refuses.
func runSettings(ctx context.Context, cfg config.Config, args []string, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := settings.New(pool)

	switch len(args) {
	case 0:
		werte, err := store.All(ctx)
		if err != nil {
			return err
		}
		for _, k := range settings.Keys() {
			markierung := ""
			if werte[k] == settings.Defaults[k] {
				markierung = "   (default)"
			}
			fmt.Printf("%-20s %s%s\n", k, werte[k], markierung)
		}
		return nil
	case 2:
		if err := store.Set(ctx, args[0], args[1], nil); err != nil {
			return err
		}
		log.Info("setting changed", "key", args[0], "value", args[1])
		return nil
	default:
		return errors.New("usage: covey settings [<key> <value>]")
	}
}

// runSystemAdmin manages the instance level.
//
// Deliberately only here and not over HTTP: system_admin is what protects the
// organisations from one another (FR-003, finding F). An endpoint that grants
// it would be reachable from inside an organisation — which is exactly the
// boundary this role exists to draw. Whoever administers the installation has
// a terminal on it.
func runSystemAdmin(ctx context.Context, cfg config.Config, args []string) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := accounts.New(pool)

	befehl := "list"
	if len(args) > 0 {
		befehl = args[0]
	}
	switch befehl {
	case "list":
		rows, err := pool.Query(ctx,
			`SELECT email FROM accounts WHERE platform_role='system_admin' ORDER BY email`)
		if err != nil {
			return err
		}
		defer rows.Close()
		leer := true
		for rows.Next() {
			var email string
			if err := rows.Scan(&email); err != nil {
				return err
			}
			fmt.Println(email)
			leer = false
		}
		if leer {
			fmt.Println("no system administrator — nobody can administer this installation")
		}
		return rows.Err()

	case "add", "remove":
		if len(args) != 2 {
			return fmt.Errorf("usage: covey system-admin %s <email>", befehl)
		}
		rolle := accounts.RoleSystemAdmin
		if befehl == "remove" {
			rolle = accounts.RoleUser
		}
		if err := store.SetPlatformRole(ctx, args[1], rolle); err != nil {
			return err
		}
		fmt.Printf("%s: %s\n", args[1], rolle)
		return nil
	}
	return fmt.Errorf("unknown: covey system-admin %s (list|add|remove)", befehl)
}

// runWaitlist manages the codes with which the instance opens in stages.
//
// The plaintext is printed exactly once, here — stored is only its hash, as
// for the session tokens. Whoever loses a code gets a new one; that is the
// price of a database dump containing no valid codes.
func runWaitlist(ctx context.Context, cfg config.Config, args []string) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := waitlist.New(pool)

	befehl := "list"
	if len(args) > 0 {
		befehl = args[0]
		args = args[1:]
	}

	switch befehl {
	case "list":
		codes, err := store.List(ctx)
		if err != nil {
			return err
		}
		if len(codes) == 0 {
			fmt.Println("no codes yet — create one with `covey waitlist new`")
			return nil
		}
		for _, c := range codes {
			zustand := "open"
			switch {
			case c.RevokedAt != nil:
				zustand = "revoked"
			case !c.Open():
				zustand = "used up/expired"
			}
			fmt.Printf("%s  %-20s %d/%d  %-16s %s\n",
				c.Hash[:12], c.Label, c.UsedCount, c.MaxUses, zustand,
				c.CreatedAt.Format("2006-01-02"))
		}
		return nil

	case "new":
		fs := flag.NewFlagSet("waitlist new", flag.ContinueOnError)
		label := fs.String("label", "", "note, e.g. the occasion the code is for")
		uses := fs.Int("uses", 1, "how often the code may be redeemed")
		days := fs.Int("days", 0, "validity in days (0 = unlimited)")
		email := fs.String("email", "", "restrict to an address or a domain (@firma.de)")
		if err := fs.Parse(args); err != nil {
			return err
		}
		opt := waitlist.Options{Label: *label, MaxUses: *uses, EmailPattern: *email}
		if *days > 0 {
			bis := time.Now().AddDate(0, 0, *days)
			opt.ExpiresAt = &bis
		}
		code, err := store.Create(ctx, opt)
		if err != nil {
			return err
		}
		fmt.Println(code)
		fmt.Fprintln(os.Stderr, "\nWrite it down — only its hash is stored, it cannot be shown again.")
		return nil

	case "revoke":
		if len(args) != 1 {
			return errors.New("usage: covey waitlist revoke <hash prefix>")
		}
		if err := store.Revoke(ctx, args[0]); err != nil {
			return err
		}
		fmt.Println("revoked")
		return nil
	}
	return fmt.Errorf("unknown: covey waitlist %s (list|new|revoke)", befehl)
}

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

	// The emergency reset works on the ACCOUNT: that is where the password
	// lives, and whoever is locked out is locked out as a person, not as the
	// occupant of one seat.
	var id uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM accounts WHERE email=$1", email).Scan(&id); err != nil {
		return fmt.Errorf("no account with e-mail %q", email)
	}
	hash, err := identbuiltin.HashPassword(pw)
	if err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "UPDATE accounts SET password_hash=$1 WHERE id=$2", hash, id); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, "DELETE FROM http_sessions WHERE account_id=$1", id); err != nil {
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

Supervisor: the org admin (human). Ask there when a task is unclear.`,
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

// openBlobStore builds the home store's backend — a port following the pattern
// of IdentityProvider and SecretStore (spec/10, "batteries included, but
// swappable"). The default is deliberately the directory: for an installation
// on one machine an object store is unnecessary operational surface, and the
// promise "one binary + Postgres" should not quietly become "one binary +
// Postgres + MinIO".
func openBlobStore(ctx context.Context, cfg config.Config, log *slog.Logger) (homestore.BlobStore, string, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.BlobStore)) {
	case "", "builtin":
		dir, err := homestore.NewDir(filepath.Join(cfg.DataDir, "blocks"))
		if err != nil {
			return nil, "", err
		}
		return dir, dir.Root(), nil
	case "s3":
		store, err := homestore.NewS3(cfg.S3Endpoint, cfg.S3Bucket, cfg.S3Prefix, homestore.Credentials{
			AccessKey: cfg.S3AccessKey, SecretKey: cfg.S3SecretKey, Region: cfg.S3Region,
		}, cfg.S3PathStyle)
		if err != nil {
			return nil, "", err
		}
		// Asked at startup: a wrong key or a missing bucket should say so here
		// and not at the first agent's falling asleep, where the message would
		// arrive inside the recording of a task that has long since run.
		checkCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		if err := store.Check(checkCtx); err != nil {
			// A warning and not an abort: everything that is not a run works
			// meanwhile, and an object store that comes back in two minutes is
			// a normal case.
			log.Warn("object store not usable — homes cannot be synced until it is", "err", err)
		}
		return store, cfg.S3Endpoint + "/" + cfg.S3Bucket, nil
	default:
		return nil, "", fmt.Errorf("COVEY_BLOB_STORE %q: only 'builtin' and 's3' are implemented", cfg.BlobStore)
	}
}

// rewriteLoopbackForContainer bends a loopback URL onto host.docker.internal so
// that a container reaches the service on the host — the control plane for the
// egress proxy, say. Non-loopback hosts (a real deployment address) stay
// untouched.
func rewriteLoopbackForContainer(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	switch u.Hostname() {
	case "localhost", "127.0.0.1", "::1":
	default:
		return raw
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
// sandbox's only way out). The allowlist comes from the control plane + ENV +
// code default and is reloaded periodically so UI changes take effect without
// a restart.
//
// It asks the control plane and no longer the database. On a remote runner the
// old construction would mean distributing the Postgres credentials to every
// host that runs sandboxes — the proxy is an enforcement point, not a database
// client (spec/16, "Trust boundary").
func runEgressProxy(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.ControlURL == "" || cfg.RunnerToken == "" {
		return fmt.Errorf("egress-proxy: COVEY_CONTROL_URL and COVEY_RUNNER_TOKEN are required — " +
			"the proxy fetches its allowlist from the control plane")
	}

	// No reachability check at startup: in network mode the container is
	// attached to the bridge network only AFTER it has started, so the control
	// plane is unreachable for the first moments. The resolver retries by
	// itself on every request, and until then it answers fail-closed — which is
	// the correct answer for a proxy whose allowlist it does not know.
	resolver := egress.NewAPIResolver(ctx, cfg.ControlURL, cfg.RunnerToken, egressBaseAllow(cfg), 15*time.Second, log)
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

	runnerStore := runnerstore.NewStore(pool)
	snapshotStore := runnerStore
	// The built-in runner: one per organisation, created by the platform
	// itself. At this stage it is only the identity the egress proxy
	// authenticates with; it becomes an execution node in stage 2 (spec/16).
	builtinRunners := runnerstore.NewBuiltinTokens(runnerStore)

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

	// Die veroeffentlichten Arbeitsplaetze (spec/16): welches Image zu welcher
	// Covey-Fassung gehoert, gepinnt auf den Digest. Mit demselben Cache wie
	// der Plugin-Katalog — der Stand ueberlebt den Neustart, und faellt der
	// Server dahinter aus, gilt der letzte gueltige weiter.
	workplaces := sandbox.NewSource(cfg.SandboxCatalogURL, marketplace.NewPgCache(pool), log)

	// Egress enforcement can only be enforced with real network isolation (docker).
	egressEnforced := cfg.EgressEnforce && cfg.SandboxProvider == "docker"

	if cfg.SandboxProvider != "docker" {
		return fmt.Errorf("sandbox provider %q: only 'docker' is implemented", cfg.SandboxProvider)
	}

	// The data plane runs through the runner protocol — including here, where
	// the runner sits in this very process (spec/16). That is not a detour: it
	// is what keeps the path to a foreign host from being a second
	// implementation that only whoever operates two machines ever exercises.
	runnerPool := runner.NewPool(log)
	runnerPool.DefaultImage = cfg.SandboxImage
	// The profiles from the catalogue (spec/16). An agent's value that names
	// none of them is taken as an image reference of its own.
	//
	// Three sources, in this order: what the environment names, what the
	// published catalogue holds for THIS Covey version (pinned by digest), and
	// the compiled default. The pool resolves that itself, because the middle
	// one can change while the process runs — a release is published and the
	// next wake takes the image built for it.
	runnerPool.Profiles = cfg.SandboxImages
	runnerPool.EnvImages = cfg.SandboxImageEnv
	runnerPool.Catalog = workplaces
	// Und die Arbeitsplätze, die eine Organisation selbst mitgebracht hat: Ein
	// Agent trägt auch dort nur einen Namen, und aufgelöst wird er hier.
	orgWorkplaces := orgworkplaces.New(pool)
	runnerPool.OrgImages = func(ctx context.Context, orgID uuid.UUID) map[string]string {
		m, err := orgWorkplaces.Images(ctx, orgID)
		if err != nil {
			log.Warn("eigene Arbeitsplätze nicht lesbar", "err", err)
			return nil
		}
		return m
	}
	runnerPool.AllOrgImages = func(ctx context.Context) map[string]string {
		m, err := orgworkplaces.AllImages(ctx, pool)
		if err != nil {
			log.Warn("eigene Arbeitsplätze nicht lesbar", "err", err)
			return nil
		}
		return m
	}
	runnerPool.AgentImages = registry.SandboxImagesInUse
	runnerPool.HomeExcludes = cfg.HomeExcludes
	// "Last seen" only means something if it moves while a runner is there.
	// Best effort: a missing timestamp is a display flaw, not a reason to do
	// anything about the connection it describes.
	runnerPool.Heard = func(runnerID uuid.UUID) {
		if err := runnerStore.Seen(context.WithoutCancel(ctx), runnerID); err != nil {
			log.Debug("runner heartbeat not recorded", "runner", runnerID, "err", err)
		}
	}

	// The home store: after every job the home goes into it as a whole and is
	// materialised from it on wake (spec/16). It makes the home replaceable —
	// and only then is a runner switch not data loss. It pays off before a
	// second host exists: two developer agents on one machine hold the same
	// 4 GB of toolchain twice today, and a deleted home is unrecoverable.
	//
	// It requires backup like the database. 99 % of it would only have to be
	// downloaded again, but the rest exists nowhere else — it is a cache in its
	// function, not in its need for protection.
	// What the file browser needs about an agent: whose organisation it is,
	// where its home last lay, and which snapshot it was last synced to. The
	// last two are what make a home readable when its runner is not connected.
	runnerPool.HomeInfo = func(ctx context.Context, agentID uuid.UUID) (uuid.UUID, uuid.UUID, string, error) {
		agent, err := registry.Get(ctx, agentID)
		if err != nil {
			return uuid.Nil, uuid.Nil, "", err
		}
		snap, err := runnerStore.LatestSnapshot(ctx, agentID)
		if err != nil {
			// Not fatal: without a snapshot the connected runner is still the
			// answer, and that is the normal case anyway.
			log.Warn("last snapshot not readable", "agent", agentID, "err", err)
		}
		last := uuid.Nil
		if snap.RunnerID != nil {
			last = *snap.RunnerID
		}
		return agent.OrgID, last, snap.ManifestHash, nil
	}

	var blobs homestore.BlobStore
	if cfg.HomeStore {
		store, where, err := openBlobStore(ctx, cfg, log)
		if err != nil {
			return err
		}
		blobs = store
		runnerPool.Blobs = store
		runnerPool.LatestSnapshot = func(ctx context.Context, agentID uuid.UUID) (string, error) {
			snap, err := snapshotStore.LatestSnapshot(ctx, agentID)
			return snap.ManifestHash, err
		}
		runnerPool.SnapshotTaken = func(ctx context.Context, agentID, runnerID uuid.UUID, res runner.HomeSynced) error {
			agent, err := registry.Get(ctx, agentID)
			if err != nil {
				return err
			}
			_, err = snapshotStore.RecordSnapshotTimed(ctx, agent.OrgID, agentID, &runnerID,
				res.ManifestHash, res.TotalSize, res.Blocks, res.BytesUp, res.DurationMS, res.Reason)
			return err
		}
		log.Info("home store active", "blocks", where,
			"note", "needs backup like the database — 48 MB of a typical home exist nowhere else")
	} else {
		log.Warn("home store switched off (COVEY_HOME_STORE=false) — " +
			"a lost home is unrecoverable, and an agent cannot move to another runner")
	}

	var proxyURL string
	if egressEnforced && cfg.EgressIsolation != "network" {
		// Cooperative: proxy inside the control plane process, container via HTTP_PROXY.
		url, closeProxy, err := startEgressProxy(ctx, cfg, egressStore, log)
		if err != nil {
			return err
		}
		defer closeProxy()
		proxyURL = url
	}

	// One built-in runner per organisation, created on first use — an
	// organisation that comes into being while the process runs gets its own
	// too. Each carries its own egress segment: a runner serves exactly one
	// tenant, and `--internal` cuts the way out, not the way sideways.
	runnerPool.EnsureLocal = func(ctx context.Context, orgID uuid.UUID) error {
		// An organisation has the built-in runner exactly as long as it has no
		// registered one. Whoever adds a runner has said that compute leaves
		// this machine; a control plane that kept quietly running sandboxes on
		// the side has not been told that (spec/16).
		//
		// Counted are REGISTERED runners, not connected ones: a maintenance
		// window on the only runner must not silently move the whole workforce
		// back onto the control plane's host.
		if remote, err := runnerStore.HasRemote(ctx, orgID); err != nil {
			return err
		} else if remote {
			return fmt.Errorf("organisation %s has its own runner — the built-in one has stood down", orgID)
		}
		runnerID, runnerToken, err := builtinRunners.For(ctx, orgID)
		if err != nil {
			return err
		}
		docker := &runner.Docker{
			RunnerID: runnerID,
			Image:    cfg.SandboxImage,
			DataDir:  cfg.DataDir,
		}
		if egressEnforced {
			switch cfg.EgressIsolation {
			case "network":
				// Hard isolation: sandbox without internet, the proxy container
				// as its only way out. The proxy fetches the allowlist from the
				// control plane and no longer from Postgres — it is an
				// enforcement point, not a database client.
				docker.EgressIsolation = "network"
				docker.EgressProxyImage = "covey-egress:latest"
				docker.EgressRunnerToken = runnerToken
				docker.EgressProxyEnv = map[string]string{
					"COVEY_CONTROL_URL":       rewriteLoopbackForContainer(cfg.PublicURL),
					"COVEY_EGRESS_ALLOW":      strings.Join(append(append([]string{}, cfg.EgressAllow...), "host.docker.internal"), ","),
					"COVEY_EGRESS_PROXY_ADDR": ":8888",
				}
			default:
				docker.EgressProxyURL = proxyURL
			}
		}
		node := runner.NewNode(runnerID, orgID, docker, log)
		node.Blobs = blobs
		return runnerPool.AttachLocal(ctx, node)
	}

	if egressEnforced && cfg.EgressIsolation == "network" {
		log.Info("egress enforcement: hard network isolation active", "proxy-image", "covey-egress:latest")
	}

	// Clear away the egress objects of earlier versions: the segment carries
	// the runner's identity now, so the instance-wide ones are left behind at
	// an upgrade. In the binary rather than in an upgrade guide — a step that
	// has to be read and typed is one that is skipped.
	runner.PruneLegacyEgress(ctx, "", log)

	// Bring the existing organisations' runners up at startup rather than at
	// the first wake: the self-check below is meant to say what is in the way
	// BEFORE an agent runs into it, and it can only ask a runner that is there.
	orgs, err := org.NewStore(pool).ListOrgs(ctx)
	if err != nil {
		return err
	}
	for _, o := range orgs {
		if remote, err := runnerStore.HasRemote(ctx, o.ID); err == nil && remote {
			log.Info("organisation has its own runner — the built-in one stays down", "org", o.ID)
			continue
		}
		if err := runnerPool.EnsureLocal(ctx, o.ID); err != nil {
			// Not an abort: an organisation whose runner does not come up is a
			// problem for its agents, not for the instance. The self-check
			// below says so, and everything that is not a run keeps working.
			log.Warn("built-in runner did not come up", "org", o.ID, "err", err)
		}
	}
	provider := orchestrator.SandboxProvider(runnerPool)

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
	// A sandbox that dies is reported by the runner instead of being inferred
	// from a ReadyTimeout minutes later — that is what watching the container
	// buys (spec/16).
	runnerPool.SandboxDied = orch.SandboxDied

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
		Marketplace: func() *marketplace.Client {
			m := marketplace.New(cfg.MarketplaceURL)
			// Der Katalog ueberlebt damit den Neustart: die erste Store-Seite
			// nach dem Start wartet nicht auf einen fremden Server, und faellt
			// der gerade aus, zeigt sie den letzten gueltigen Stand statt nichts.
			m.Store, m.Log = marketplace.NewPgCache(pool), log
			return m
		}(),
		Workplaces: workplaces, OrgWorkplaces: orgWorkplaces,
		Settings: settings.New(pool), Accounts: accounts.New(pool), Waitlist: waitlist.New(pool),
		Skills: skillStore,
		Orch:   orch, WebFS: dist, Log: log,
		WebhookSecrets: cfg.WebhookSecrets,
		SessionTTL:     cfg.SessionTTL,
		SiteURL:        cfg.SiteURL,
		CookieSecure:   cfg.CookieSecure,
		TrustedProxies: cfg.TrustedProxies,
		EgressStore:    egressStore,
		EgressEnforced: egressEnforced,
		EgressDefaults: egressBaseAllow(cfg),
		Config:         &cfg,
		Runners:        runnerStore,
		RunnerPool:     runnerPool,
		Blobs:          blobs,
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
		go dreams.RunNightly(ctx, at, func(ctx context.Context, agentID uuid.UUID) (llm.Provider, bool) {
			a, err := registry.Get(ctx, agentID)
			if err != nil {
				return nil, false
			}
			p, err := llm.Resolve(ctx, secretStore, a.OrgID)
			return p, err == nil
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
	// Recording retention: the verbatim runs whose time is up (spec/06). Only
	// those — what an action, an approval or a credential request recorded stays,
	// because that is the audit trail and the basis of every indicator.
	//
	// On the same six hours as the neighbours above. A retention measured in
	// days does not need a finer interval; what it needs is to run at all,
	// without anybody remembering it.
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				n, err := obs.CleanupRecordings(ctx)
				if err != nil {
					log.Warn("recording retention failed", "err", err)
					continue
				}
				if n > 0 {
					log.Info("expired recordings removed", "deleted", n)
				}
			}
		}
	}()
	// Home store retention. Without this the rules on the organisation are a
	// setting that nothing enforces: they apply when an admin presses the
	// button in Administration → Runners, and on an installation where nobody
	// does, the store grows until a deploy dies on a full disk — which is
	// exactly what spec/16 asks to be spared ("a warning before the disk runs
	// short, not after"). Six hours, like the egress log above: often enough
	// that nothing piles up, rare enough that a sweep rarely meets a sync.
	//
	// Whoever needs the space now does not wait for the tick:
	// `covey home-store cleanup --apply` runs the same pass.
	if blobs != nil {
		go func() {
			t := time.NewTicker(6 * time.Hour)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					orgs, err := runnerStore.OrgIDs(ctx)
					if err != nil {
						log.Warn("home store cleanup: organisations unreadable", "err", err)
						continue
					}
					for _, id := range orgs {
						res, err := runnerStore.CleanupOrg(ctx, blobs, id, false)
						if err != nil {
							// One organisation's store being unreadable must not
							// stop the others: the next one may be the one that
							// is actually filling the disk.
							log.Warn("home store cleanup failed", "org", id, "err", err)
							continue
						}
						if res.BlocksRemoved > 0 {
							log.Info("home store cleaned up", "org", id,
								"blocks", res.BlocksRemoved, "bytes", res.FreedBytes)
						}
					}
				}
			}
		}()
	}
	go func() {
		<-ctx.Done()
		// Graceful shutdown: finish running requests, close the daemons.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
		// Whatever the file browser has changed and not yet synced goes in now.
		// A settling period that is still running would otherwise fire into a
		// connection that is already gone, and the change would live only in the
		// working copy — which is exactly the case this is meant to prevent.
		runnerPool.FlushHomes(shutdownCtx)
	}()

	log.Info("covey serve", "addr", cfg.ListenAddr, "public", cfg.PublicURL, "build", buildinfo.String())
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
