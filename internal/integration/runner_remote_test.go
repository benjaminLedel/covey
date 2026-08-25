package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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
	runnerstore "covey/internal/runner/store"
	"covey/internal/sandbox"
)

// The remote runner is the same implementation as the built-in one with a
// different transport — that is the claim the seam makes, and this is where it
// is checked against a real WebSocket, a real registration and a real block
// transfer through the runner API.
//
// It also checks the two things that are easy to get wrong and expensive to
// notice late: a runner may only ever serve its own organisation, and it must
// never be able to name an identity other than its token's.

func remoteStack(t *testing.T, dir string) (*stack, *runner.Pool, homestore.BlobStore) {
	t.Helper()
	blobs, err := homestore.NewDir(filepath.Join(dir, "blocks"))
	if err != nil {
		t.Fatal(err)
	}
	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
			pool.StartTimeout = 20 * time.Second
			return pool
		},
	})
	s.setRunnerPool(pool, blobs)
	return s, pool, blobs
}

// connectRemoteRunner registers a host and lets it connect, with the image
// claim it is to make. Factored out because the interesting tests are the ones
// where a registered runner exists and does NOT fit.
func registerRemoteRunner(t *testing.T, s *stack) (uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	regToken, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "Build host", nil)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{
		"token": regToken, "description": "Build host", "version": "test", "arch": runtime.GOARCH,
	})
	resp, err := http.Post(s.http.URL+"/api/runner/v1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registration: %s", resp.Status)
	}
	var reg struct {
		RunnerID uuid.UUID `json:"runner_id"`
		OrgID    uuid.UUID `json:"org_id"`
		Token    string    `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	return reg.RunnerID, reg.Token
}

// connectRemoteRunner registers a host AND lets it connect, with the image
// claim it is to make.
func connectRemoteRunner(t *testing.T, s *stack, pool *runner.Pool, dir string, images []string) uuid.UUID {
	t.Helper()
	runnerID, token := registerRemoteRunner(t, s)
	node := runner.NewNode(runnerID, s.orgID, &runner.Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test",
		DataDir: filepath.Join(dir, "remote-work"), DockerBin: fakeDocker(t, dir),
	}, slog.Default())
	node.Images = images
	t.Cleanup(node.Close)
	runCtx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = runner.RunNode(runCtx, node, s.http.URL, token, 200*time.Millisecond) }()
	waitFor(t, "the remote runner has connected", 15*time.Second, func() bool {
		for _, l := range pool.LiveFor(s.orgID) {
			if l.Connected {
				return true
			}
		}
		return false
	})
	return runnerID
}

// The outage this test exists for: a GPU host registered, the control plane
// restarted, and from then on every wake failed with "no runner holds the
// image" — the host claimed covey-sandbox:latest, the agents needed the deploy
// image, and nobody was a candidate.
//
// The answer is not a cleverer fallback, it is that the claim never excluded
// anybody in the first place: docker run fetches an image the host does not
// have, so a registered runner carries a workplace it never claimed. Only tags
// exclude, because only a tag says what a host IS.
func TestARegisteredRunnerCarriesAWorkplaceItDidNotClaim(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _ := remoteStack(t, dir)

	// callCtx is the wake's, ctx is the process's — AttachLocal binds the
	// runner's life to what it is given, and bound to the wake the built-in one
	// exists for a single run.
	ensured := 0
	pool.EnsureLocal = func(callCtx context.Context, orgID uuid.UUID) error {
		ensured++
		remote, err := s.runners.HasRemote(callCtx, orgID)
		if err != nil {
			return err
		}
		if !runner.BuiltinAllowed("auto", remote) {
			return errors.New("the built-in runner is switched off for this organisation")
		}
		id := uuid.New()
		return pool.AttachLocal(ctx, runner.NewNode(id, orgID, &runner.Docker{
			RunnerID: id, Image: "covey-sandbox:test",
			DataDir: filepath.Join(dir, "builtin-work"), DockerBin: fakeDocker(t, dir),
		}, slog.Default()))
	}

	connectRemoteRunner(t, s, pool, dir, []string{"covey-sandbox:test"})
	agent := s.newSupportAgent("agent-with-its-own-workplace")

	sb, err := pool.Start(ctx, orchestrator.SandboxSpec{
		AgentID: agent.ID, OrgID: s.orgID, Image: "registry.example.com/team/dev:1",
	})
	if err != nil {
		t.Fatalf("the registered runner should have carried it: %v", err)
	}
	if err := sb.Stop(ctx); err != nil {
		t.Fatal(err)
	}
	// And the control plane stayed out of it: the host is there, it takes the
	// work, and nothing had to step in.
	if ensured != 0 {
		t.Errorf("the built-in runner was brought up although a host was connected (%d×)", ensured)
	}
}

// What is left of the built-in runner's job: a registered host that is not
// connected — a maintenance window, a reboot, a dead network. Then the
// organisation has a runner on paper and none in fact, and the control plane
// carries the work unless somebody has said it must not.
func TestBuiltinRunnerOffRefusesWhenNothingIsConnected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _ := remoteStack(t, dir)

	var asked int
	mode := runner.BuiltinModeOff
	pool.EnsureLocal = func(callCtx context.Context, orgID uuid.UUID) error {
		asked++
		remote, err := s.runners.HasRemote(callCtx, orgID)
		if err != nil {
			return err
		}
		if !runner.BuiltinAllowed(mode, remote) {
			return errors.New("the built-in runner is switched off for this organisation")
		}
		id := uuid.New()
		return pool.AttachLocal(ctx, runner.NewNode(id, orgID, &runner.Docker{
			RunnerID: id, Image: "covey-sandbox:test",
			DataDir: filepath.Join(dir, "builtin-work"), DockerBin: fakeDocker(t, dir),
		}, slog.Default()))
	}

	// Registered, never connected.
	registerRemoteRunner(t, s)
	agent := s.newSupportAgent("agent-on-a-strict-instance")

	if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID}); err == nil {
		t.Fatal("COVEY_BUILTIN_RUNNER=off has to refuse")
	}
	if asked == 0 {
		t.Error("the policy was never consulted")
	}

	// The same instance with the default: the control plane carries it rather
	// than leaving the organisation without a data plane.
	mode = "auto"
	if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID}); err != nil {
		t.Fatalf("with the default the built-in runner has to step in: %v", err)
	}
}

func TestRemoteRunnerRegistersAndCarriesSandboxes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, blobs := remoteStack(t, dir)

	// The organisation issues a registration token — the way it happens in the
	// interface.
	regToken, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "Build host Frankfurt", nil)
	if err != nil {
		t.Fatal(err)
	}

	// The host registers. Everything it learns about itself comes from this
	// answer: it inherits its organisation from the token and cannot change it.
	body, _ := json.Marshal(map[string]any{
		"token": regToken, "description": "Build host Frankfurt", "tags": []string{"arm64"},
		"version": "test", "arch": "arm64",
	})
	resp, err := http.Post(s.http.URL+"/api/runner/v1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("registration: %s", resp.Status)
	}
	var reg struct {
		RunnerID uuid.UUID `json:"runner_id"`
		OrgID    uuid.UUID `json:"org_id"`
		Token    string    `json:"token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&reg); err != nil {
		t.Fatal(err)
	}
	if reg.OrgID != s.orgID {
		t.Fatalf("the runner has to inherit its organisation from the token: %s", reg.OrgID)
	}

	// A revoked token must not work a second time.
	if _, err := s.pool.Exec(ctx,
		`UPDATE runner_registration_tokens SET revoked_at = now() WHERE org_id = $1`, s.orgID); err != nil {
		t.Fatal(err)
	}
	again, err := http.Post(s.http.URL+"/api/runner/v1/register", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	again.Body.Close()
	if again.StatusCode != http.StatusUnauthorized {
		t.Errorf("a revoked registration token has to be refused, got %s", again.Status)
	}

	// Now the host itself: Docker faked, blocks through the runner API — a
	// runner never gets the store's credentials.
	homes := filepath.Join(dir, "runner-work")
	node := runner.NewNode(reg.RunnerID, reg.OrgID, &runner.Docker{
		RunnerID: reg.RunnerID, Image: "covey-sandbox:test",
		DataDir: homes, DockerBin: fakeDocker(t, dir),
	}, slog.Default())
	node.Tags = []string{"arm64"}
	t.Cleanup(node.Close)
	node.Blobs = homestore.NewHTTPStore(s.http.URL, reg.Token)

	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = runner.RunNode(runCtx, node, s.http.URL, reg.Token, 200*time.Millisecond) }()

	// The connection comes from the runner: it dials out, the control plane
	// only waits.
	agent := s.newSupportAgent("remote-agent")
	waitFor(t, "the remote runner has connected", 15*time.Second, func() bool {
		_, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID})
		return err == nil
	})

	// The home goes through the block API into the store and comes back out of
	// it — over HTTP, without the runner ever touching the store directly.
	home := filepath.Join(homes, "homes", agent.ID.String())
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "ergebnis.md"), []byte("auf dem fernen Host entstanden"), 0o644); err != nil {
		t.Fatal(err)
	}

	pool.SnapshotTaken = func(ctx context.Context, agentID, runnerID uuid.UUID, res runner.HomeSynced) error {
		_, err := s.runners.RecordSnapshot(ctx, s.orgID, agentID, &runnerID,
			res.ManifestHash, res.TotalSize, res.Blocks, res.BytesUp, "job")
		return err
	}
	sb, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: agent.ID, OrgID: s.orgID})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sb.Stop(ctx); err != nil {
		t.Fatal(err)
	}

	snap, err := s.runners.LatestSnapshot(ctx, agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.ManifestHash == "" {
		t.Fatal("the remote runner's home was not synced")
	}
	// The blocks lie in the control plane's store, put there over HTTP.
	m, err := homestore.Load(ctx, blobs, s.orgID, snap.ManifestHash)
	if err != nil {
		t.Fatalf("the snapshot is not in the store: %v", err)
	}
	var found bool
	for _, e := range m.Entries {
		if e.Path == "ergebnis.md" {
			found = true
		}
	}
	if !found {
		t.Errorf("the work done on the remote host is missing from the snapshot: %+v", m.Entries)
	}
}

// A runner speaks for exactly one organisation, and the identity it claims in
// the handshake has to be the one its token names. Without this check a runner
// could take another's place in the pool — and with it another organisation's
// sandboxes.
func TestRemoteRunnerCannotClaimAForeignIdentity(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, pool, _ := remoteStack(t, dir)

	regToken, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "ehrlicher Host", nil)
	if err != nil {
		t.Fatal(err)
	}
	rn, token, err := s.runners.Register(ctx, regToken, "ehrlicher Host", nil)
	if err != nil {
		t.Fatal(err)
	}

	// The node lies: it reports a foreign runner and a foreign organisation.
	fremd := uuid.New()
	node := runner.NewNode(fremd, uuid.New(), &runner.Docker{RunnerID: fremd, DataDir: dir}, slog.Default())
	transport, err := runner.Dial(ctx, s.http.URL, token)
	if err != nil {
		t.Fatal(err)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() { _ = node.Run(runCtx, transport) }()

	// It must not end up in the pool: a start for the honest runner's
	// organisation finds no candidate.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := pool.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: rn.OrgID}); err == nil {
			t.Fatal("a runner that names a foreign identity must not be taken into the pool")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// A runner reaches its own organisation's blocks and no others'. The block API
// takes the organisation from the token, never from the request.
func TestRunnerBlocksStayInsideTheOrganisation(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, _, blobs := remoteStack(t, dir)

	// A block of a foreign organisation.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Blocks')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	hash := homestore.Hash([]byte("fremder Inhalt"))
	if err := blobs.Put(ctx, fremdeOrg, hash, strings.NewReader("fremder Inhalt")); err != nil {
		t.Fatal(err)
	}

	regToken, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.runners.Register(ctx, regToken, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	store := homestore.NewHTTPStore(s.http.URL, token)

	// It is not there — for this runner the block does not exist, and that is
	// what keeps the existence of a hash from being an oracle across tenants.
	has, err := store.Has(ctx, s.orgID, hash)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("a runner must not see a foreign organisation's block")
	}
	if _, err := store.Get(ctx, s.orgID, hash); err == nil {
		t.Error("a runner must not read a foreign organisation's block")
	}

	// Its own it may store and read back.
	own := homestore.Hash([]byte("eigener Inhalt"))
	if err := store.Put(ctx, s.orgID, own, strings.NewReader("eigener Inhalt")); err != nil {
		t.Fatal(err)
	}
	rc, err := store.Get(ctx, s.orgID, own)
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(rc)
	if buf.String() != "eigener Inhalt" {
		t.Errorf("its own block came back changed: %q", buf.String())
	}
}

// An organisation has the built-in runner exactly as long as it has no
// registered one. The alternative would be a mixed pool — some agents on the
// registered hosts, some on the control plane's machine, and which is which
// decided by a scheduling preference nobody remembers making.
//
// The other half of the rule matters just as much: offline is not "no runner".
// A maintenance window on the only runner must not silently move the whole
// workforce back onto the control plane's host.
func TestBuiltInRunnerStandsDownForARegisteredOne(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	if remote, err := s.runners.HasRemote(ctx, s.orgID); err != nil || remote {
		t.Fatalf("fresh organisation: no registered runner expected (%v, %v)", remote, err)
	}

	regToken, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "eigener Host", nil)
	if err != nil {
		t.Fatal(err)
	}
	rn, _, err := s.runners.Register(ctx, regToken, "eigener Host", nil)
	if err != nil {
		t.Fatal(err)
	}

	remote, err := s.runners.HasRemote(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if !remote {
		t.Fatal("with a registered runner the built-in one has to stand down")
	}

	// Offline is not "gone": the row stays, and with it the rule. Whatever the
	// connection is doing, nothing moves back to the control plane's host.
	if _, err := s.pool.Exec(ctx, `UPDATE runners SET last_seen_at = now() - interval '3 days' WHERE id = $1`, rn.ID); err != nil {
		t.Fatal(err)
	}
	if remote, err := s.runners.HasRemote(ctx, s.orgID); err != nil || !remote {
		t.Error("an offline runner is still a registered runner")
	}

	// Deleting the last one is a deliberate act — and then the built-in one is
	// the answer again, the same rule read forwards.
	if err := s.runners.Delete(ctx, s.orgID, rn.ID); err != nil {
		t.Fatal(err)
	}
	if remote, err := s.runners.HasRemote(ctx, s.orgID); err != nil || remote {
		t.Error("after the last registered runner goes, the built-in one comes back")
	}

	// A foreign organisation's runner never counts for this one.
	fremdeOrg := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Standdown')", fremdeOrg); err != nil {
		t.Fatal(err)
	}
	fremdesToken, err := s.runners.CreateRegistrationToken(ctx, fremdeOrg, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.runners.Register(ctx, fremdesToken, "fremd", nil); err != nil {
		t.Fatal(err)
	}
	if remote, err := s.runners.HasRemote(ctx, s.orgID); err != nil || remote {
		t.Error("a foreign organisation's runner must not make ours stand down")
	}
}

// The built-in runner's token: rolled once per control-plane start, only its
// hash in the database. Two things have to hold — the same organisation gets
// the same token for the life of the process (otherwise the egress proxy would
// be locked out mid-run), and a second process rolls a new one (otherwise it
// would be a long-lived secret at rest for no gain).
func TestBuiltInTokensAreStableWithinAProcessAndFreshAcross(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	first := runnerstore.NewBuiltinTokens(s.runners)
	idA, tokenA, err := first.For(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	idB, tokenB, err := first.For(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if idA != idB || tokenA != tokenB {
		t.Error("within a process the same organisation has to get the same runner and token")
	}
	// And it works: the proxy authenticates with exactly this.
	if rn, err := s.runners.ByToken(ctx, tokenA); err != nil || rn.ID != idA {
		t.Fatalf("the issued token has to be usable: %v", err)
	}

	// A second organisation gets its own — a runner belongs to exactly one.
	other := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Zweit-Org')", other); err != nil {
		t.Fatal(err)
	}
	idC, tokenC, err := first.For(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	if idC == idA || tokenC == tokenA {
		t.Error("two organisations must not share a runner or a token")
	}

	// A restart: new process, new token — and the old one stops working, which
	// is the point of not keeping it at rest.
	second := runnerstore.NewBuiltinTokens(s.runners)
	idD, tokenD, err := second.For(ctx, s.orgID)
	if err != nil {
		t.Fatal(err)
	}
	if idD != idA {
		t.Error("the runner row survives a restart")
	}
	if tokenD == tokenA {
		t.Error("a restart has to roll a new token")
	}
	if _, err := s.runners.ByToken(ctx, tokenA); err == nil {
		t.Error("the old token must not keep working")
	}
}

// A registration token is revocable, and revoking it has to take effect at
// once — it is the credential with which a foreign host joins an organisation.
// Revoked rather than deleted, so that "which token did this runner come in
// on?" stays answerable afterwards.
func TestRegistrationTokenCanBeRevoked(t *testing.T) {
	s := newStack(t)
	ctx := context.Background()

	token, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "einmalig", nil)
	if err != nil {
		t.Fatal(err)
	}
	var id uuid.UUID
	if err := s.pool.QueryRow(ctx,
		`SELECT id FROM runner_registration_tokens WHERE org_id = $1`, s.orgID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.runners.Register(ctx, token, "erster Host", nil); err != nil {
		t.Fatalf("before the revocation it has to work: %v", err)
	}

	if err := s.runners.RevokeRegistrationToken(ctx, s.orgID, id); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.runners.Register(ctx, token, "zweiter Host", nil); !errors.Is(err, runnerstore.ErrTokenInvalid) {
		t.Errorf("after the revocation it must not work: %v", err)
	}
	// The row stays — otherwise the question which token a runner came in on
	// would be unanswerable exactly when somebody asks it.
	var revoked bool
	if err := s.pool.QueryRow(ctx,
		`SELECT revoked_at IS NOT NULL FROM runner_registration_tokens WHERE id = $1`, id).Scan(&revoked); err != nil {
		t.Fatalf("the token row has to stay: %v", err)
	}
	if !revoked {
		t.Error("the token has to be marked as revoked")
	}

	// A foreign organisation cannot revoke ours.
	fremd := uuid.New()
	if _, err := s.pool.Exec(ctx, "INSERT INTO organizations (id, name) VALUES ($1,'Fremd-Revoke')", fremd); err != nil {
		t.Fatal(err)
	}
	zweites, err := s.runners.CreateRegistrationToken(ctx, s.orgID, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var zweitesID uuid.UUID
	if err := s.pool.QueryRow(ctx,
		`SELECT id FROM runner_registration_tokens WHERE org_id = $1 AND revoked_at IS NULL`, s.orgID).Scan(&zweitesID); err != nil {
		t.Fatal(err)
	}
	if err := s.runners.RevokeRegistrationToken(ctx, fremd, zweitesID); err == nil {
		t.Error("a foreign organisation must not revoke our token")
	}
	if _, _, err := s.runners.Register(ctx, zweites, "dritter Host", nil); err != nil {
		t.Errorf("our token has to keep working: %v", err)
	}
}
