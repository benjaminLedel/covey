package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"covey/internal/config"
	"covey/internal/memory"
)

// The blob store is a port following the pattern of IdentityProvider and
// SecretStore: the directory is the default on purpose, so that "one binary +
// Postgres" does not quietly become "one binary + Postgres + MinIO".
func TestOpenBlobStore(t *testing.T) {
	ctx := context.Background()
	log := quiet()

	for _, kind := range []string{"", "builtin", "BUILTIN", " builtin "} {
		cfg := config.Config{BlobStore: kind, DataDir: t.TempDir()}
		store, where, err := openBlobStore(ctx, cfg, log)
		if err != nil {
			t.Fatalf("%q: %v", kind, err)
		}
		if store == nil || where == "" {
			t.Errorf("%q gave no store", kind)
		}
		// The blocks land beside the data directory, not somewhere derived.
		if !strings.HasPrefix(where, cfg.DataDir) {
			t.Errorf("%q put the blocks at %q, outside %q", kind, where, cfg.DataDir)
		}
		if _, err := os.Stat(filepath.Join(cfg.DataDir, "blocks")); err != nil {
			t.Errorf("%q did not create the directory: %v", kind, err)
		}
	}

	// An unimplemented backend is named rather than silently falling back to
	// the directory — a homes store somebody configured and did not get is
	// worse than one that refuses to start.
	_, _, err := openBlobStore(ctx, config.Config{BlobStore: "gcs", DataDir: t.TempDir()}, log)
	if err == nil || !strings.Contains(err.Error(), "builtin") {
		t.Errorf("an unknown blob store was accepted: %v", err)
	}
}

// The built-in embedder is the default and says what it costs: the vector
// search then only measures word overlap.
func TestBuildEmbedder(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	for _, provider := range []string{"", "builtin", " BUILTIN "} {
		e, err := buildEmbedder(config.Config{EmbeddingProvider: provider}, log)
		if err != nil {
			t.Fatalf("%q: %v", provider, err)
		}
		if _, ok := e.(memory.HashEmbedder); !ok {
			t.Errorf("%q did not give the built-in embedder but %T", provider, e)
		}
	}
	// A provider this binary does not carry is named at startup rather than at
	// the first page somebody writes.
	if _, err := buildEmbedder(config.Config{EmbeddingProvider: "erfunden"}, log); err == nil {
		t.Error("an unknown embedding provider was accepted")
	}
}

// The ENV addition to the allowlist is copied, not shared: a caller that
// appends to the result must not grow the configuration.
func TestEgressBaseAllowIsACopy(t *testing.T) {
	cfg := config.Config{EgressAllow: []string{"host.docker.internal"}}
	got := egressBaseAllow(cfg)
	if len(got) != 1 || got[0] != "host.docker.internal" {
		t.Fatalf("egressBaseAllow = %v", got)
	}
	got = append(got, "noch.einer")
	if len(cfg.EgressAllow) != 1 {
		t.Errorf("appending to the result changed the configuration: %v", cfg.EgressAllow)
	}
	if got := egressBaseAllow(config.Config{}); len(got) != 0 {
		t.Errorf("without a setting: %v", got)
	}
}

func TestGetenvDefault(t *testing.T) {
	t.Setenv("COVEY_WIRING_TEST", "")
	if got := getenvDefault("COVEY_WIRING_TEST", "fallback"); got != "fallback" {
		t.Errorf("getenvDefault = %q", got)
	}
	t.Setenv("COVEY_WIRING_TEST", "gesetzt")
	if got := getenvDefault("COVEY_WIRING_TEST", "fallback"); got != "gesetzt" {
		t.Errorf("getenvDefault = %q", got)
	}
}

// The demo agent is the first thing an installation shows. Its configuration
// is written by bootstrap and is the one example every new operator reads, so
// it has to carry the files the compiler expects and say what it cannot do.
func TestDefaultDemoConfig(t *testing.T) {
	cfg := defaultDemoConfig()
	for _, file := range []string{"SOUL.md", "CAPABILITIES.md", "PLAYBOOKS.md", "ORG.md"} {
		body, ok := cfg[file]
		if !ok {
			t.Errorf("the demo agent carries no %s", file)
			continue
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s is empty", file)
		}
		if !strings.HasPrefix(strings.TrimSpace(body), "#") {
			t.Errorf("%s does not start with a heading", file)
		}
	}
	// It has no target system, and it says so — an agent that acted as if it
	// had one would be the first thing to go wrong.
	if !strings.Contains(cfg["SOUL.md"], "No target system") {
		t.Error("the demo agent does not say that it has no target system")
	}
}

// The usage text is what somebody sees who types the binary's name. Every
// subcommand main() dispatches on has to appear in it.
func TestUsageNamesEverySubcommand(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	usage()
	w.Close()
	os.Stderr = old
	buf := make([]byte, 8192)
	n, _ := r.Read(buf)
	out := string(buf[:n])

	for _, cmd := range []string{
		"migrate", "bootstrap", "passwd", "serve", "egress-proxy",
		"settings", "system-admin", "waitlist", "config", "doctor",
		"plugin", "style", "version",
	} {
		if !strings.Contains(out, cmd) {
			t.Errorf("the usage does not name %q:\n%s", cmd, out)
		}
	}
}
