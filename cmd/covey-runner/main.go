// covey-runner is the standalone runner (spec/16): a process on an arbitrary
// host that registers with the control plane and gets sandboxes assigned to it
// from there.
//
// A binary of its own, and not a subcommand of `covey`: on a runner host
// `serve`, `migrate` and `bootstrap` should not exist at all, and the trust
// boundary ("no database access") reads badly when the database code is
// compiled in beside it. The easy path is bought by the built-in runner, which
// needs no artefact whatsoever — not by merging two that have different jobs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"covey/internal/buildinfo"
	"covey/internal/homestore"
	"covey/internal/runner"
)

// defaultConfigPath is where `register` deposits its result. Deliberately a
// file and not just environment variables: the runner runs as a service on a
// machine that otherwise has nothing to do with Covey, and register has to be
// able to put its result somewhere.
const defaultConfigPath = "/etc/covey-runner/config.toml"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var err error
	switch os.Args[1] {
	case "register":
		err = runRegister(ctx, os.Args[2:], log)
	case "run":
		err = runRun(ctx, os.Args[2:], log)
	case "version", "--version", "-v":
		fmt.Println("covey-runner " + buildinfo.String())
		fmt.Printf("runner protocol %d\n", runner.Protocol)
		fmt.Println("AGPL-3.0 · source: " + buildinfo.SourceURL)
		return
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
	fmt.Fprintln(os.Stderr, `covey-runner — runs sandboxes for a Covey control plane

  covey-runner register --url <control plane> --token <registration token>
                        [--tag php --tag arm64] [--description "Build host Frankfurt"]
                        [--config <path>] [--work-dir <path>]
  covey-runner run      [--config <path>]
  covey-runner version

register writes the received runner token into the configuration file
(`+defaultConfigPath+`, overridable with --config). run then holds the
connection to the control plane.

The runner needs Docker on this host and no access to anything else — no
database, no object store. It speaks exclusively the runner protocol.`)
}

// config is what register deposits and run reads.
type config struct {
	URL     string
	Token   string
	WorkDir string
	Images  []string
	Tags    []string
}

func runRegister(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	url := fs.String("url", "", "the control plane's address, e.g. https://covey.example")
	token := fs.String("token", "", "the organisation's registration token")
	description := fs.String("description", "", "what this host is, for the runner view")
	configPath := fs.String("config", defaultConfigPath, "where the configuration is written")
	workDir := fs.String("work-dir", "/var/lib/covey-runner", "working copies and local blocks")
	var images stringList
	fs.Var(&images, "image", "a sandbox image this host holds (repeatable); default covey-sandbox:latest")
	var tags stringList
	fs.Var(&tags, "tag", "a capability of this host (repeatable), e.g. arm64")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *url == "" || *token == "" {
		return errors.New("--url and --token are required")
	}

	rn, runnerToken, err := register(ctx, *url, *token, *description, tags)
	if err != nil {
		return err
	}
	if len(images) == 0 {
		images = stringList{"covey-sandbox:latest"}
	}
	cfg := config{URL: strings.TrimRight(*url, "/"), Token: runnerToken, WorkDir: *workDir, Images: images, Tags: tags}
	if err := writeConfig(*configPath, cfg); err != nil {
		return fmt.Errorf("writing %s: %w — the runner token is lost with it, register again", *configPath, err)
	}
	// The organisation is named in the output on purpose: a runner inherits it
	// from the registration token and cannot change it, and somebody will
	// otherwise register a build host in the wrong one and search for why its
	// agents never arrive.
	log.Info("runner registered", "runner", rn.RunnerID, "organisation", rn.OrgID, "config", *configPath)
	fmt.Printf("Registered as runner %s (organisation %s).\n", rn.RunnerID, rn.OrgID)

	// And then actually connect once. Registering is an HTTP call; running is a
	// WebSocket, and the two fail differently — a reverse proxy that does not
	// upgrade, a TLS chain the host does not trust, a firewall that lets 443
	// through but not the upgrade. Finding that out here, while somebody is
	// standing at this shell, is worth the two seconds.
	if err := verifyConnection(ctx, cfg); err != nil {
		fmt.Printf("\nBut the connection does not come up:\n  %v\n\n"+
			"The registration stands and the configuration is written — fix the connection\n"+
			"and start it with: covey-runner run --config %s\n", err, *configPath)
		return err
	}
	fmt.Printf("Connection checked.\nStart it with: covey-runner run --config %s\n", *configPath)
	return nil
}

// verifyConnection opens the runner link once and lets it go again. It proves
// what `register` alone cannot: that this host can reach the control plane the
// way a runner has to.
func verifyConnection(ctx context.Context, cfg config) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	t, err := runner.Dial(ctx, cfg.URL, cfg.Token)
	if err != nil {
		return err
	}
	return t.Close()
}

func runRun(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	configPath := fs.String("config", defaultConfigPath, "the configuration written by register")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := readConfig(*configPath)
	if err != nil {
		return err
	}
	if cfg.URL == "" || cfg.Token == "" {
		return fmt.Errorf("%s carries no url/token — run `covey-runner register` first", *configPath)
	}

	me, err := whoami(ctx, cfg.URL, cfg.Token)
	if err != nil {
		return err
	}

	docker := &runner.Docker{
		RunnerID: me.RunnerID,
		Image:    firstOr(cfg.Images, "covey-sandbox:latest"),
		DataDir:  cfg.WorkDir,
	}
	node := runner.NewNode(me.RunnerID, me.OrgID, docker, log)
	node.Tags = cfg.Tags
	node.Images = cfg.Images
	// The blocks come through the control plane: a runner never gets the
	// store's credentials (spec/16, "Trust boundary").
	node.Blobs = homestore.NewHTTPStore(cfg.URL, cfg.Token)

	log.Info("covey-runner", "version", buildinfo.String(), "protocol", runner.Protocol,
		"runner", me.RunnerID, "organisation", me.OrgID, "work-dir", cfg.WorkDir,
		"arch", runtime.GOARCH)
	return runner.RunNode(ctx, node, cfg.URL, cfg.Token, 5*time.Second)
}

// writeConfig deposits the configuration with permissions that match its
// content: the runner token is what lets a host act for an organisation.
func writeConfig(path string, cfg config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("# covey-runner — written by `covey-runner register`.\n")
	b.WriteString("# The token is what this host acts for its organisation with. Keep it as such.\n")
	fmt.Fprintf(&b, "url = %q\n", cfg.URL)
	fmt.Fprintf(&b, "token = %q\n", cfg.Token)
	fmt.Fprintf(&b, "work_dir = %q\n", cfg.WorkDir)
	fmt.Fprintf(&b, "images = [%s]\n", quoteList(cfg.Images))
	fmt.Fprintf(&b, "tags = [%s]\n", quoteList(cfg.Tags))
	return os.WriteFile(path, []byte(b.String()), 0o600)
}

// readConfig reads the handful of keys register writes. Deliberately no TOML
// library: five scalar keys and one list are not worth a dependency on a host
// that is supposed to carry as little of Covey as possible.
func readConfig(path string) (config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return config{}, fmt.Errorf("%s: %w — run `covey-runner register` first", path, err)
	}
	cfg := config{WorkDir: "/var/lib/covey-runner", Images: []string{"covey-sandbox:latest"}}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		switch key {
		case "tags":
			cfg.Tags = parseList(value)
			continue
		case "images":
			cfg.Images = parseList(value)
			continue
		}
		value = strings.Trim(value, `"`)
		switch key {
		case "url":
			cfg.URL = value
		case "token":
			cfg.Token = value
		case "work_dir":
			cfg.WorkDir = value
		}
	}
	return cfg, nil
}

func quoteList(items []string) string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, fmt.Sprintf("%q", item))
	}
	return strings.Join(quoted, ", ")
}

func parseList(value string) []string {
	value = strings.Trim(strings.TrimSpace(value), "[]")
	var out []string
	for _, item := range strings.Split(value, ",") {
		item = strings.Trim(strings.TrimSpace(item), `"`)
		if item != "" {
			out = append(out, item)
		}
	}
	return out
}

func firstOr(items []string, fallback string) string {
	if len(items) > 0 {
		return items[0]
	}
	return fallback
}

// stringList collects a repeatable flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// identity is what the control plane says this runner is.
type identity struct {
	RunnerID uuid.UUID `json:"runner_id"`
	OrgID    uuid.UUID `json:"org_id"`
}
