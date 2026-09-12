package main

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/db"
	"covey/migrations"
)

// The seeder builds the demo organisation behind the screenshots, and it is
// run by hand against a fresh instance — which is exactly the kind of program
// that rots unnoticed. A migration renames a column, nobody seeds for three
// months, and the next demo is the moment it comes out.
func seedDB(t *testing.T) (string, *pgxpool.Pool) {
	t.Helper()
	admin := os.Getenv("COVEY_TEST_DATABASE_URL")
	if admin == "" {
		admin = "postgres://covey:covey@localhost:5433/covey?sslmode=disable"
	}
	ctx := context.Background()
	adminPool, err := db.Connect(ctx, admin)
	if err != nil {
		t.Skipf("no test Postgres at %s: %v", admin, err)
	}
	name := "covey_seed_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		adminPool.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		adminPool.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
		adminPool.Close()
	})

	u, _ := url.Parse(admin)
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		t.Fatal(err)
	}
	orgID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO organizations (id, name) VALUES ($1, 'Beispiel GmbH')`, orgID); err != nil {
		t.Fatal(err)
	}
	// The bootstrap admin becomes the boss of the demo org chart — without one
	// the seeder has nobody to hang the workforce under, and says so. A seat
	// needs its account since P1: a person without a login could not sign in,
	// and the tests used to build exactly that unnoticed.
	accountID := uuid.New()
	if _, err := pool.Exec(ctx,
		`INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
		 VALUES ($1,'admin@covey.local','x','Admin',now())`, accountID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		 VALUES ($1,$2,$3,'admin@covey.local','Admin','x','org_admin')`,
		uuid.New(), orgID, accountID); err != nil {
		t.Fatal(err)
	}
	return u.String(), pool
}

func count(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// What the seeder produces is the demo: a workforce, a backlog with columns,
// costs over time and a recording somebody can open. Each of those is a
// screenshot, and an empty one is a screenshot of nothing.
func TestSeedBuildsTheDemoOrganisation(t *testing.T) {
	dsn, pool := seedDB(t)
	ctx := context.Background()

	if err := run(ctx, dsn, false); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for _, table := range []string{"agents", "backlog_tasks", "agent_stages", "departments", "recording_events"} {
		if n := count(t, pool, table); n == 0 {
			t.Errorf("%s is empty after the seed", table)
		}
	}
}

// Repeatable: what an earlier run created goes out first. Otherwise a second
// seed doubles the workforce, and the demo shows two of everybody.
func TestSeedIsRepeatable(t *testing.T) {
	dsn, pool := seedDB(t)
	ctx := context.Background()

	if err := run(ctx, dsn, false); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	first := count(t, pool, "agents")

	// The second run needs -force, because by then the instance looks used —
	// which is the guard, not an obstacle.
	if err := run(ctx, dsn, true); err != nil {
		t.Fatalf("second seed: %v", err)
	}
	if got := count(t, pool, "agents"); got != first {
		t.Errorf("the second seed left %d agents instead of %d", got, first)
	}
}

// The guard against the most expensive mistake: seeding over an instance
// somebody is using. It deletes every agent of the organisation, so it has to
// refuse where that would take real work with it.
func TestSeedRefusesAnInstanceThatLooksUsed(t *testing.T) {
	dsn, pool := seedDB(t)
	ctx := context.Background()

	if err := run(ctx, dsn, false); err != nil {
		t.Fatalf("first seed: %v", err)
	}
	err := run(ctx, dsn, false)
	if err == nil {
		t.Fatal("the seed ran over an instance that looks used")
	}
	if !strings.Contains(err.Error(), "force") {
		t.Errorf("the refusal does not name the way past it: %v", err)
	}
	_ = pool
}

// Without an organisation there is nothing to seed into, and the message has
// to name the command that creates one.
func TestSeedWithoutAnOrganisation(t *testing.T) {
	admin := os.Getenv("COVEY_TEST_DATABASE_URL")
	if admin == "" {
		admin = "postgres://covey:covey@localhost:5433/covey?sslmode=disable"
	}
	ctx := context.Background()
	adminPool, err := db.Connect(ctx, admin)
	if err != nil {
		t.Skipf("no test Postgres at %s: %v", admin, err)
	}
	defer adminPool.Close()
	name := "covey_seed_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	if _, err := adminPool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer adminPool.Exec(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")

	u, _ := url.Parse(admin)
	u.Path = "/" + name
	pool, err := db.Connect(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		t.Fatal(err)
	}
	pool.Close()

	err = run(ctx, u.String(), false)
	if err == nil {
		t.Fatal("the seed ran without an organisation")
	}
	if !strings.Contains(err.Error(), "bootstrap") {
		t.Errorf("the message does not name the command that creates one: %v", err)
	}
}

func TestEnvOr(t *testing.T) {
	t.Setenv("COVEY_SEED_TEST", "")
	if got := envOr("COVEY_SEED_TEST", "fallback"); got != "fallback" {
		t.Errorf("envOr = %q", got)
	}
	t.Setenv("COVEY_SEED_TEST", "gesetzt")
	if got := envOr("COVEY_SEED_TEST", "fallback"); got != "gesetzt" {
		t.Errorf("envOr = %q", got)
	}
}

func TestMaxInt(t *testing.T) {
	if maxInt(3, 7) != 7 || maxInt(7, 3) != 7 || maxInt(-1, -5) != -1 {
		t.Error("maxInt does not pick the larger one")
	}
}
