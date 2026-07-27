// coveyd ist der schlanke Sandbox-Daemon (spec/01): er spricht das
// Daemon-Protokoll zur Control Plane und bootstrappt die Runtime.
// Konfiguration ausschließlich über ENV — gesetzt vom SandboxProvider.
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"covey/internal/daemon"
	"covey/internal/target"

	// Kompilierte Zielsystem-Plugins: Blank-Import = ausgeliefert (analog
	// cmd/covey). Manifest-Plugins kommen zur Laufzeit über das Protokoll.
	_ "covey/internal/target/browser"
	_ "covey/internal/target/dev"
	_ "covey/internal/target/email"
	_ "covey/internal/target/gitlab"
	_ "covey/internal/target/sharepoint"
	_ "covey/internal/target/teams"
	_ "covey/internal/target/zammad"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	wsURL := os.Getenv("COVEY_WS_URL")
	token := os.Getenv("COVEY_DAEMON_TOKEN")
	agentID := os.Getenv("COVEY_AGENT_ID")
	homeDir := os.Getenv("COVEY_HOME")
	if wsURL == "" || token == "" || agentID == "" {
		log.Error("COVEY_WS_URL, COVEY_DAEMON_TOKEN und COVEY_AGENT_ID sind Pflicht")
		os.Exit(2)
	}
	if homeDir == "" {
		homeDir, _ = os.Getwd()
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		log.Error("home anlegen", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	client := daemon.NewClient(wsURL, token, agentID, homeDir, log)
	err := client.Run(ctx)
	// Lokalen Plugin-Zustand abräumen (z. B. Prozesse des dev-Supervisors) —
	// sonst überlebten gestartete Dev-Server/Browser die Sandbox.
	target.Shutdown()
	switch {
	case err == nil:
		log.Info("sleep — daemon fährt herunter")
	case errors.Is(err, daemon.ErrKilled):
		log.Warn("kill-switch — daemon beendet sich sofort")
		os.Exit(3)
	default:
		log.Error("daemon beendet", "err", err)
		os.Exit(1)
	}
}
