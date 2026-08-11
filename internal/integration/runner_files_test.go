package integration

import (
	"archive/zip"
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/orchestrator"
	"covey/internal/runner"
	"covey/internal/sandboxfs"
)

// The file browser has to work when the home is not on this machine. And what
// it writes has to reach the home store — otherwise an upload lives only in the
// runner's working copy, the agent wakes on another host, materialises the last
// snapshot, and the file is gone without anyone having deleted it.
//
// That window is what these tests are about. The browsing itself is the smaller
// half.

// filesStack brings up a stack whose data plane is a runner with a faked
// Docker, and whose home store is real.
func filesStack(t *testing.T, dir string) (*stack, *runner.Pool, *runner.Node, homestore.BlobStore, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}

	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.DefaultImage = "covey-sandbox:test"
			pool.StartTimeout = 20 * time.Second
			pool.Blobs = blobs
			return pool
		},
	})
	s.setRunnerPool(pool, blobs)

	rn, err := s.runners.EnsureBuiltin(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	pool.HomeInfo = func(ctx context.Context, agentID uuid.UUID) (uuid.UUID, uuid.UUID, string, error) {
		snap, _ := s.runners.LatestSnapshot(ctx, agentID)
		last := uuid.Nil
		if snap.RunnerID != nil {
			last = *snap.RunnerID
		}
		return s.orgID, last, snap.ManifestHash, nil
	}
	pool.SnapshotTaken = func(ctx context.Context, agentID, runnerID uuid.UUID, res runner.HomeSynced) error {
		_, err := s.runners.RecordSnapshot(ctx, s.orgID, agentID, &runnerID,
			res.ManifestHash, res.TotalSize, res.Blocks, res.BytesUp, "file-browser")
		return err
	}

	node := runner.NewNode(rn.ID, s.orgID, &runner.Docker{
		RunnerID: rn.ID, Image: "covey-sandbox:test",
		DataDir: filepath.Join(dir, "work"), DockerBin: fakeDocker(t, dir),
	}, slog.Default())
	node.Blobs = blobs
	runnerCtx, stopRunner := context.WithCancel(ctx)
	t.Cleanup(stopRunner)
	s.stopRunner = stopRunner
	if err := pool.AttachLocal(runnerCtx, node); err != nil {
		t.Fatal(err)
	}
	return s, pool, node, blobs, rn.ID
}

func TestFileBrowserReachesAHomeThroughTheRunnerLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	s, _, _, _, _ := filesStack(t, dir)
	agent := s.newSupportAgent("files-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatalf("the home is not reachable: %v", err)
	}

	// Writing, listing, reading — all of it over the protocol, none of it
	// against a path this process knows.
	if _, err := tree.Write("notizen/plan.md", strings.NewReader("# Plan\n\nErster Schritt.")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	listing, err := tree.List("notizen")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "plan.md" {
		t.Fatalf("the listing does not show the file: %+v", listing.Entries)
	}
	file, err := tree.Read("notizen/plan.md")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if !strings.Contains(file.Content, "Erster Schritt") {
		t.Errorf("the content came back wrong: %q", file.Content)
	}

	// The errors have to survive the journey: a mistyped path is a 404 and not
	// a 500, and that distinction lives in a sentinel that has to be rebuilt on
	// this side.
	if _, err := tree.Read("gibtesnicht.md"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Errorf("a missing path has to come back as ErrNotFound: %v", err)
	}

	// Download: streamed in chunks over the link, so a large file need not fit
	// into one message.
	big := strings.Repeat("Zeile mit Inhalt\n", 100_000) // ~1.7 MB, several chunks
	if _, err := tree.Write("gross.log", strings.NewReader(big)); err != nil {
		t.Fatal(err)
	}
	rc, info, err := tree.Open("gross.log")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	if info.Size != int64(len(big)) {
		t.Errorf("the size has to be known before the first byte: %d", info.Size)
	}
	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading the stream: %v", err)
	}
	if string(got) != big {
		t.Errorf("the download came back changed (%d of %d bytes)", len(got), len(big))
	}

	// An archive is measured first and streamed afterwards — "too large" has to
	// be a status, not a download that breaks off.
	plan, err := tree.PlanZip([]string{"notizen"})
	if err != nil {
		t.Fatalf("PlanZip: %v", err)
	}
	if plan.Files != 1 {
		t.Errorf("the plan has to name the extent: %+v", plan)
	}
	var archive bytes.Buffer
	if err := tree.WriteZip(&archive, plan); err != nil {
		t.Fatalf("WriteZip: %v", err)
	}
	zr, err := zip.NewReader(bytes.NewReader(archive.Bytes()), int64(archive.Len()))
	if err != nil {
		t.Fatalf("the archive is not readable: %v", err)
	}
	var inArchive []string
	for _, f := range zr.File {
		inArchive = append(inArchive, f.Name)
	}
	// The folder goes in with its content, named relative to its parent —
	// whoever selects "notizen" gets a folder in the archive and not its
	// spilled-out parts.
	if !strings.Contains(strings.Join(inArchive, " "), "notizen/plan.md") {
		t.Errorf("the archive does not contain the file: %v", inArchive)
	}

	// Move and remove, and the usage figure the page shows.
	if _, err := tree.Move("notizen/plan.md", "notizen/plan-v2.md"); err != nil {
		t.Fatalf("Move: %v", err)
	}
	if err := tree.Remove("gross.log"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if usage := tree.Usage(); !usage.Exists {
		t.Error("the usage figure has to know the home")
	}
}

// The point of the whole thing: what the browser writes has to reach the store.
// Between the write and the next sync there is a window in which the agent can
// wake on another runner, materialise the last snapshot — and the upload is
// gone, with no deletion anywhere to explain it.
func TestBrowserChangesReachTheHomeStore(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, blobs, _ := filesStack(t, dir)
	agent := s.newSupportAgent("sync-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("uebergabe.md", strings.NewReader("von Hand hochgeladen")); err != nil {
		t.Fatal(err)
	}

	// The sync follows after a settling period — a fifty-file upload has to
	// produce one sync and not fifty.
	waitFor(t, "the change has been synced", 15*time.Second, func() bool {
		snap, err := s.runners.LatestSnapshot(ctx, agent.ID)
		return err == nil && snap.ManifestHash != ""
	})
	snap, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := homestore.Load(ctx, blobs, s.orgID, snap.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, e := range m.Entries {
		if e.Path == "uebergabe.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("what the browser wrote is missing from the snapshot: %+v", m.Entries)
	}

	// And the harder half: a change that the settling period has NOT yet
	// carried out must not be overwritten by the next start. The start
	// materialises the last snapshot over the working copy, so it has to sync
	// first.
	if _, err := tree.Write("gerade-eben.md", strings.NewReader("kurz vor dem Wecken")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID}); err != nil {
		t.Fatal(err)
	}
	after, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	m2, err := homestore.Load(ctx, blobs, s.orgID, after.ManifestHash)
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, e := range m2.Entries {
		if e.Path == "gerade-eben.md" {
			found = true
		}
	}
	if !found {
		t.Error("a change shortly before the wake has to be synced before the home is materialised")
	}
	// And it is really still on disk afterwards — the materialise must not have
	// removed it as "unknown to the snapshot".
	home := filepath.Join(dir, "work", "homes", agent.ID.String(), "gerade-eben.md")
	if _, err := os.Stat(home); err != nil {
		t.Errorf("the file is gone from the working copy: %v", err)
	}
}

// When the runner is not connected, the home is still readable — from its last
// snapshot. That is exactly the moment somebody needs it: looking at the work
// of an agent whose host is down. Writing into it is refused, because a change
// to a snapshot is a second state beside the working copy that is coming back.
func TestOfflineRunnerLeavesTheHomeReadable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, _ := filesStack(t, dir)
	agent := s.newSupportAgent("offline-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("ergebnis/bericht.md", strings.NewReader("# Bericht\n\nFertig.")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)
	if snap, err := s.runners.LatestSnapshot(ctx, agent.ID); err != nil || snap.ManifestHash == "" {
		t.Fatalf("no snapshot to read from: %v", err)
	}

	// The runner goes away — a host that is down, a maintenance window.
	s.cancelRunner()
	waitFor(t, "the runner is disconnected", 10*time.Second, func() bool {
		tree, err := s.orch.AgentFiles(agent.ID)
		if err != nil {
			return false
		}
		_, err = tree.Write("egal.md", strings.NewReader("x"))
		var readOnly *sandboxfs.ReadOnlyError
		return errorsAs(err, &readOnly)
	})

	tree, err = s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatalf("an offline runner must not make the home unreachable: %v", err)
	}
	listing, err := tree.List("ergebnis")
	if err != nil {
		t.Fatalf("List from the snapshot: %v", err)
	}
	if len(listing.Entries) != 1 || listing.Entries[0].Name != "bericht.md" {
		t.Errorf("the snapshot listing is wrong: %+v", listing.Entries)
	}
	file, err := tree.Read("ergebnis/bericht.md")
	if err != nil {
		t.Fatalf("Read from the snapshot: %v", err)
	}
	if !strings.Contains(file.Content, "Fertig") {
		t.Errorf("the content from the snapshot is wrong: %q", file.Content)
	}

	// Read-only, and it says why.
	_, err = tree.Mkdir("neuer-ordner")
	var readOnly *sandboxfs.ReadOnlyError
	if !errorsAs(err, &readOnly) {
		t.Fatalf("writing into a snapshot has to be refused: %v", err)
	}
	if !strings.Contains(readOnly.Reason, "runner") {
		t.Errorf("the refusal has to say why: %q", readOnly.Reason)
	}
}

// The HTTP layer has to turn that refusal into a status somebody can act on.
func TestReadOnlyHomeAnswersWithConflict(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _, _, _ := filesStack(t, dir)
	agent := s.newSupportAgent("http-offline-agent")

	tree, err := s.orch.AgentFiles(agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tree.Write("da.md", strings.NewReader("inhalt")); err != nil {
		t.Fatal(err)
	}
	pool.FlushHomes(ctx)
	s.cancelRunner()

	c := login(t, s, "admin@test.local", "admin-passwort")
	waitFor(t, "the write comes back as a conflict", 10*time.Second, func() bool {
		resp := c.do(http.MethodPost, "/api/v1/agents/"+agent.ID.String()+"/files/dir",
			map[string]string{"path": "neu"})
		resp.Body.Close()
		return resp.StatusCode == http.StatusConflict
	})

	// Reading keeps working, over the same endpoints as always.
	resp := c.do(http.MethodGet, "/api/v1/agents/"+agent.ID.String()+"/files?path=", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the listing has to keep working: %s", resp.Status)
	}
	body := new(bytes.Buffer)
	_, _ = body.ReadFrom(resp.Body)
	if !strings.Contains(body.String(), "da.md") {
		t.Errorf("the file from the snapshot is missing from the listing: %s", body.String())
	}
}
