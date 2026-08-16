package integration

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"

	"covey/internal/config"
	"covey/internal/homestore"
	"covey/internal/orchestrator"
)

// The runner view and the home store's interface (spec/16, stages 5 and 6). A
// store that grows quietly in the background and whose content nobody can see
// is an operational risk — you notice it when the disk is full. These tests are
// about the figures somebody acts on, so what matters is that they are the
// right ones and not merely present.

func TestRunnerViewShowsWhatIsConnectedAndWhatItCarries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, runnerID := filesStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")

	var view []struct {
		ID   uuid.UUID `json:"id"`
		Kind string    `json:"kind"`
		Live *struct {
			Connected bool `json:"connected"`
			Sandboxes int  `json:"sandboxes"`
			Outdated  bool `json:"outdated"`
		} `json:"live"`
		Capacity *struct {
			TotalBytes int64  `json:"total_bytes"`
			FreeBytes  int64  `json:"free_bytes"`
			WorkDir    string `json:"work_dir"`
		} `json:"capacity"`
	}
	resp := c.do(http.MethodGet, "/api/v1/runners", nil)
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(view) != 1 || view[0].ID != runnerID {
		t.Fatalf("the organisation's built-in runner has to be in the list: %+v", view)
	}
	if view[0].Kind != "builtin" {
		t.Errorf("kind: %q", view[0].Kind)
	}
	// The row says what a runner IS, the pool what it is DOING. Only both
	// together answer the question somebody comes to this page with.
	if view[0].Live == nil || !view[0].Live.Connected {
		t.Fatal("a connected runner has to be shown as connected")
	}
	if view[0].Live.Outdated {
		t.Error("a runner of this build speaks the current protocol")
	}
	// Free disk is the figure that decides whether the next home still fits —
	// it comes from the file system the working copies lie on.
	if view[0].Capacity == nil || view[0].Capacity.TotalBytes == 0 {
		t.Fatalf("the capacity of a connected runner has to be readable: %+v", view[0].Capacity)
	}
	if !strings.Contains(view[0].Capacity.WorkDir, dir) {
		t.Errorf("the capacity has to be about the working directory: %q", view[0].Capacity.WorkDir)
	}

	// A running sandbox shows up as load.
	agent := s.newSupportAgent("view-agent")
	if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID}); err != nil {
		t.Fatal(err)
	}
	resp = c.do(http.MethodGet, "/api/v1/runners", nil)
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if view[0].Capacity == nil || view[0].Capacity.TotalBytes == 0 {
		t.Fatal("capacity is missing after the start")
	}
	if view[0].Live.Sandboxes != 1 {
		t.Errorf("the running sandbox has to show up as load: %d", view[0].Live.Sandboxes)
	}
}

// The figure the agent page is really about: how big the home is, and how much
// of it only this agent holds. The first says what a loss costs in time, the
// second what it costs in work.
func TestHomeViewNamesWhatOnlyThisAgentHolds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, _ := filesStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")

	shared := strings.Repeat("SDK", 20_000) // ~60 kB, identical on both agents
	own := strings.Repeat("nur meins", 5_000)

	write := func(agentID uuid.UUID, rel, content string) {
		t.Helper()
		tree, err := s.orch.AgentFiles(agentID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tree.Write(rel, strings.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}

	a := s.newSupportAgent("home-a")
	b := s.newSupportAgent("home-b")
	write(a.ID, ".pub-cache/paket.tar", shared)
	write(a.ID, "arbeit/ergebnis.md", own)
	write(b.ID, ".pub-cache/paket.tar", shared)
	pool.FlushHomes(ctx)

	var view struct {
		Enabled        bool  `json:"enabled"`
		TotalBytes     int64 `json:"total_bytes"`
		ExclusiveBytes int64 `json:"exclusive_bytes"`
		TopDirs        []struct {
			Path  string `json:"path"`
			Bytes int64  `json:"bytes"`
		} `json:"top_dirs"`
		RunnerKind string `json:"runner_kind"`
		Latest     *struct {
			BlocksUp int `json:"blocks_up"`
		} `json:"latest"`
	}
	resp := c.do(http.MethodGet, "/api/v1/agents/"+a.ID.String()+"/home", nil)
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !view.Enabled || view.Latest == nil {
		t.Fatalf("the home view is empty: %+v", view)
	}
	if view.TotalBytes < int64(len(shared)+len(own)) {
		t.Errorf("the total size is too small: %d", view.TotalBytes)
	}
	// The toolchain both agents hold is NOT exclusive; the own work is.
	if view.ExclusiveBytes >= view.TotalBytes {
		t.Errorf("what both hold must not count as exclusive: %d of %d",
			view.ExclusiveBytes, view.TotalBytes)
	}
	if view.ExclusiveBytes < int64(len(own)) {
		t.Errorf("the agent's own work has to count as exclusive: %d", view.ExclusiveBytes)
	}
	if view.RunnerKind != "builtin" {
		t.Errorf("the view has to say where the working copy sits: %q", view.RunnerKind)
	}
	// "Why is this home so big?" — answered without shell access.
	var found bool
	for _, d := range view.TopDirs {
		if d.Path == ".pub-cache" && d.Bytes >= int64(len(shared)) {
			found = true
		}
	}
	if !found {
		t.Errorf("the largest directories are missing or wrong: %+v", view.TopDirs)
	}
}

// Backing up on demand, and the promise that goes with it: there is exactly one
// state per agent, so a second backup replaces the first rather than adding to
// it (spec/16). The database holds that promise; this checks that the path
// through the interface honours it too.
func TestBackupNowReplacesTheOneState(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, _ := filesStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("rollback-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("bericht.md", strings.NewReader("erste Fassung")); err != nil {
		t.Fatal(err)
	}
	// "Back up now" — before a maintenance window, or simply because somebody
	// wants the current state safe.
	resp := c.do(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/home/snapshots", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("back up now: %s", resp.Status)
	}
	first, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil || first.ManifestHash == "" {
		t.Fatalf("no snapshot after backing up: %v", err)
	}
	if first.Reason != "manual" {
		t.Errorf("the snapshot has to say what asked for it: %q", first.Reason)
	}

	// The work goes on, and the next sync replaces that state.
	if _, err := tree.Write("bericht.md", strings.NewReader("zweite Fassung")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)

	latest, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ManifestHash == first.ManifestHash {
		t.Error("the second sync did not become the agent's state")
	}
	// The decisive part, and the reason the constraint sits in the database: one
	// row, not two. A second one would leave the sweep guessing which of them is
	// the home.
	var rows int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM home_snapshots WHERE agent_id = $1`, agent.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("exactly one state per agent, found %d", rows)
	}
}

// Retention and cleanup. The preview has to name the space ACTUALLY freed: a
// block belongs to no single snapshot, so the sum of the snapshot sizes would
// be a number that is never right.
func TestCleanupFreesOnlyWhatNothingElseNeeds(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, blobs, _ := filesStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("retention-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Three states: one shared file that survives throughout, and one that
	// only the middle snapshot holds.
	if _, err := tree.Write("bleibt.md", strings.NewReader("überlebt alles")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)
	if _, err := tree.Write("nur-mittendrin.md", strings.NewReader(strings.Repeat("x", 50_000))); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)
	if err := tree.Remove("nur-mittendrin.md"); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)

	// Three syncs, one state: each replaced the one before it, and the blocks
	// only the middle one held became garbage the moment it was replaced.
	var rows int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM home_snapshots WHERE agent_id = $1`, agent.ID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("exactly one state per agent, found %d", rows)
	}

	alleBloecke, err := blobs.List(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}

	var preview struct {
		BlocksRemoved int   `json:"blocks_removed"`
		FreedBytes    int64 `json:"freed_bytes"`
		Preview       bool  `json:"preview"`
	}
	resp := c.do(http.MethodPost, "/api/v1/platform/home-store/cleanup?preview=true", map[string]any{})
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !preview.Preview || preview.BlocksRemoved == 0 {
		t.Fatalf("the preview has to name what would fall away: %+v", preview)
	}
	// The 50 kB that only the middle snapshot holds are freed; the file that
	// survives throughout is not.
	if preview.FreedBytes < 50_000 {
		t.Errorf("the preview understates what would be freed: %d", preview.FreedBytes)
	}

	// And nothing has happened yet.
	if before, err := blobs.List(ctx, s.orgID); err != nil || len(before) != len(alleBloecke) {
		t.Fatalf("a preview must not delete anything: %d, %v", len(before), err)
	}

	resp = c.do(http.MethodPost, "/api/v1/platform/home-store/cleanup?preview=false", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cleanup: %s", resp.Status)
	}
	after, err := blobs.List(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(alleBloecke) {
		t.Errorf("nothing was swept: %d blocks before, %d after", len(alleBloecke), len(after))
	}

	// The decisive part: the surviving snapshot is still complete. A cleanup
	// that swept away a block another snapshot still needed would show up
	// exactly here — and nowhere else until somebody needed the home.
	surviving, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := homestore.Load(ctx, blobs, s.orgID, surviving.ManifestHash)
	if err != nil {
		t.Fatalf("the surviving snapshot is unreadable: %v", err)
	}
	restore := t.TempDir()
	if _, err := homestore.Materialize(ctx, blobs, s.orgID, restore, m); err != nil {
		t.Fatalf("the surviving snapshot is incomplete: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(restore, "bleibt.md")); err != nil || string(got) != "überlebt alles" {
		t.Errorf("the shared block was swept away: %q, %v", got, err)
	}
}

// A deleted agent takes its snapshot rows with it (ON DELETE CASCADE) and
// leaves its blocks behind. Retention then has nothing left to catch — and a
// cleanup that stopped there could never reclaim them: the store would keep
// growing with no setting that explains it, until a deploy dies on a full
// disk. This is the case the periodic pass exists for.
func TestCleanupReclaimsWhatADeletedAgentLeftBehind(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, blobs, _ := filesStack(t, dir)
	agent := s.newSupportAgent("wegwerf-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("gross.bin", strings.NewReader(strings.Repeat("y", 80_000))); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)

	before, err := blobs.List(ctx, s.orgID)
	if err != nil || len(before) == 0 {
		t.Fatalf("the snapshot left no blocks behind: %d, %v", len(before), err)
	}

	if err := s.registry.Delete(ctx, s.orgID, agent.ID); err != nil {
		t.Fatal(err)
	}
	if left, err := s.runners.LatestSnapshot(ctx, agent.ID); err != nil || left.ManifestHash != "" {
		t.Fatalf("the row should have gone with the agent: %q, %v", left.ManifestHash, err)
	}

	res, err := s.runners.CleanupOrg(ctx, blobs, s.orgID, false)
	if err != nil {
		t.Fatal(err)
	}
	if res.BlocksRemoved == 0 {
		t.Error("the orphaned blocks stayed behind — the sweep never ran")
	}
	after, err := blobs.List(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(before) {
		t.Errorf("nothing was reclaimed: %d blocks before, %d after", len(before), len(after))
	}
}

// The store's fill level belongs on the dashboard — a warning before the disk
// runs short, not after.
func TestStoreViewReportsTheFillLevel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, _ := filesStack(t, dir)
	c := login(t, s, "admin@test.local", "admin-passwort")
	agent := s.newSupportAgent("store-view-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("inhalt.bin", strings.NewReader(strings.Repeat("y", 30_000))); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)

	var view struct {
		Enabled bool  `json:"enabled"`
		Bytes   int64 `json:"bytes"`
		Agents  int   `json:"agents"`
	}
	resp := c.do(http.MethodGet, "/api/v1/platform/home-store", nil)
	if err := json.NewDecoder(resp.Body).Decode(&view); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if !view.Enabled {
		t.Fatal("the home store is switched on in this stack")
	}
	if view.Bytes < 30_000 {
		t.Errorf("the fill level is too small: %d", view.Bytes)
	}
	// One row per agent, so this count is both at once: how many agents have a
	// home in the store, and how many states it holds.
	if view.Agents == 0 {
		t.Errorf("no agent with a home: %+v", view)
	}
}

// The platform diagnostics: the same checks `covey doctor` runs, and the same
// lint `covey config lint` runs, in the browser. Both existed only as
// subcommands — that is, only for whoever has a shell on the host — and the
// agent config lint had already learned that a check nobody runs is one that
// effectively does not exist.
func TestPlatformDiagnosticsAnswerInTheBrowser(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	c := login(t, s, "admin@test.local", "admin-passwort")

	// Without the process configuration the question is left unasked rather
	// than answered by guessing.
	resp := c.do(http.MethodGet, "/api/v1/platform/doctor", nil)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("without a configuration: %s", resp.Status)
	}

	s.srv.Config = &config.Config{
		DatabaseURL:   "unused — the pool is handed in",
		DataDir:       t.TempDir(),
		SandboxImage:  "covey-sandbox:test",
		SandboxImages: map[string]string{"base": "covey-sandbox:test", "dev": "covey-sandbox-dev:test"},
		HomeStore:     true,
		BlobStore:     "builtin",
	}
	agent := s.newSupportAgent("doctor-agent")
	if _, err := s.pool.Exec(ctx, "UPDATE agents SET sandbox_image='dev' WHERE id=$1", agent.ID); err != nil {
		t.Fatal(err)
	}

	var report struct {
		Blocking int `json:"blocking"`
		Findings []struct {
			OK       bool   `json:"ok"`
			Blocking bool   `json:"blocking"`
			What     string `json:"what"`
			Detail   string `json:"detail"`
			Remedy   string `json:"remedy"`
		} `json:"findings"`
	}
	resp = c.do(http.MethodGet, "/api/v1/platform/doctor", nil)
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	by := map[string]string{}
	for _, f := range report.Findings {
		by[f.What] = f.Detail
	}
	if by["migrations"] != "up to date" {
		t.Errorf("the test database is migrated: %q", by["migrations"])
	}
	// The one that matters at an upgrade: the image an agent is pointed at is
	// named, with how many wait on it — and with the remedy.
	var missing bool
	for _, f := range report.Findings {
		if strings.Contains(f.What, "covey-sandbox-dev:test") {
			missing = true
			if !f.Blocking {
				t.Error("a missing image keeps agents from working")
			}
			if !strings.Contains(f.Detail, "1 agent") {
				t.Errorf("the number of waiting agents is missing: %q", f.Detail)
			}
			if !strings.Contains(f.Remedy, "make sandbox-image-dev") {
				t.Errorf("the remedy has to name the right target: %q", f.Remedy)
			}
		}
	}
	// Only meaningful where Docker answers at all; without it the check reports
	// the daemon instead, which is the honest order.
	if !missing && by["docker"] != "" {
		t.Errorf("the image in use was not checked: %+v", by)
	}
	// The home store's backup obligation is named rather than assumed known.
	if !strings.Contains(by["home store"], "blocks") {
		t.Errorf("the home store is missing from the report: %q", by["home store"])
	}

	// And the org-wide lint, which used to be reachable only from a shell.
	resp = c.do(http.MethodGet, "/api/v1/platform/lint", nil)
	var lint []struct {
		Slug     string `json:"slug"`
		Findings []struct {
			Rule string `json:"rule"`
		} `json:"findings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&lint); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	for _, a := range lint {
		if len(a.Findings) == 0 {
			t.Errorf("an agent without findings must not be in the list: %s", a.Slug)
		}
	}
}

// The workplaces come from the catalogue (internal/sandbox) and no longer from
// a list the interface keeps of its own. The point of that is nothing one sees:
// the third profile appears everywhere, instead of in three of four places.
//
// What is worth testing about it is the part that is not the catalogue — the
// instance's image per profile, and how many of THIS organisation's agents work
// in it.
func TestWorkplacesComeFromTheCatalogue(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()
	c := login(t, s, "admin@test.local", "admin-passwort")

	// Was die Instanz ausdruecklich benannt hat — die eine der drei Quellen,
	// die ein Mensch gesetzt hat, und deshalb die, die gewinnt (spec/16).
	s.srv.Config = &config.Config{
		SandboxImage:    "covey-sandbox:test",
		SandboxImageEnv: map[string]string{"base": "covey-sandbox:test", "dev": "eigenes-dev:2026"},
	}
	agent := s.newSupportAgent("workplace-agent")
	if _, err := s.pool.Exec(ctx, "UPDATE agents SET sandbox_image='dev' WHERE id=$1", agent.ID); err != nil {
		t.Fatal(err)
	}

	var list []struct {
		Name      string `json:"name"`
		Image     string `json:"image"`
		Build     string `json:"build"`
		Source    string `json:"source"`
		Default   bool   `json:"default"`
		Available *bool  `json:"available"`
		Agents    []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"agents"`
	}
	resp := c.do(http.MethodGet, "/api/v1/workplaces", nil)
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		t.Fatalf("workplaces: %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if len(list) < 2 {
		t.Fatalf("the catalogue has to carry at least base and dev: %+v", list)
	}
	byName := map[string]int{}
	for i, w := range list {
		byName[w.Name] = i
	}
	dev, ok := byName["dev"]
	if !ok {
		t.Fatalf("no dev profile in %+v", list)
	}
	if list[dev].Image != "eigenes-dev:2026" {
		t.Errorf("the instance's image did not come through: %q", list[dev].Image)
	}
	// Und woher es kommt, steht dabei: sonst muesste jemand zwischen
	// Umgebung, Katalog und Voreinstellung raten, wenn ein Image nicht das
	// ist, was er erwartet hat.
	if list[dev].Source != "env" {
		t.Errorf("dev source = %q, expected env", list[dev].Source)
	}
	if list[dev].Build == "" {
		t.Error("a profile without a build command leaves whoever reads it looking")
	}
	// Benannt statt gezählt: Wer einen Arbeitsplatz ändern oder löschen will,
	// fragt nicht nach der Anzahl, sondern danach, wen es angeht.
	if len(list[dev].Agents) != 1 {
		t.Errorf("dev: %d agents, expected 1", len(list[dev].Agents))
	}
	// Nobody could be asked here — and that is not the same as "not there".
	// Shown as unavailable, the interface would advise a build that has already
	// happened.
	if list[dev].Available != nil {
		t.Errorf("without a runner nothing may be claimed about the image: %v", *list[dev].Available)
	}
}
