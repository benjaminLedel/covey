package doctor

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/config"
	"covey/internal/db"
	"covey/migrations"
)

// The doctor is what somebody runs before an upgrade and reads when an agent
// will not start. Two properties make it worth anything: it changes nothing,
// and a finding that blocks says so — the figure a deploy script reads.
func TestReportCountsOnlyTheBlockingFindings(t *testing.T) {
	d := &doctor{}
	d.ok("database", "reachable")
	d.problem("images", "the sandbox image is missing", "make sandbox-images-pull", true)
	d.problem("egress", "enforcement is off", "COVEY_EGRESS_ENFORCE=true", false)
	d.problem("home store", "no object store", "COVEY_BLOB_STORE=s3", true)

	rep := d.report()
	if len(rep.Findings) != 4 {
		t.Fatalf("the report holds %d findings", len(rep.Findings))
	}
	if rep.Blocking != 2 {
		t.Errorf("Blocking = %d, expected 2", rep.Blocking)
	}
	// A finding that blocks has to name a remedy — one that only says no
	// leaves the reader to derive it.
	for _, f := range rep.Findings {
		if f.Blocking && f.Remedy == "" {
			t.Errorf("%q blocks and names no remedy", f.What)
		}
		if f.OK && f.Blocking {
			t.Errorf("%q is both fine and blocking", f.What)
		}
	}
}

// A database that does not answer is the first finding and the blocking one:
// every other check reads from it, so reporting them would be guessing.
func TestRunWithoutADatabase(t *testing.T) {
	cfg := config.Config{DatabaseURL: "postgres://covey:covey@127.0.0.1:15999/covey?sslmode=disable"}
	rep := Run(context.Background(), cfg)
	if rep.Blocking == 0 {
		t.Fatal("an unreachable database is not reported as blocking")
	}
	if len(rep.Findings) != 1 {
		t.Errorf("with no database the doctor reported %d findings — the rest would be guesswork", len(rep.Findings))
	}
	if !strings.Contains(rep.Findings[0].Remedy, "COVEY_DATABASE_URL") {
		t.Errorf("the remedy does not name the setting: %q", rep.Findings[0].Remedy)
	}
}

// Against a migrated instance the doctor answers without falling over, and it
// changes nothing — which is the property that lets somebody run it on a
// machine they are worried about.
func TestRunAgainstAFreshInstance(t *testing.T) {
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
	name := "covey_doctor_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
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
	defer pool.Close()

	// Before the migrations: the honest answer is "not answerable yet", not a
	// Postgres error about a column that does not exist.
	before := RunWith(ctx, config.Config{DatabaseURL: u.String()}, pool)
	if len(before.Findings) == 0 {
		t.Fatal("the doctor said nothing about an unmigrated instance")
	}
	// Pending migrations are deliberately NOT blocking: they run by themselves
	// at the next `covey serve`, so reporting them as a wall would send an
	// operator looking for something to do that is already arranged.
	var named bool
	for _, f := range before.Findings {
		if f.What == "migrations" {
			named = true
			if f.Blocking {
				t.Error("pending migrations are reported as blocking — they run by themselves")
			}
			if f.Remedy == "" {
				t.Error("the migrations finding names no remedy")
			}
		}
	}
	if !named {
		t.Errorf("the doctor says nothing about the migrations: %+v", before.Findings)
	}
	for _, f := range before.Findings {
		if strings.Contains(strings.ToLower(f.Detail), "does not exist") {
			t.Errorf("a Postgres error leaked into the report: %q", f.Detail)
		}
	}

	if _, err := db.MigrateUp(ctx, pool, migrations.FS); err != nil {
		t.Fatal(err)
	}
	after := RunWith(ctx, config.Config{
		DatabaseURL: u.String(),
		DataDir:     t.TempDir(),
		HomeStore:   true,
		BlobStore:   "builtin",
	}, pool)
	if len(after.Findings) <= len(before.Findings) {
		t.Errorf("after the migrations the doctor said no more than before (%d vs %d)",
			len(after.Findings), len(before.Findings))
	}
	// It changes nothing: a second pass says the same as the first.
	second := RunWith(ctx, config.Config{
		DatabaseURL: u.String(),
		DataDir:     t.TempDir(),
		HomeStore:   true,
		BlobStore:   "builtin",
	}, pool)
	if second.Blocking != after.Blocking {
		t.Errorf("two passes disagree: %d vs %d blocking", after.Blocking, second.Blocking)
	}
	for _, f := range after.Findings {
		if f.What == "" || f.Detail == "" {
			t.Errorf("a finding without a subject or a detail: %+v", f)
		}
	}
}

func TestHumanBytes(t *testing.T) {
	for _, tc := range []struct {
		in   int64
		want string
	}{{0, "0 B"}, {999, "999 B"}, {1024, "1.0 KB"}, {1 << 20, "1.0 MB"}, {1 << 30, "1.0 GB"}} {
		if got := humanBytes(tc.in); got != tc.want {
			t.Errorf("humanBytes(%d) = %q, expected %q", tc.in, got, tc.want)
		}
	}
}

func TestDirSize(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(dir+"/eins", []byte("12345"), 0o600)
	os.WriteFile(dir+"/zwei", []byte("123"), 0o600)
	size, n := dirSize(dir)
	if n != 2 || size != 8 {
		t.Errorf("dirSize = %d bytes in %d files, expected 8 in 2", size, n)
	}
	// A directory that is not there is not an error here — the store simply
	// holds nothing yet.
	if size, n := dirSize(dir + "/gibtesnicht"); size != 0 || n != 0 {
		t.Errorf("a missing directory gave %d/%d", size, n)
	}
}
