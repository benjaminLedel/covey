package main

import (
	"context"
	"log/slog"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/config"
	"covey/internal/db"
)

// The subcommands are how an installation is administered when the interface
// is unreachable — the way back in. They need a database, so they get the same
// throwaway one the integration suite uses: created here, dropped afterwards,
// and skipped where none answers.
func testConfig(t *testing.T) config.Config {
	t.Helper()
	admin := os.Getenv("COVEY_TEST_DATABASE_URL")
	if admin == "" {
		admin = "postgres://covey:covey@localhost:5433/covey?sslmode=disable"
	}
	ctx := context.Background()
	pool, err := db.Connect(ctx, admin)
	if err != nil {
		t.Skipf("no test Postgres at %s: %v", admin, err)
	}
	name := "covey_cli_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		pool.Close()
	})

	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	cfg, err := config.FromEnv()
	if err != nil {
		t.Fatal(err)
	}
	cfg.DatabaseURL = u.String()
	cfg.DataDir = t.TempDir()
	return cfg
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// Migrations run behind an advisory lock and are the first thing every other
// command depends on. Running them twice must change nothing — that is what
// makes `serve` able to do it on every start.
func TestMigrateUpIsRepeatable(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runMigrate(ctx, cfg, nil, quiet()); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := runMigrate(ctx, cfg, []string{"up"}, quiet()); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if err := runMigrate(ctx, cfg, []string{"seitwärts"}, quiet()); err == nil {
		t.Error("an unknown direction was accepted")
	}
}

// Bootstrap creates the organisation, the admin and the demo agent — and it is
// idempotent, because it is what an upgrade script runs.
func TestBootstrapIsIdempotent(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runBootstrap(ctx, cfg, quiet()); err != nil {
		t.Fatalf("first bootstrap: %v", err)
	}

	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	count := func(table string) int {
		var n int
		if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	orgs, humans, agents := count("organizations"), count("humans"), count("agents")
	if orgs == 0 || humans == 0 || agents == 0 {
		t.Fatalf("bootstrap created nothing: %d orgs, %d humans, %d agents", orgs, humans, agents)
	}

	if err := runBootstrap(ctx, cfg, quiet()); err != nil {
		t.Fatalf("second bootstrap: %v", err)
	}
	if got := count("organizations"); got != orgs {
		t.Errorf("a second bootstrap created %d organisations instead of %d", got, orgs)
	}
	if got := count("agents"); got != agents {
		t.Errorf("a second bootstrap created %d agents instead of %d", got, agents)
	}
}

// `covey settings` is the way back in when the interface is unreachable. It
// shows what is set, marks what is merely the default, and never prints a
// secret's value — only whether there is one.
func TestSettingsSubcommand(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runMigrate(ctx, cfg, nil, quiet()); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runSettings(ctx, cfg, nil, quiet()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "signup.mode") || !strings.Contains(out, "(default)") {
		t.Errorf("the listing names neither the key nor the default:\n%s", out)
	}
	if !strings.Contains(out, "(none)") {
		t.Errorf("an unset secret is not marked as such:\n%s", out)
	}

	if err := runSettings(ctx, cfg, []string{"site.name", "Beispiel GmbH"}, quiet()); err != nil {
		t.Fatalf("setting a value: %v", err)
	}
	out = captureStdout(t, func() {
		if err := runSettings(ctx, cfg, nil, quiet()); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, "Beispiel GmbH") {
		t.Errorf("the changed value does not appear:\n%s", out)
	}

	// Opening registration is gated on a mailer that has PROVEN itself — a
	// host that is merely filled in proves nothing, and an instance that opens
	// without one cannot send the confirmation it promises.
	if err := runSettings(ctx, cfg, []string{"signup.mode", "waitlist"}, quiet()); err == nil {
		t.Error("registration was opened without a mail server that has been tested")
	}

	// The validation sits in the store, so the CLI cannot disagree with the API
	// about what is valid.
	if err := runSettings(ctx, cfg, []string{"signup.mode", "offen"}, quiet()); err == nil {
		t.Error("an invalid value was stored from the CLI")
	}
	if err := runSettings(ctx, cfg, []string{"signup.mod", "off"}, quiet()); err == nil {
		t.Error("a typo in the key was accepted")
	}
	if err := runSettings(ctx, cfg, []string{"eins", "zwei", "drei"}, quiet()); err == nil {
		t.Error("three arguments were accepted")
	}
}

// system_admin is what protects the organisations from one another, which is
// why it is granted here and nowhere over HTTP.
func TestSystemAdminSubcommand(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runBootstrap(ctx, cfg, quiet()); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runSystemAdmin(ctx, cfg, []string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	_ = out

	email := "admin@covey.local"
	if err := runSystemAdmin(ctx, cfg, []string{"add", email}); err != nil {
		t.Fatalf("granting: %v", err)
	}
	out = captureStdout(t, func() {
		if err := runSystemAdmin(ctx, cfg, []string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(out, email) {
		t.Errorf("the instance admin is not in the list:\n%s", out)
	}

	// The CLI path deliberately DOES demote the last one, unlike the browser:
	// whoever runs it on the server can grant it back at the same terminal,
	// and `covey system-admin add` needs no login. The round trip is the
	// reasoning, so it is what gets checked.
	if err := runSystemAdmin(ctx, cfg, []string{"remove", email}); err != nil {
		t.Fatalf("the CLI refused a demotion it is allowed to make: %v", err)
	}
	out = captureStdout(t, func() {
		if err := runSystemAdmin(ctx, cfg, []string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, email) {
		t.Errorf("the demotion did not take:\n%s", out)
	}
	if err := runSystemAdmin(ctx, cfg, []string{"add", email}); err != nil {
		t.Fatalf("the way back is blocked: %v", err)
	}

	if err := runSystemAdmin(ctx, cfg, []string{"add"}); err == nil {
		t.Error("add without an address was accepted")
	}
	if err := runSystemAdmin(ctx, cfg, []string{"erheben", email}); err == nil {
		t.Error("an unknown subcommand was accepted")
	}
	if err := runSystemAdmin(ctx, cfg, []string{"add", "niemand@nirgends.test"}); err == nil {
		t.Error("an address without an account was accepted")
	}
}

// The waitlist codes are how a closed instance lets somebody in. A code is
// shown once — afterwards only its prefix, which is also how it is revoked.
func TestWaitlistSubcommand(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runMigrate(ctx, cfg, nil, quiet()); err != nil {
		t.Fatal(err)
	}

	made := captureStdout(t, func() {
		if err := runWaitlist(ctx, cfg, []string{"new"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(made) == "" {
		t.Fatal("new printed no code")
	}

	list := captureStdout(t, func() {
		if err := runWaitlist(ctx, cfg, []string{"list"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(list) == "" {
		t.Error("the freshly created code is not in the list")
	}

	if err := runWaitlist(ctx, cfg, []string{"revoke"}); err == nil {
		t.Error("revoke without a prefix was accepted")
	}
	if err := runWaitlist(ctx, cfg, []string{"stornieren"}); err == nil {
		t.Error("an unknown subcommand was accepted")
	}
}

// `covey passwd` is the other way back in. It refuses what would lock somebody
// out rather than reporting a change it did not make.
func TestPasswdSubcommandRefusals(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runBootstrap(ctx, cfg, quiet()); err != nil {
		t.Fatal(err)
	}
	if err := runPasswd(ctx, cfg, nil, quiet()); err == nil {
		t.Error("passwd without an address was accepted")
	}
}

// The lint changes nothing: it reads, judges and describes. On a fresh
// installation there is nothing to describe, and that has to be an answer
// rather than an error.
func TestConfigLintOnAFreshInstallation(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runMigrate(ctx, cfg, nil, quiet()); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		if err := runConfigLint(ctx, cfg, []string{"lint"}); err != nil {
			t.Fatal(err)
		}
	})
	_ = out

	json := captureStdout(t, func() {
		if err := runConfigLint(ctx, cfg, []string{"lint", "--json"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.TrimSpace(json) != "[]" {
		t.Errorf("--json on an empty installation gave %q, expected an empty list", strings.TrimSpace(json))
	}

	if err := runConfigLint(ctx, cfg, nil); err == nil {
		t.Error("config without a subcommand was accepted")
	}
	if err := runConfigLint(ctx, cfg, []string{"lint", "--yaml"}); err == nil {
		t.Error("an unknown option was accepted")
	}
}

// The doctor reports what an installation is missing. It has to answer on one
// that is missing a lot — that is the case it exists for.
func TestDoctorOnAFreshInstallation(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runMigrate(ctx, cfg, nil, quiet()); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() {
		_ = runDoctor(ctx, cfg, nil)
	})
	if strings.TrimSpace(out) == "" {
		t.Error("the doctor said nothing at all")
	}
}

// The home-store sweep deletes blocks nothing references any more. It previews
// by default, because the thing it removes is the only copy of an agent's
// working history.
func TestHomeStoreCleanupSubcommand(t *testing.T) {
	cfg := testConfig(t)
	ctx := context.Background()
	if err := runMigrate(ctx, cfg, nil, quiet()); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		if err := runHomeStore(ctx, cfg, []string{"cleanup"}, quiet()); err != nil {
			t.Fatal(err)
		}
	})
	// The preview says that it is one — otherwise somebody reads a list of
	// blocks and believes they are gone.
	if !strings.Contains(out, "preview") || !strings.Contains(out, "--apply") {
		t.Errorf("the preview does not say it is one:\n%s", out)
	}
	if !strings.Contains(out, "home store:") {
		t.Errorf("the sweep does not name where it looked:\n%s", out)
	}

	out = captureStdout(t, func() {
		if err := runHomeStore(ctx, cfg, []string{"cleanup", "--apply"}, quiet()); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(out, "preview") {
		t.Errorf("--apply still called itself a preview:\n%s", out)
	}
	if !strings.Contains(out, "freed") {
		t.Errorf("the applied sweep does not say what it freed:\n%s", out)
	}

	if err := runHomeStore(ctx, cfg, nil, quiet()); err == nil {
		t.Error("home-store without a subcommand was accepted")
	}
	if err := runHomeStore(ctx, cfg, []string{"cleanup", "--force"}, quiet()); err == nil {
		t.Error("an unknown option was accepted")
	}

	// With the home store switched off there is nothing to sweep, and saying
	// so is better than sweeping a store nobody writes to.
	off := cfg
	off.HomeStore = false
	if err := runHomeStore(ctx, off, []string{"cleanup"}, quiet()); err == nil {
		t.Error("the sweep ran with the home store switched off")
	}
}

func TestStoreBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{
		{0, "0 B"}, {512, "512 B"},
		{1024, "1.0 KB"}, {1536, "1.5 KB"},
		{1 << 20, "1.0 MB"}, {1 << 30, "1.0 GB"}, {1 << 40, "1.0 TB"},
		// Beyond the last unit it stays in it rather than inventing one.
		{1 << 50, "1024.0 TB"},
	} {
		if got := storeBytes(tc.in); got != tc.want {
			t.Errorf("storeBytes(%d) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}
