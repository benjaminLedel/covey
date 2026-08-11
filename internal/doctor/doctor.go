// Package doctor answers, for one installation, what a restart or an upgrade
// would run into — before it does.
//
// Its own package because it has two callers and they are the point: `covey
// doctor` for whoever is at a shell before the restart, and the platform
// administration for whoever operates this instance through a browser. The
// agent config lint learned the same lesson earlier: a subcommand nobody runs
// is a check that effectively does not exist.
//
// Everything in here reads. Nothing changes state.
package doctor

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/config"
	"covey/internal/db"
	"covey/internal/homestore"
	runnerstore "covey/internal/runner/store"
	"covey/migrations"
)

// Finding is one answer. Blocking = an agent cannot work until this is fixed;
// everything else is worth knowing but not in the way.
type Finding struct {
	OK       bool   `json:"ok"`
	Blocking bool   `json:"blocking"`
	What     string `json:"what"`
	Detail   string `json:"detail"`
	Remedy   string `json:"remedy,omitempty"`
}

// Report is what one pass found.
type Report struct {
	Findings []Finding `json:"findings"`
	// Blocking counts the findings that keep agents from working — the figure
	// a deploy script and a page header both want.
	Blocking int `json:"blocking"`
}

// Run performs all checks against its own database connection — the shape the
// CLI needs, which has no pool yet.
func Run(ctx context.Context, cfg config.Config) Report {
	d := &doctor{cfg: cfg}
	pool, err := db.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		d.problem("database", "not reachable: "+err.Error(),
			"check COVEY_DATABASE_URL and whether Postgres is running", true)
		return d.report()
	}
	defer pool.Close()
	return RunWith(ctx, cfg, pool)
}

// RunWith performs them against an existing pool — the shape the control plane
// needs, which has one.
func RunWith(ctx context.Context, cfg config.Config, pool *pgxpool.Pool) Report {
	d := &doctor{cfg: cfg}
	d.ok("database", "reachable")
	d.check(ctx, pool)
	return d.report()
}

type doctor struct {
	cfg      config.Config
	findings []Finding
}

func (d *doctor) report() Report {
	out := Report{Findings: d.findings}
	for _, f := range d.findings {
		if f.Blocking {
			out.Blocking++
		}
	}
	return out
}

func (d *doctor) add(f Finding) { d.findings = append(d.findings, f) }

func (d *doctor) ok(what, detail string) {
	d.add(Finding{OK: true, What: what, Detail: detail})
}

func (d *doctor) problem(what, detail, remedy string, blocking bool) {
	d.add(Finding{Blocking: blocking, What: what, Detail: detail, Remedy: remedy})
}

func (d *doctor) check(ctx context.Context, pool *pgxpool.Pool) {
	// The order matters: with migrations still pending, the columns the checks
	// below read do not exist yet. Saying "not answerable until the migrations
	// have run" is the honest answer — leaking a Postgres error about a missing
	// column would send the reader after the wrong thing entirely.
	migrated := d.checkMigrations(ctx, pool)
	if !migrated {
		d.add(Finding{What: "the rest",
			Detail: "answerable once the migrations have run — start `covey serve` once, then ask again"})
		d.checkEgress(ctx)
		return
	}
	d.checkImages(ctx, pool)
	d.checkHomeStore(ctx, pool)
	d.checkEgress(ctx)
	d.checkRunners(ctx, pool)
}

// checkMigrations says what a restart is about to do to the database — the one
// moment somebody would want a backup.
func (d *doctor) checkMigrations(ctx context.Context, pool *pgxpool.Pool) bool {
	pending, err := db.Pending(ctx, pool, migrations.FS)
	if err != nil {
		d.problem("migrations", "not readable: "+err.Error(), "", false)
		return false
	}
	if len(pending) == 0 {
		d.ok("migrations", "up to date")
		return true
	}
	// Named individually while they still fit on a screen — that is the upgrade
	// case, and there the names are what somebody looks up. A fresh database
	// has all of them, and listing fifty-six is a wall nobody reads.
	detail := fmt.Sprintf("%d pending: %s", len(pending), strings.Join(pending, ", "))
	remedy := "they run by themselves at the next `covey serve` — take a database backup first"
	if len(pending) > 8 {
		detail = fmt.Sprintf("%d pending (%s … %s)", len(pending), pending[0], pending[len(pending)-1])
		remedy = "a database this far behind is usually an empty one — `covey bootstrap` sets it up"
	}
	d.problem("migrations", detail, remedy, false)
	return false
}

// checkImages is the one that bites at an upgrade: the image an agent is
// pointed at may not exist on this host. Asked per image in use, so the answer
// names how many agents are waiting on it.
func (d *doctor) checkImages(ctx context.Context, pool *pgxpool.Pool) {
	registry := agents.NewRegistry(pool)
	wanted, err := registry.SandboxImagesInUse(ctx)
	if err != nil {
		d.problem("sandbox images", "not readable: "+err.Error(), "", false)
		return
	}
	profiles := map[string]string{"base": d.cfg.SandboxImage, "dev": d.cfg.SandboxImageDev}

	byImage := map[string]int{}
	for value, n := range wanted {
		image := strings.TrimSpace(value)
		switch {
		case image == "":
			image = d.cfg.SandboxImage
		case profiles[image] != "":
			image = profiles[image]
		}
		byImage[image] += n
	}
	if len(byImage) == 0 {
		d.ok("sandbox images", "no agents yet")
		return
	}

	names := make([]string, 0, len(byImage))
	for image := range byImage {
		names = append(names, image)
	}
	sort.Strings(names)

	if !dockerReachable(ctx) {
		d.problem("docker", "no daemon reachable",
			"running the control plane in a container? then it needs the host's socket: "+
				"-v /var/run/docker.sock:/var/run/docker.sock", true)
		return
	}
	d.ok("docker", "reachable")

	for _, image := range names {
		if imageExists(ctx, image) {
			d.ok("image "+image, fmt.Sprintf("present, %d agent(s)", byImage[image]))
			continue
		}
		target := "make sandbox-image"
		if strings.Contains(image, "sandbox-dev") {
			target = "make sandbox-image-dev"
		}
		d.problem("image "+image,
			fmt.Sprintf("missing, %d agent(s) work in it", byImage[image]),
			"build it: "+target, true)
	}
}

// checkHomeStore names the new operational obligation rather than assuming it
// is known: the blocks need backup like the database.
func (d *doctor) checkHomeStore(ctx context.Context, pool *pgxpool.Pool) {
	if !d.cfg.HomeStore {
		d.problem("home store", "switched off (COVEY_HOME_STORE=false)",
			"homes then have no snapshots, no rollback, and are unrecoverable when lost", false)
		return
	}
	switch strings.ToLower(strings.TrimSpace(d.cfg.BlobStore)) {
	case "", "builtin":
		dir := filepath.Join(d.cfg.DataDir, "blocks")
		size, files := dirSize(dir)
		detail := "directory " + dir
		if files > 0 {
			detail += fmt.Sprintf(", %s in %d blocks", humanBytes(size), files)
		} else {
			detail += ", still empty"
		}
		d.problem("home store", detail,
			"this directory needs backup like the database — of a 7 GB home, 48 MB exist nowhere else", false)
	case "s3":
		store, err := homestore.NewS3(d.cfg.S3Endpoint, d.cfg.S3Bucket, d.cfg.S3Prefix, homestore.Credentials{
			AccessKey: d.cfg.S3AccessKey, SecretKey: d.cfg.S3SecretKey, Region: d.cfg.S3Region,
		}, d.cfg.S3PathStyle)
		if err != nil {
			d.problem("home store", "s3 configuration incomplete: "+err.Error(),
				"COVEY_S3_ENDPOINT, COVEY_S3_BUCKET, COVEY_S3_ACCESS_KEY, COVEY_S3_SECRET_KEY", true)
			return
		}
		probeCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if err := store.Check(probeCtx); err != nil {
			d.problem("home store", "object store not usable: "+err.Error(),
				"homes cannot be synced until it is", true)
			return
		}
		d.ok("home store", "object store "+d.cfg.S3Endpoint+"/"+d.cfg.S3Bucket+" reachable and writable")
	default:
		d.problem("home store", "unknown backend "+d.cfg.BlobStore, "COVEY_BLOB_STORE: builtin | s3", true)
	}

	var snapshots, withHome int
	_ = pool.QueryRow(ctx,
		`SELECT count(*), count(DISTINCT agent_id) FROM home_snapshots`).Scan(&snapshots, &withHome)
	if snapshots > 0 {
		d.ok("snapshots", fmt.Sprintf("%d, over %d agent(s)", snapshots, withHome))
	}
}

// checkEgress looks at the enforcement point — including the objects of
// earlier versions, which the control plane clears away at the next start.
func (d *doctor) checkEgress(ctx context.Context) {
	if !d.cfg.EgressEnforce {
		d.ok("egress", "not enforced (COVEY_EGRESS_ENFORCE=false)")
		return
	}
	if d.cfg.EgressIsolation != "network" {
		d.ok("egress", "cooperative — the agent can bypass it; `network` is the hard mode")
		return
	}
	if !dockerReachable(ctx) {
		return // already reported
	}
	if !imageExists(ctx, "covey-egress:latest") {
		d.problem("egress proxy", "image covey-egress:latest missing, and hard isolation needs it",
			"build it: make egress-image", true)
	} else {
		d.ok("egress proxy", "image present, hard network isolation")
	}
	if legacyEgressPresent(ctx) {
		d.problem("egress (old)", "objects of an earlier version still there",
			"they are removed at the next `covey serve` — the segment carries the runner now", false)
	}
}

func (d *doctor) checkRunners(ctx context.Context, pool *pgxpool.Pool) {
	store := runnerstore.NewStore(pool)
	rows, err := pool.Query(ctx, `SELECT id FROM organizations`)
	if err != nil {
		return
	}
	defer rows.Close()
	var orgs []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err == nil {
			orgs = append(orgs, id)
		}
	}
	var withRemote int
	for _, org := range orgs {
		if remote, err := store.HasRemote(ctx, org); err == nil && remote {
			withRemote++
		}
	}
	switch {
	case len(orgs) == 0:
		return
	case withRemote == 0:
		d.ok("runners", fmt.Sprintf("%d organisation(s), all on the built-in runner", len(orgs)))
	default:
		d.ok("runners", fmt.Sprintf("%d of %d organisation(s) have a registered runner — "+
			"their built-in one has stood down", withRemote, len(orgs)))
	}
}

// --- the small helpers, all of them read-only ---

func dockerReachable(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Run() == nil
}

func imageExists(ctx context.Context, image string) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, "docker", "image", "inspect", image).Run() == nil
}

func legacyEgressPresent(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if exec.CommandContext(ctx, "docker", "network", "inspect", "covey-egress-internal").Run() == nil {
		return true
	}
	return exec.CommandContext(ctx, "docker", "inspect", "covey-egress-proxy").Run() == nil
}

func dirSize(root string) (int64, int) {
	var total int64
	var files int
	_ = filepath.WalkDir(root, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil //nolint:nilerr // a store that is not there is simply empty
		}
		if info, err := entry.Info(); err == nil {
			total += info.Size()
			files++
		}
		return nil
	})
	return total, files
}

func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	v := float64(n)
	i := 0
	for v >= 1024 && i < len(units)-1 {
		v /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", v, units[i])
}
