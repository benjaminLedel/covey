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
		Snapshots      int   `json:"snapshots"`
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

	if !view.Enabled || view.Snapshots == 0 || view.Latest == nil {
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

// Backing up on demand and rolling back — the rollback falls out of the
// construction anyway. Restoring is a modifying action on somebody else's work,
// so it is only allowed while the agent is asleep.
func TestBackupAndRestoreThroughTheInterface(t *testing.T) {
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

	// The work goes on, and a second state follows.
	if _, err := tree.Write("bericht.md", strings.NewReader("zweite Fassung")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)

	// Rolling back to the first state.
	resp = c.do(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/home/restore",
		map[string]string{"snapshot": first.ID.String()})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("restore: %s", resp.Status)
	}
	home := filepath.Join(dir, "work", "homes", agent.ID.String(), "bericht.md")
	got, err := os.ReadFile(home)
	if err != nil || string(got) != "erste Fassung" {
		t.Fatalf("the rollback did not reach the working copy: %q, %v", got, err)
	}
	// And it holds: the restored state became the current one, so the next
	// wake does not materialise the newer snapshot over it.
	latest, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if latest.ManifestHash != first.ManifestHash {
		t.Errorf("after a rollback the restored state has to be the current one")
	}

	// While the agent is working, restoring is refused — otherwise the running
	// sandbox writes into a home that changes underneath it.
	if err := s.registry.SetStatus(ctx, agent.ID, "working"); err != nil {
		t.Fatal(err)
	}
	resp = c.do(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/home/restore",
		map[string]string{"snapshot": first.ID.String()})
	resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Errorf("restoring a running agent's home has to be refused, got %s", resp.Status)
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

	list, err := s.runners.ListSnapshots(ctx, agent.ID, 10)
	if err != nil || len(list) < 3 {
		t.Fatalf("three snapshots expected: %d, %v", len(list), err)
	}

	// Keep only the most recent one.
	resp := c.do(http.MethodPatch, "/api/v1/platform/home-store",
		map[string]int{"keep_per_agent": 1, "max_age_days": 0})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("retention: %s", resp.Status)
	}

	var preview struct {
		Snapshots     int   `json:"snapshots"`
		BlocksRemoved int   `json:"blocks_removed"`
		FreedBytes    int64 `json:"freed_bytes"`
		Preview       bool  `json:"preview"`
	}
	resp = c.do(http.MethodPost, "/api/v1/platform/home-store/cleanup?preview=true", map[string]any{})
	if err := json.NewDecoder(resp.Body).Decode(&preview); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !preview.Preview || preview.Snapshots != len(list)-1 {
		t.Fatalf("the preview has to name what would fall away: %+v", preview)
	}
	// The 50 kB that only the middle snapshot holds are freed; the file that
	// survives throughout is not.
	if preview.FreedBytes < 50_000 {
		t.Errorf("the preview understates what would be freed: %d", preview.FreedBytes)
	}

	// And nothing has happened yet.
	if again, _ := s.runners.ListSnapshots(ctx, agent.ID, 10); len(again) != len(list) {
		t.Fatal("a preview must not delete anything")
	}

	resp = c.do(http.MethodPost, "/api/v1/platform/home-store/cleanup?preview=false", map[string]any{})
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("cleanup: %s", resp.Status)
	}
	after, err := s.runners.ListSnapshots(ctx, agent.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 {
		t.Errorf("the most recent snapshot has to survive, and only it: %d", len(after))
	}

	// The decisive part: the surviving snapshot is still complete. A cleanup
	// that swept away a block another snapshot still needed would show up
	// exactly here — and nowhere else until somebody needed the home.
	m, err := homestore.Load(ctx, blobs, s.orgID, after[0].ManifestHash)
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
		Enabled      bool  `json:"enabled"`
		Bytes        int64 `json:"bytes"`
		Snapshots    int   `json:"snapshots"`
		Agents       int   `json:"agents"`
		KeepPerAgent int   `json:"keep_per_agent"`
		MaxAgeDays   int   `json:"max_age_days"`
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
	if view.Snapshots == 0 || view.Agents == 0 {
		t.Errorf("snapshots/agents missing: %+v", view)
	}
	// The defaults from the migration, so the page shows a rule and not zeroes.
	if view.KeepPerAgent != 10 || view.MaxAgeDays != 30 {
		t.Errorf("the retention defaults are wrong: %+v", view)
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

	s.srv.Config = &config.Config{
		SandboxImage:  "covey-sandbox:test",
		SandboxImages: map[string]string{"base": "covey-sandbox:test", "dev": "eigenes-dev:2026"},
	}
	agent := s.newSupportAgent("workplace-agent")
	if _, err := s.pool.Exec(ctx, "UPDATE agents SET sandbox_image='dev' WHERE id=$1", agent.ID); err != nil {
		t.Fatal(err)
	}

	var list []struct {
		Name      string `json:"name"`
		Image     string `json:"image"`
		Build     string `json:"build"`
		Default   bool   `json:"default"`
		Available *bool  `json:"available"`
		InUse     int    `json:"in_use"`
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
	if list[dev].Build == "" {
		t.Error("a profile without a build command leaves whoever reads it looking")
	}
	if list[dev].InUse != 1 {
		t.Errorf("dev: %d agents, expected 1", list[dev].InUse)
	}
	// Nobody could be asked here — and that is not the same as "not there".
	// Shown as unavailable, the interface would advise a build that has already
	// happened.
	if list[dev].Available != nil {
		t.Errorf("without a runner nothing may be claimed about the image: %v", *list[dev].Available)
	}
}
