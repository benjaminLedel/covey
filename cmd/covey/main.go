// covey ist das eine Binary der Control Plane (spec/10): API/BFF +
// Orchestration-Core + eingebettetes Frontend + eingebettete Migrationen.
// Subcommands: serve, migrate (up|down), bootstrap, passwd.
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
	"covey/internal/backlog"
	"covey/internal/config"
	"covey/internal/db"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/httpapi"
	identbuiltin "covey/internal/identity/builtin"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/orchestrator"
	"covey/internal/org"
	secbuiltin "covey/internal/secrets/builtin"
	targetstore "covey/internal/target/store"
	"covey/internal/templates"
	"covey/migrations"
	"covey/web"

	// Kompilierte Zielsystem-Plugins: Blank-Import = ausgeliefert. Wer Covey
	// ohne ein System bauen will, entfernt dessen Zeile — der Rest bleibt gleich.
	_ "covey/internal/target/dev"
	_ "covey/internal/target/email"
	_ "covey/internal/target/gitlab"
	_ "covey/internal/target/nextcloud"
	_ "covey/internal/target/sharepoint"
	_ "covey/internal/target/teams"
	_ "covey/internal/target/zammad"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
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
	fmt.Fprintln(os.Stderr, `covey — Control Plane für KI-Agenten

  covey migrate up|down   Migrationen anwenden / eine zurückrollen
  covey bootstrap         Organisation + Admin + Demo-Agent anlegen (idempotent)
  covey passwd <email>    Passwort eines Nutzers neu setzen (Notfall-Reset)
  covey serve             API + Orchestrator + Admin-UI starten
  covey egress-proxy      Egress-Allowlist-Proxy (network-Isolationsmodus, im Container)
  covey genkey            neuen COVEY_MASTER_KEY erzeugen

Konfiguration über ENV: COVEY_DATABASE_URL, COVEY_LISTEN_ADDR, COVEY_PUBLIC_URL,
COVEY_MASTER_KEY, COVEY_COVEYD_PATH, COVEY_DATA_DIR, COVEY_ZAMMAD_WEBHOOK_SECRET,
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
		log.Info("migrationen angewendet", "count", n)
		return nil
	case "down":
		return db.MigrateDown(ctx, pool, migrations.FS)
	default:
		return fmt.Errorf("unbekannte richtung %q (up|down)", dir)
	}
}

// runBootstrap legt idempotent Organisation, Admin-Login und den Demo-
// Support-Agenten samt Config-as-Code und Standard-Guard-Rails an.
func runBootstrap(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		return err
	}

	// Organisation.
	var orgID uuid.UUID
	err = pool.QueryRow(ctx, "SELECT id FROM organizations LIMIT 1").Scan(&orgID)
	if err != nil {
		orgID = uuid.New()
		if _, err := pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,$2)",
			orgID, "Demo-Organisation"); err != nil {
			return err
		}
		// Basis-Allowlist seeden (konfigurierbar über die Egress-UI).
		if _, err := pool.Exec(ctx, `INSERT INTO egress_default_hosts (org_id, pattern, note)
			VALUES ($1, 'api.anthropic.com', 'LLM-Endpunkt der Claude-Runtime')
			ON CONFLICT DO NOTHING`, orgID); err != nil {
			return err
		}
		log.Info("organisation angelegt", "id", orgID)
	}

	// Admin-Mensch.
	adminEmail := strings.ToLower(getenvDefault("COVEY_ADMIN_EMAIL", "admin@covey.local"))
	adminPass := os.Getenv("COVEY_ADMIN_PASSWORD")
	if adminPass == "" {
		adminPass = "covey-admin"
		log.Warn("bootstrap: COVEY_ADMIN_PASSWORD nicht gesetzt — verwende das bekannte Default-Passwort 'covey-admin'. Vor jeder nicht-lokalen Nutzung ändern (COVEY_ADMIN_PASSWORD setzen oder Passwort in der UI ändern).")
	}
	var adminID uuid.UUID
	err = pool.QueryRow(ctx, "SELECT id FROM humans WHERE email=$1", adminEmail).Scan(&adminID)
	if err != nil {
		// Kein Human mit der Bootstrap-E-Mail. Existiert bereits ein
		// Platform-Admin (umbenannt oder unter anderer Adresse angelegt),
		// wird KEIN neuer angelegt — sonst würde jeder Deploy einen in der
		// UI gelöschten oder geänderten Admin wiederbeleben. Neu angelegt
		// wird nur, wenn die Organisation gar keinen Platform-Admin hat
		// (frische Installation oder Lockout-Recovery).
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
			log.Info("admin angelegt", "email", adminEmail)
		}
	}

	// Demo-Support-Agent inkl. SOUL.md & Co.
	registry := agents.NewRegistry(pool)
	agent, err := registry.GetBySlug(ctx, orgID, "support")
	if errors.Is(err, agents.ErrNotFound) {
		agent, err = registry.Create(ctx, orgID, "support", "Support-Agent", "claude-code", &adminID)
		if err != nil {
			return err
		}
		if _, err := registry.SaveConfig(ctx, agent.ID, defaultSupportConfig(), &adminID); err != nil {
			return err
		}
		log.Info("support-agent angelegt", "id", agent.ID)
	} else if err != nil {
		return err
	}
	// Default-Board sicherstellen (idempotent) — auch für einen bereits
	// existierenden Demo-Agenten aus einer früheren Bootstrap-Runde.
	if err := backlog.NewStore(pool).SeedDefaultStages(ctx, agent.ID); err != nil {
		return err
	}

	// Standard-Guard-Rails (fail-closed-Basissatz, spec/06).
	rails := guardrails.NewStore(pool)
	existing, err := rails.List(ctx, orgID)
	if err != nil {
		return err
	}
	if len(existing) == 0 {
		defaults := []guardrails.Rule{
			{OrgID: orgID, ScopeLevel: "global", RuleType: guardrails.RuleRequireApproval,
				Pattern: "zammad:reply_external"}, // externe Antwort nur mit Freigabe
			{OrgID: orgID, ScopeLevel: "global", RuleType: guardrails.RuleDenySystem,
				Pattern: "hr*"}, // Personalsysteme sind für Agenten tabu
			{OrgID: orgID, ScopeLevel: "global", RuleType: guardrails.RuleDenyAction,
				Pattern: "*:delete*"}, // destruktive Aktionen hart verboten
		}
		for _, r := range defaults {
			if _, err := rails.Create(ctx, r); err != nil {
				return err
			}
		}
		log.Info("standard-guard-rails angelegt", "count", len(defaults))
	}
	log.Info("bootstrap fertig", "login", adminEmail)
	return nil
}

// runPasswd setzt das Passwort eines Nutzers neu — der Notfall-Weg, wenn auch
// der Platform-Admin ausgesperrt ist (der Builtin-Provider hat bewusst keinen
// Self-Service-Reset). Das Passwort kommt nie aus argv (Prozessliste!),
// sondern ohne Echo vom Terminal oder als Zeile von stdin. Alle laufenden
// Sessions des Nutzers werden invalidiert.
func runPasswd(ctx context.Context, cfg config.Config, args []string, log *slog.Logger) error {
	if len(args) != 1 {
		return errors.New("aufruf: covey passwd <email>")
	}
	email := strings.ToLower(strings.TrimSpace(args[0]))

	pw, err := readNewPassword()
	if err != nil {
		return err
	}
	if len(pw) < 8 {
		return errors.New("passwort braucht mindestens 8 Zeichen")
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	var id uuid.UUID
	if err := pool.QueryRow(ctx, "SELECT id FROM humans WHERE email=$1", email).Scan(&id); err != nil {
		return fmt.Errorf("kein Nutzer mit E-Mail %q", email)
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
	log.Info("passwort neu gesetzt, alle sessions invalidiert", "email", email)
	return nil
}

// readNewPassword liest das neue Passwort: interaktiv zweimal ohne Echo,
// nicht-interaktiv (Pipe) als einzelne Zeile von stdin.
func readNewPassword() (string, error) {
	fd := int(os.Stdin.Fd())
	if term.IsTerminal(fd) {
		fmt.Fprint(os.Stderr, "Neues Passwort: ")
		first, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		fmt.Fprint(os.Stderr, "Wiederholen: ")
		second, err := term.ReadPassword(fd)
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", err
		}
		if string(first) != string(second) {
			return "", errors.New("passwörter stimmen nicht überein")
		}
		return string(first), nil
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("passwort von stdin lesen: %w", err)
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func defaultSupportConfig() map[string]string {
	return map[string]string{
		"SOUL.md": `# Support-Agent

## Rolle
First-Level-Support für Kundenanfragen im Ticketsystem.

## Auftrag
Tickets triagieren, lösbare Fälle selbst beantworten,
komplexe Fälle an den zuständigen Menschen eskalieren.

## Ton
Freundlich, knapp, lösungsorientiert. Deutsch, Sie-Form.

## Grenzen
- Keine Zusagen zu Preisen oder Verträgen.
- Bei rechtlichen Fragen immer eskalieren.
- Keine Aktionen ohne Freigabe, die Kundendaten löschen.`,
		"CAPABILITIES.md": `# Fähigkeiten

- Zuständig: eingehende Support-Tickets (Zammad).
- Nicht zuständig: Vertrieb, Rechtsfragen, Personalthemen — immer eskalieren.`,
		"PLAYBOOKS.md": `# Playbooks

## Ticket-Triage
1. Ticket und Verlauf über den Action-Proxy lesen (get_ticket, list_articles).
2. Gedächtnis-Kontext beachten: gab es den Fall schon?
3. Lösbar → Antwort als interne Notiz entwerfen, dann extern antworten.
4. Fehlt Information vom Kunden → Rückfrage stellen (reply, internal=false),
   Ticket auf "pending reminder" setzen und mit Status blocked parken
   (correlation_key: zammad:ticket:<id>).
5. Nicht lösbar → escalate mit Begründung.`,
		"ACCESS.md": `# Zugänge

- system: zammad scope: read,write,comment`,
		"ORG.md": `# Organisation

Vorgesetzter: Team-Lead Support (Mensch). Eskalation immer dorthin.`,
	}
}

func getenvDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// egressBaseAllow sind die Infrastruktur-Zusätze aus COVEY_EGRESS_ALLOW (ENV) —
// nur per Config änderbar, im UI als „ENV" ausgewiesen (z. B.
// host.docker.internal für den Proxy-Container). Alles Fachliche — auch der
// LLM-Endpunkt — liegt konfigurierbar in der DB: Basis-Allowlist der Org
// (egress_default_hosts, geseedet mit api.anthropic.com), Templates und
// agent-eigene Hosts.
func egressBaseAllow(cfg config.Config) []string {
	return append([]string{}, cfg.EgressAllow...)
}

// rewriteDBForContainer biegt eine Loopback-DB-URL auf host.docker.internal um,
// damit der Egress-Proxy-Container die Postgres-Instanz auf dem Host erreicht.
// Nicht-Loopback-Hosts (echtes DB-Deployment) bleiben unangetastet.
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

// runEgressProxy fährt den Egress-Allowlist-Proxy als eigenständigen Prozess
// (network-Isolationsmodus: läuft im Proxy-Container als einziger Ausgang der
// isolierten Sandbox). Die Allowlist kommt aus DB + ENV + Code-Default und wird
// periodisch neu geladen, damit UI-Änderungen ohne Neustart greifen.
func runEgressProxy(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	// DB kann beim Start noch nicht erreichbar sein: im network-Modus wird das
	// interne Netz erst NACH dem Container-Start ans Bridge-Netz gehängt. Bis
	// dahin retryen — der Prozess bleibt am Leben (Container „running"), damit
	// `docker network connect` greift.
	var store *egress.Store
	for store == nil {
		pool, err := db.Connect(ctx, cfg.DatabaseURL)
		if err != nil {
			log.Warn("egress-proxy: DB noch nicht erreichbar, retry", "err", err)
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
	log.Info("covey egress-proxy", "addr", addr, "basis", egressBaseAllow(cfg))
	<-ctx.Done()
	return nil
}

// startEgressProxy bindet den kooperativen Proxy (im Control-Plane-Prozess) auf
// einem freien Port (alle Interfaces, damit der Container ihn via
// host.docker.internal erreicht). Rückgabe: container-seitige Basis-Proxy-URL
// (ohne Credentials — der Provider hängt pro Agent das per-Sandbox-Token an),
// plus Close-Funktion.
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
	log.Info("egress-enforcement aktiv (kooperativ)", "proxy", containerURL, "basis", egressBaseAllow(cfg))
	return containerURL, func() { _ = proxy.Close() }, nil
}

func runServe(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	if cfg.MasterKeyHex == "" {
		return fmt.Errorf("COVEY_MASTER_KEY fehlt (mit `covey genkey` erzeugen)")
	}
	// Härtungs-Hinweise für nicht-lokale Deployments sichtbar machen.
	for _, warn := range cfg.SecurityWarnings() {
		log.Warn("sicherheit: " + warn)
	}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	// Auto-Migrate beim Start — der advisory lock serialisiert HA-Instanzen.
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
	// Plattformweiter Wiki-Aufräum-Heartbeat (COVEY_WIKI_CLEANUP): die Control Plane
	// legt jedem Agenten periodisch einen Backlog-Task an, in dem er sein Wiki pflegt
	// (Duplikate mergen, tote Links fixen — spec/05). Pro Agent per HEARTBEAT.md
	// überschreibbar; leerer Zeitplan = aus.
	if hb, enabled, err := agents.WikiCleanupHeartbeat(cfg.WikiCleanup); err != nil {
		return err
	} else if enabled {
		registry.SystemHeartbeats = append(registry.SystemHeartbeats, hb)
		sched := hb.DailyAt
		if sched == "" {
			sched = hb.Every.String()
		}
		log.Info("wiki-aufräum-heartbeat aktiv", "zeitplan", sched)
	}
	// Auch bei abgeschaltetem Feature aufrufen — räumt verwaiste System-Heartbeats weg.
	if err := registry.ReconcileSystemHeartbeats(ctx); err != nil {
		return err
	}
	backlogStore := backlog.NewStore(pool)
	obs := observability.NewStore(pool)
	rails := guardrails.NewStore(pool)
	mem := memory.NewStore(pool, memory.HashEmbedder{})
	targets := targetstore.NewStore(pool)
	egressStore := egress.NewStore(pool)
	templateStore := templates.NewStore(pool)

	// Egress-Enforcement ist nur mit echter Netz-Isolation (docker) durchsetzbar.
	egressEnforced := cfg.EgressEnforce && cfg.SandboxProvider == "docker"

	var provider orchestrator.SandboxProvider
	switch cfg.SandboxProvider {
	case "local":
		if cfg.EgressEnforce {
			log.Warn("COVEY_EGRESS_ENFORCE ignoriert: der local-Provider teilt das Host-Netz, Egress ist nicht durchsetzbar — docker-Provider nutzen")
		}
		provider = &orchestrator.LocalProvider{CoveydPath: cfg.CoveydPath, DataDir: cfg.DataDir}
	case "docker":
		dp := &orchestrator.DockerProvider{Image: cfg.SandboxImage, DataDir: cfg.DataDir}
		if egressEnforced {
			switch cfg.EgressIsolation {
			case "network":
				// Harte Isolation: Sandbox ohne Internet, Proxy-Container als
				// einziger Ausgang. Der Proxy liest die Allowlist selbst aus der DB
				// (Live-Reload per Poll) — deshalb kein host-seitiger Reload nötig.
				dp.EgressIsolation = "network"
				dp.EgressProxyImage = "covey-egress:latest"
				dp.EgressProxyEnv = map[string]string{
					"COVEY_DATABASE_URL":      rewriteDBForContainer(cfg.DatabaseURL),
					"COVEY_EGRESS_ALLOW":      strings.Join(append(append([]string{}, cfg.EgressAllow...), "host.docker.internal"), ","),
					"COVEY_EGRESS_PROXY_ADDR": ":8888",
				}
				log.Info("egress-enforcement: harte netz-isolation aktiv", "proxy-image", dp.EgressProxyImage)
			default:
				// Kooperativ: Proxy im Control-Plane-Prozess, Container via HTTP_PROXY.
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
		return fmt.Errorf("sandbox provider %q: implementiert sind 'local' und 'docker'", cfg.SandboxProvider)
	}

	wsURL := strings.Replace(cfg.PublicURL, "http", "ws", 1) + "/api/daemon/ws"
	orch := orchestrator.New(orchestrator.Options{
		Pool: pool, Registry: registry, Backlog: backlogStore, Obs: obs,
		Rails: rails, Secrets: secretStore, Identity: idp, Memory: mem,
		Targets:        targets,
		Egress:         egressStore,
		Provider:       provider,
		PublicWSURL:    wsURL,
		DaemonTokenTTL: cfg.DaemonTokenTTL,
		TickInterval:   cfg.TickInterval,
		Log:            log,
	})

	dist, err := web.Dist()
	if err != nil {
		return err
	}
	srv := &httpapi.Server{
		Pool: pool, Registry: registry, Backlog: backlogStore, Obs: obs,
		Rails: rails, Secrets: secretStore, Identity: idp, Memory: mem,
		Org: org.NewStore(pool), Targets: targets, Templates: templateStore,
		Orch: orch, WebFS: dist, Log: log,
		WebhookSecrets: cfg.WebhookSecrets,
		SessionTTL:     cfg.SessionTTL,
		PublicURL:      cfg.PublicURL,
		CookieSecure:   cfg.CookieSecure,
		EgressStore:    egressStore,
		EgressEnforced: egressEnforced,
		EgressDefaults: egressBaseAllow(cfg),
	}

	httpServer := &http.Server{Addr: cfg.ListenAddr, Handler: srv.Handler()}

	go func() {
		if err := orch.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("orchestrator", "err", err)
		}
	}()
	// Egress-Log-Retention: alte Entscheidungen periodisch wegräumen.
	go func() {
		t := time.NewTicker(6 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if n, err := egressStore.CleanupLog(ctx, 30*24*time.Hour); err == nil && n > 0 {
					log.Info("egress-log aufgeräumt", "gelöscht", n)
				}
			}
		}
	}()
	go func() {
		<-ctx.Done()
		// Graceful Shutdown: laufende Requests zu Ende, Daemons schließen.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpServer.Shutdown(shutdownCtx)
	}()

	log.Info("covey serve", "addr", cfg.ListenAddr, "public", cfg.PublicURL)
	if err := httpServer.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
