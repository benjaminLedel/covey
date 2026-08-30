package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// A runner that only runs while somebody's shell is open is not a runner. It
// answers for five minutes, the SSH session ends, and the host shows as
// "offline" in the runner view — the state in which agents wait rather than
// fail, which is the expensive kind of broken.
//
// So the service belongs in the binary, not in a documentation snippet somebody
// copies. Third parties install Covey from GitHub and have exactly the means
// this binary brings; a paragraph in a runbook is not one of them.
const (
	serviceUnitName = "covey-runner.service"
	serviceUnitDir  = "/etc/systemd/system"
)

// unitParams is what the unit text is built from — everything that differs
// between hosts, and nothing else.
type unitParams struct {
	Binary string // absolute path to this binary
	Config string // the configuration register wrote
	User   string // empty or "root" → run as root, which is what Docker wants
}

// unitFile renders the systemd unit. Kept a pure function so it can be printed
// on a host without systemd — a runner on a machine with a different init
// system is a person who needs the text, not an error message.
func unitFile(p unitParams) string {
	var b strings.Builder
	b.WriteString(`[Unit]
Description=Covey runner
Documentation=https://github.com/benjaminLedel/covey/blob/main/docs/en/operations/runner.md
After=network-online.target docker.service
Wants=network-online.target

[Service]
`)
	fmt.Fprintf(&b, "ExecStart=%s run --config %s\n", p.Binary, p.Config)
	if p.User != "" && p.User != "root" {
		// A user of its own still needs the Docker socket, and membership in
		// that group is practically the same power — with a name in front of
		// it, which is the point.
		fmt.Fprintf(&b, "User=%s\nSupplementaryGroups=docker\n", p.User)
	}
	b.WriteString(`Restart=always
RestartSec=5
# The runner holds sandboxes; a stop that does not wait tears them down
# mid-write.
TimeoutStopSec=60

[Install]
WantedBy=multi-user.target
`)
	return b.String()
}

// systemdPresent is the canonical test: systemd creates this directory, and
// nothing else does.
func systemdPresent() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	st, err := os.Stat("/run/systemd/system")
	return err == nil && st.IsDir()
}

func runInstallService(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("install-service", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "the configuration written by register")
	user := fs.String("user", "root", "the user the service runs as (a non-root user is added to the docker group)")
	noStart := fs.Bool("no-start", false, "install and enable the service, but do not start it now")
	print := fs.Bool("print", false, "only print the unit, change nothing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	binary, err := os.Executable()
	if err != nil {
		return fmt.Errorf("finding this binary: %w", err)
	}
	if resolved, err := filepath.EvalSymlinks(binary); err == nil {
		binary = resolved
	}
	unit := unitFile(unitParams{Binary: binary, Config: *configPath, User: *user})

	if *print {
		fmt.Print(unit)
		return nil
	}
	if !systemdPresent() {
		// Not an error: the host runs something else, and the person in front
		// of it needs the text rather than a refusal.
		fmt.Fprintf(os.Stderr, "No systemd on this host — here is the unit, adapt it for your init system:\n\n")
		fmt.Print(unit)
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("writing %s needs root — run it with sudo, or take the text from `covey-runner install-service --print`", filepath.Join(serviceUnitDir, serviceUnitName))
	}
	if _, err := os.Stat(*configPath); err != nil {
		return fmt.Errorf("%s is not there — run `covey-runner register` first, the service starts a runner without a token otherwise", *configPath)
	}

	path := filepath.Join(serviceUnitDir, serviceUnitName)
	if err := os.WriteFile(path, []byte(unit), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	action := []string{"enable", "--now", serviceUnitName}
	if *noStart {
		action = []string{"enable", serviceUnitName}
	}
	if err := systemctl(ctx, action...); err != nil {
		return err
	}
	log.Info("service installed", "unit", path, "user", *user, "started", !*noStart)
	if *noStart {
		fmt.Printf("Installed %s (enabled, not started).\nStart it with: systemctl start %s\n", path, serviceUnitName)
		return nil
	}
	fmt.Printf("Installed and started %s.\n"+
		"  systemctl status %s\n"+
		"  journalctl -u %s -f\n", path, serviceUnitName, serviceUnitName)
	return nil
}

func runRemoveService(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("remove-service", flag.ExitOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	path := filepath.Join(serviceUnitDir, serviceUnitName)
	if _, err := os.Stat(path); err != nil {
		fmt.Printf("No %s here — nothing to remove.\n", path)
		return nil
	}
	if os.Geteuid() != 0 {
		return fmt.Errorf("removing %s needs root — run it with sudo", path)
	}
	// disable --now before the file goes: the reverse order leaves a running
	// service whose unit systemd can no longer name.
	if err := systemctl(ctx, "disable", "--now", serviceUnitName); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("removing %s: %w", path, err)
	}
	if err := systemctl(ctx, "daemon-reload"); err != nil {
		return err
	}
	log.Info("service removed", "unit", path)
	fmt.Printf("Removed %s. The registration and %s stay — the runner can be started by hand.\n", path, defaultConfigPath)
	return nil
}

// systemctl runs one systemctl call and passes its complaint on unchanged: the
// message systemd writes is more useful than any wrapping of it.
func systemctl(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "systemctl", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// installServiceAfterRegister is the automatic path: on a systemd host, run as
// root, the registration ends with a service instead of with a command to type.
// Everywhere else it says what it did not do, so the next step stays visible.
func installServiceAfterRegister(ctx context.Context, configPath string, log *slog.Logger) {
	switch {
	case !systemdPresent():
		fmt.Printf("Start it with: covey-runner run --config %s\n"+
			"(no systemd here — `covey-runner install-service --print` writes the unit for your init system)\n", configPath)
	case os.Geteuid() != 0:
		fmt.Printf("Start it with: covey-runner run --config %s\n"+
			"As a service that survives the logout: sudo covey-runner install-service\n", configPath)
	default:
		if err := runInstallService(ctx, []string{"--config", configPath}, log); err != nil {
			// A failed service installation does not undo the registration —
			// it leaves a host that has to be started by hand, which is worth
			// one clear sentence rather than an exit code.
			fmt.Printf("The service could not be installed: %v\n"+
				"The registration stands — start it with: covey-runner run --config %s\n", err, configPath)
		}
	}
}
