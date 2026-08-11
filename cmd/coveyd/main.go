// coveyd is the slim sandbox daemon (spec/01): it speaks the daemon protocol
// towards the control plane and bootstraps the runtime.
// Configuration exclusively via ENV — set by the SandboxProvider.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"covey/internal/buildinfo"
	"covey/internal/daemon"
	"covey/internal/target"

	// Compiled-in target system plugins: blank import = shipped (analogous to
	// cmd/covey). Manifest plugins arrive at runtime over the protocol.
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
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))

	// A sandbox image can go stale — `coveyd version` answers which build
	// actually sits in the container.
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Println("coveyd " + buildinfo.String())
			// The same note as with covey: one work, one licence.
			fmt.Println("AGPL-3.0 · source: " + buildinfo.SourceURL)
			return
		}
	}

	wsURL := os.Getenv("COVEY_WS_URL")
	token := os.Getenv("COVEY_DAEMON_TOKEN")
	agentID := os.Getenv("COVEY_AGENT_ID")
	homeDir := os.Getenv("COVEY_HOME")
	if wsURL == "" || token == "" || agentID == "" {
		log.Error("COVEY_WS_URL, COVEY_DAEMON_TOKEN and COVEY_AGENT_ID are mandatory")
		os.Exit(2)
	}
	if homeDir == "" {
		homeDir, _ = os.Getwd()
	}
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		log.Error("create home", "err", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Info("coveyd start", "agent", agentID, "build", buildinfo.String())
	client := daemon.NewClient(wsURL, token, agentID, homeDir, log)
	err := client.Run(ctx)
	// Clear out local plugin state (e.g. the dev supervisor's processes) —
	// otherwise started dev servers/browsers would outlive the sandbox.
	target.Shutdown()
	switch {
	case err == nil:
		log.Info("sleep — daemon is shutting down")
	case errors.Is(err, daemon.ErrKilled):
		log.Warn("kill switch — daemon terminates immediately")
		os.Exit(3)
	default:
		log.Error("daemon terminated", "err", err)
		os.Exit(1)
	}
}
