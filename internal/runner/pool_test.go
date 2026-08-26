package runner

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/orchestrator"
	"covey/internal/sandbox"
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fakeDockerBin writes a docker stand-in that records its arguments and can be
// told to fail for one subcommand. `wait` blocks until the file `stopped`
// appears — that is how a container that is still running is modelled — and
// deposits its pid in `waitpid`, so a test can ask whether the watcher's child
// process really ended.
func fakeDockerBin(t *testing.T, dir string, failOn string) string {
	t.Helper()
	path := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s ' "$@" >> ` + filepath.Join(dir, "args") + `
printf '\n' >> ` + filepath.Join(dir, "args") + `
if [ "$1" = "` + failOn + `" ]; then echo 'boom' >&2; exit 1; fi
if [ "$1" = "wait" ]; then
  printf '%s' "$$" > ` + filepath.Join(dir, "waitpid") + `
  while [ ! -f ` + filepath.Join(dir, "stopped") + ` ]; do
    # See fakeDocker in internal/integration: if the test binary is killed,
    # nothing else stops this loop, and it polls on forever.
    # Quoted, and exiting NON-zero: an unquoted path with a space makes
    # the test fail, and an exit 0 would then be read as a container that
    # ended by itself - a reported death for a sandbox that is running fine.
    [ -d '` + dir + `' ] || exit 1
    sleep 0.05
  done
  cat ` + filepath.Join(dir, "stopped") + `
  exit 0
fi
echo ok
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// newLocalPool wires a pool with a built-in runner — exactly the way the
// control plane does on a normal installation.
func newLocalPool(t *testing.T, dir, dockerBin string, orgID uuid.UUID) (*Pool, uuid.UUID) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	runnerID := uuid.New()
	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.Profiles = map[string]string{"base": "covey-sandbox:test", "dev": "covey-sandbox-dev:test"}
	p.StartTimeout = 10 * time.Second

	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: dockerBin,
	}, quietLog())
	t.Cleanup(node.Close)
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatalf("built-in runner: %v", err)
	}
	return p, runnerID
}

// The built-in runner is the default path, and it speaks the same protocol as
// one on a foreign host. That is the whole point of the seam: if the in-process
// case took a shortcut past the protocol, the remote path would be exercised
// only by whoever operates two machines — and would rot everywhere else.
func TestBuiltInRunnerStartsAndStopsThroughTheProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), orgID)

	agentID := uuid.New()
	sb, err := p.Start(context.Background(), orchestrator.SandboxSpec{
		AgentID: agentID, OrgID: orgID, Image: "dev",
		Env: map[string]string{"COVEY_DAEMON_TOKEN": "tok"},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	args, err := os.ReadFile(filepath.Join(dir, "args"))
	if err != nil {
		t.Fatal(err)
	}
	// The profile is resolved on the control plane: a runner knows images, not
	// agents. If this ever moved to the node, every runner would need the
	// profile table — and a runner is precisely what should know nothing about
	// the platform.
	if !strings.Contains(string(args), "covey-sandbox-dev:test") {
		t.Errorf("the profile dev has to be resolved to its image:\n%s", args)
	}
	if !strings.Contains(string(args), "covey-sandbox-"+agentID.String()) {
		t.Errorf("the container has to carry the agent's name:\n%s", args)
	}

	// Ending it deliberately must NOT be reported as a death.
	var died []string
	p.SandboxDied = func(_ uuid.UUID, reason string) { died = append(died, reason) }
	if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := sb.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	time.Sleep(300 * time.Millisecond)
	if len(died) != 0 {
		t.Errorf("a stop somebody asked for is not a death: %v", died)
	}
}

// The value this seam delivers on its own: a sandbox that dies is a reported
// fact instead of a ReadyTimeout minutes later. Without it the control plane
// waits out the full timeout and then blames the daemon for an end the runner
// saw immediately.
func TestSandboxDeathIsReportedNotInferred(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), orgID)

	reported := make(chan string, 1)
	p.SandboxDied = func(_ uuid.UUID, reason string) { reported <- reason }

	agentID := uuid.New()
	if _, err := p.Start(context.Background(), orchestrator.SandboxSpec{AgentID: agentID, OrgID: orgID}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	// The container ends on its own — 137 is what an OOM kill looks like.
	if err := os.WriteFile(filepath.Join(dir, "stopped"), []byte("137\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	select {
	case reason := <-reported:
		if !strings.Contains(reason, "137") || !strings.Contains(reason, "memory") {
			t.Errorf("the reason has to be usable to a human, got %q", reason)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the end of the sandbox was not reported")
	}
}

// A runner serves exactly one organisation (spec/16). A runner of a foreign
// tenant is not a worse candidate, it is none — otherwise one organisation's
// homes and daemon tokens would land on another's machine.
func TestPoolNeverAssignsAcrossOrganisations(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	ourOrg := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), ourOrg)

	_, err := p.Start(context.Background(), orchestrator.SandboxSpec{
		AgentID: uuid.New(), OrgID: uuid.New(), // a foreign organisation
	})
	if !errors.Is(err, ErrNoRunner) {
		t.Fatalf("a foreign organisation must not be served, got %v", err)
	}

	// And the organisation without a runner gets its own built-in one when the
	// platform offers to create it — organisations come into being while the
	// process runs.
	other := uuid.New()
	p.EnsureLocal = func(ctx context.Context, orgID uuid.UUID) error {
		id := uuid.New()
		return p.AttachLocal(ctx, NewNode(id, orgID, &Docker{
			RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
		}, quietLog()))
	}
	if _, err := p.Start(context.Background(), orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: other}); err != nil {
		t.Fatalf("the new organisation should have got its built-in runner: %v", err)
	}
}

// AttachLocal binds the built-in runner's life to the context it is given, and
// nothing else says so. While EnsureLocal was only called at startup that was
// harmless — since it is also asked during a wake, handing it the wake's
// context makes a host that exists for one run: offline in the runner view
// between runs, and started again by every wake. The test states the property
// so the next caller can read what it is choosing.
func TestTheBuiltInRunnerLivesAsLongAsItsContext(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p := NewPool(quietLog())
	life, end := context.WithCancel(context.Background())
	id := uuid.New()
	if err := p.AttachLocal(life, NewNode(id, org, &Docker{
		RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())); err != nil {
		t.Fatal(err)
	}
	if _, err := p.pick(need{orgID: org}); err != nil {
		t.Fatalf("the runner should be there: %v", err)
	}
	end()
	// It goes with its context — which is why the context has to be the
	// process's and not a request's.
	waitUntil(t, 3*time.Second, func() bool {
		_, err := p.pick(need{orgID: org})
		return err != nil
	})
}

// The regression that cost covey.work its data plane: a remote runner was
// registered, the control plane restarted, and the built-in one was never
// brought up again because the organisation was no longer runner-less. What is
// left of that case after images stopped excluding anybody: a registered host
// that is not CONNECTED — a maintenance window, a reboot, a dead network —
// must not leave the organisation without a data plane either.
func TestABuiltInRunnerStepsInWhenNothingIsConnected(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p := NewPool(quietLog())

	ensured := 0
	p.EnsureLocal = func(ctx context.Context, orgID uuid.UUID) error {
		ensured++
		id := uuid.New()
		return p.AttachLocal(ctx, NewNode(id, orgID, &Docker{
			RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
		}, quietLog()))
	}
	if _, err := p.Start(context.Background(), orchestrator.SandboxSpec{
		AgentID: uuid.New(), OrgID: org, Image: "registry.example.com/team/dev:1",
	}); err != nil {
		t.Fatalf("the built-in runner should have taken it: %v", err)
	}
	if ensured != 1 {
		t.Errorf("EnsureLocal was called %d times, expected exactly one", ensured)
	}
}

// A start can fail on the host that was chosen — no credentials for the
// registry the image lies in, a full disk, a Docker that has just died. That
// is what makes the image an ordering criterion and not a wall: the sandbox
// moves to the next host instead of being lost with the first.
func TestAStartThatFailsMovesToTheNextRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	org := uuid.New()
	p := NewPool(quietLog())
	attach := func(dir, failOn string) uuid.UUID {
		id := uuid.New()
		if err := p.AttachLocal(context.Background(), NewNode(id, org, &Docker{
			RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, failOn),
		}, quietLog())); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// The one that claims the image is asked first — and cannot pull it.
	broken := attach(t.TempDir(), "run")
	p.SetCapabilities(broken, nil, []string{"team/dev:1"}, true)
	attach(t.TempDir(), "nothing")

	if _, err := p.Start(context.Background(), orchestrator.SandboxSpec{
		AgentID: uuid.New(), OrgID: org, Image: "team/dev:1",
	}); err != nil {
		t.Fatalf("the second host should have carried it: %v", err)
	}
}

func TestTheFallbackStillRespectsTags(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), org)
	p.EnsureLocal = func(ctx context.Context, orgID uuid.UUID) error {
		id := uuid.New()
		return p.AttachLocal(ctx, NewNode(id, orgID, &Docker{
			RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
		}, quietLog()))
	}
	_, err := p.Start(context.Background(), orchestrator.SandboxSpec{
		AgentID: uuid.New(), OrgID: org, RunnerTags: []string{"gpu"},
	})
	if !errors.Is(err, ErrNoRunner) {
		t.Fatalf("a tag nobody carries has to stay a refusal, got %v", err)
	}
}

// Tags and images belonged to the host's configuration file: changing them
// meant editing a file on the machine and restarting the runner. Assigned from
// the interface they have to apply to the connection that is standing right
// now — "why is it not taking anything, I gave it the tag" is the question the
// assignment exists to answer.
func TestAssignedCapabilitiesApplyToARunningConnection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), org)

	var id uuid.UUID
	p.mu.Lock()
	for _, c := range p.conns {
		c.reportedTags, c.tags = []string{"arm64"}, []string{"arm64"}
		c.reportedImages, c.images = []string{"covey-sandbox:latest"}, []string{"covey-sandbox:latest"}
		id = c.runnerID
	}
	p.mu.Unlock()

	// A tag is added, not replaced: the host does not stop being arm64 because
	// somebody labelled it "build".
	p.SetCapabilities(id, []string{"build"}, nil, false)
	if _, err := p.pick(need{orgID: org, tags: []string{"arm64", "build"}}); err != nil {
		t.Fatalf("both tags should be carried: %v", err)
	}
	// An image it does not claim is no longer a refusal — it is taken and
	// fetched. Only the tag says what a host is.
	if _, err := p.pick(need{orgID: org, image: "registry.example.com/team/dev:1"}); err != nil {
		t.Fatalf("an unclaimed workplace still belongs to it: %v", err)
	}
	// A tag that was taken back stops matching, though.
	p.SetCapabilities(id, nil, nil, false)
	if _, err := p.pick(need{orgID: org, tags: []string{"build"}}); err == nil {
		t.Error("tags that were taken back must not keep matching")
	}
}

// What a host holds is an ordering, not a wall: among candidates, the one that
// already has the image is the cheaper start — and the host the agent last
// worked on is cheaper still, because its home is lying there.
func TestTheOrderPrefersTheLastHostThenTheOneHoldingTheImage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p := NewPool(quietLog())
	attach := func(images []string) uuid.UUID {
		id := uuid.New()
		if err := p.AttachLocal(context.Background(), NewNode(id, org, &Docker{
			RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
		}, quietLog())); err != nil {
			t.Fatal(err)
		}
		p.SetCapabilities(id, nil, images, true)
		return id
	}
	bare := attach([]string{})
	holder := attach([]string{"team/dev:1"})

	// Nobody has worked anywhere yet: the host that holds the image wins.
	c, err := p.pick(need{orgID: org, image: "team/dev:1"})
	if err != nil {
		t.Fatal(err)
	}
	if c.runnerID != holder {
		t.Errorf("the host holding the image should come first, got %s", short(c.runnerID))
	}
	// The agent's last host beats it: an image is pulled in seconds, a home is
	// materialised in minutes.
	c, err = p.pick(need{orgID: org, image: "team/dev:1", prefer: bare})
	if err != nil {
		t.Fatal(err)
	}
	if c.runnerID != bare {
		t.Errorf("the last host should win, got %s", short(c.runnerID))
	}
	// But it is a preference and not a requirement: a host that is gone must
	// not keep the agent waiting.
	c, err = p.pick(need{orgID: org, image: "team/dev:1", prefer: uuid.New()})
	if err != nil {
		t.Fatalf("an absent last host must not be a refusal: %v", err)
	}
	if c.runnerID != holder {
		t.Errorf("without its last host the image decides, got %s", short(c.runnerID))
	}
}

// The recording answers "where did this run happen" through an optional
// interface — and an optional interface is the kind of thing a refactor drops
// without a compiler noticing. So the test asks for it by name, and checks
// that the label is the one an operator gave the host rather than a uuid.
func TestASandboxSaysWhichHostItRunsOn(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p, _ := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), org)
	p.RunnerLabel = func(context.Context, uuid.UUID) string { return "Build host Frankfurt" }

	sb, err := p.Start(context.Background(), orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org})
	if err != nil {
		t.Fatal(err)
	}
	placed, ok := sb.(orchestrator.Placed)
	if !ok {
		t.Fatal("a sandbox of the pool has to say which host it runs on")
	}
	id, label := placed.Runner()
	if id == uuid.Nil {
		t.Error("the host's id belongs in the recording")
	}
	if label != "Build host Frankfurt" {
		t.Errorf("label = %q, expected the name from the interface", label)
	}

	// Without a name the label still has to be readable — the recording is
	// read by people, and a bare uuid is not an answer.
	p.RunnerLabel = nil
	if _, label := placed.Runner(); label == "" || label == uuid.Nil.String() {
		t.Errorf("without a name there still has to be something readable, got %q", label)
	}
}

// The image hangs off the agent (spec/16). One column carries both a profile
// name and an image of an organisation's own, so the resolution is the whole
// rule — and if it is wrong, an agent silently works in a foreign workplace:
// either without the toolchain it needs, or with one it should not have.
func TestImageForResolvesProfilesAndOwnImages(t *testing.T) {
	p := &Pool{
		Profiles: map[string]string{
			"base": "covey-sandbox:latest",
			"dev":  "covey-sandbox-dev:latest",
		},
	}
	faelle := []struct {
		name, want, expected string
	}{
		{"nothing named → the default profile", "", "covey-sandbox:latest"},
		{"profile base", "base", "covey-sandbox:latest"},
		{"profile dev", "dev", "covey-sandbox-dev:latest"},
		// The third row of the profile table: an organisation builds its own
		// image and names it. Without this the field would need a second one
		// beside it that says how to read the first.
		{"own image", "registry.example.com/team/sandbox:2026-08", "registry.example.com/team/sandbox:2026-08"},
		// Whitespace comes from a text field in the interface — and a trailing
		// blank must not produce an image name nobody can find.
		{"trimmed", "  dev  ", "covey-sandbox-dev:latest"},
	}
	for _, f := range faelle {
		t.Run(f.name, func(t *testing.T) {
			if got := p.imageFor(t.Context(), uuid.Nil, f.want); got != f.expected {
				t.Errorf("imageFor(%q) = %q, expected %q", f.want, got, f.expected)
			}
		})
	}

	// A profile without a configured image must not swallow the value: an
	// instance without COVEY_SANDBOX_IMAGE_DEV would otherwise start every dev
	// agent in an empty image name.
	leer := &Pool{Profiles: map[string]string{"dev": ""}}
	if got := leer.imageFor(t.Context(), uuid.Nil, "dev"); got != "dev" {
		t.Errorf("an unconfigured profile stays the literal value, got %q", got)
	}
}

// Runner and server are delivered separately, so different versions inevitably
// meet. A refusal has to name which side is behind — a runner that quietly
// fails to connect costs an evening of searching.
// Der Fall, den covey.work an dem Tag zeigte, an dem es seine Arbeitsplätze aus
// dem Katalog nahm: Ein Agent ohne benannten Arbeitsplatz landete auf dem
// einkompilierten `covey-sandbox:latest`, weil dafür ein zweites Feld zuständig
// war, das beim Prozessstart gefüllt wird — vor dem ersten Katalogabruf. Der
// Standard ist ein Profilname, keine zweite Quelle.
func TestTheDefaultWorkplaceIsTheBaseProfile(t *testing.T) {
	p := &Pool{Profiles: map[string]string{sandbox.DefaultName(): "ghcr.io/example/covey-sandbox@sha256:abc"}}
	if got := p.imageFor(t.Context(), uuid.Nil, ""); got != "ghcr.io/example/covey-sandbox@sha256:abc" {
		t.Errorf("imageFor(\"\") = %q, erwartet das Bild des Standardprofils", got)
	}
	// Und derselbe Name ausgeschrieben ergibt dasselbe — sonst hinge es davon
	// ab, ob jemand das Feld ausfüllt.
	if got := p.imageFor(t.Context(), uuid.Nil, sandbox.DefaultName()); got != "ghcr.io/example/covey-sandbox@sha256:abc" {
		t.Errorf("imageFor(%q) = %q", sandbox.DefaultName(), got)
	}
}

// Der Zeitausfall begrenzt einen LANGSAMEN Start, nicht einen toten Runner —
// für den ist der Herzschlag zuständig. Deshalb ist die Vorgabe großzügig: Der
// erste Start auf einem Host ohne das Image ist ein Download von mehreren
// Gigabyte, und auf covey.work scheiterte er an zwei Minuten.
func TestTheStartTimeoutIsGenerousByDefaultAndSettable(t *testing.T) {
	if defaultStartTimeout < 30*time.Minute {
		t.Errorf("defaultStartTimeout = %s — zu knapp für einen Kaltstart mit Pull", defaultStartTimeout)
	}
	// Und die Instanz darf ihn setzen: 0 heißt „nimm die Vorgabe", damit es
	// nicht zwei Zahlen gibt, die auseinanderlaufen können.
	p := &Pool{}
	if got := p.startTimeout(); got != defaultStartTimeout {
		t.Errorf("ohne Wert = %s, erwartet die Vorgabe", got)
	}
	p.StartTimeout = 5 * time.Minute
	if got := p.startTimeout(); got != 5*time.Minute {
		t.Errorf("gesetzt = %s, erwartet 5m", got)
	}
}

func TestProtocolMismatchIsRefusedWithAReason(t *testing.T) {
	p := NewPool(quietLog())
	control, node := NewInProc()
	defer control.Close()

	go func() {
		msg, _ := encode(TypeRegistered, "", Registered{
			RunnerID: uuid.New(), OrgID: uuid.New(), Protocol: Protocol + 1, Version: "9.9.9",
		})
		_ = node.Send(context.Background(), msg)
	}()

	err := p.Attach(context.Background(), control, false)
	if err == nil {
		t.Fatal("a foreign protocol version has to be refused")
	}
	if !strings.Contains(err.Error(), "the control plane needs updating") {
		t.Errorf("the message has to name which side is behind: %v", err)
	}
}

// Scheduling has to be able to say why nothing fits, because the three causes
// call for different things: no runner at all, none with the tags, none with
// the image. One collective "no capacity" would send whoever reads it looking
// in the wrong place — and this is the message the transition to a registered
// runner produces first.
func TestSchedulingNamesWhyNothingFits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.Profiles = map[string]string{"base": "covey-sandbox:test", "dev": "covey-sandbox-dev:test"}
	p.StartTimeout = 5 * time.Second

	// No runner at all.
	_, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: orgID})
	if err == nil || !strings.Contains(err.Error(), "no connected runner") {
		t.Fatalf("without a runner the message has to say so: %v", err)
	}

	// An arm64 build host that holds only the base image — the case that
	// catches people out after registering their first runner.
	id := uuid.New()
	node := NewNode(id, orgID, &Docker{
		RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	node.Tags = []string{"arm64"}
	node.Images = []string{"covey-sandbox:test"}
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	// The agent wants a tag nobody carries.
	_, err = p.Start(ctx, orchestrator.SandboxSpec{
		AgentID: uuid.New(), OrgID: orgID, RunnerTags: []string{"gpu"},
	})
	if err == nil || !strings.Contains(err.Error(), "tags") {
		t.Errorf("a missing tag has to be named: %v", err)
	}

	// The agent wants the dev workplace, which this host does not claim. It
	// still goes there: docker run fetches an image the host does not have,
	// and a claim that excluded it instead is what left an organisation with a
	// runner and without a data plane. What the host IS (its tags) decides;
	// what it happens to hold is an ordering.
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: orgID, Image: "dev"}); err != nil {
		t.Errorf("an unclaimed workplace has to be taken and fetched: %v", err)
	}

	// What fits, runs — including the tag the host does carry.
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{
		AgentID: uuid.New(), OrgID: orgID, Image: "base", RunnerTags: []string{"arm64"},
	}); err != nil {
		t.Errorf("a matching runner has to be used: %v", err)
	}
}

// The self-check is what says at startup what an agent would otherwise run into
// at its first wake — and it has to ask the runners, because only they can see
// their own host. What it asks about follows the agents: asking about every
// configured profile would warn every fresh installation about a dev image
// nobody wants.
func TestPoolCheckAsksTheRunnersAboutTheImagesInUse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Nothing connected: that is itself the answer, and it names what to do.
	empty := NewPool(quietLog())
	problems := empty.Check(ctx)
	if len(problems) != 1 || !strings.Contains(problems[0], "no runner connected") {
		t.Fatalf("without a runner the check has to say so: %v", problems)
	}

	dir := t.TempDir()
	orgID := uuid.New()
	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.Profiles = map[string]string{"base": "covey-sandbox:test", "dev": "covey-sandbox-dev:test"}
	p.AgentImages = func(context.Context) (map[string]int, error) {
		return map[string]int{"dev": 2}, nil // nobody on base
	}
	id := uuid.New()
	node := NewNode(id, orgID, &Docker{
		RunnerID: id, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "image"), // every image inspect fails
	}, quietLog())
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	problems = p.Check(ctx)
	if len(problems) != 1 {
		t.Fatalf("exactly the image in use has to be reported: %v", problems)
	}
	if !strings.Contains(problems[0], "covey-sandbox-dev:test") {
		t.Errorf("the resolved image has to be named: %q", problems[0])
	}
	// The profile is resolved on this side — a runner knows images, not agents.
	if strings.Contains(problems[0], "\"dev\"") {
		t.Errorf("the profile name must not reach the runner: %q", problems[0])
	}

	// With everything in place the check stays silent. One that cries wolf gets
	// ignored, and then the one that matters is ignored too.
	quiet := NewPool(quietLog())
	quiet.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	quietID := uuid.New()
	quietDir := t.TempDir()
	if err := quiet.AttachLocal(ctx, NewNode(quietID, orgID, &Docker{
		RunnerID: quietID, Image: "covey-sandbox:test", DataDir: quietDir,
		DockerBin: fakeDockerBin(t, quietDir, "nothing"),
	}, quietLog())); err != nil {
		t.Fatal(err)
	}
	if problems := quiet.Check(ctx); len(problems) != 0 {
		t.Errorf("a working data plane has to stay silent: %v", problems)
	}
}

// What a runner reports it is carrying — the basis for the runner view and for
// the warning before the disk runs short.
func TestCapacityReportsWhatTheRunnerCarries(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runnerID := uuid.New()
	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	// The refresh runs at the heartbeat's tempo — at the speed of a test here.
	p.HeartbeatEvery = 100 * time.Millisecond
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	// Nobody asks the host: the connection does that by itself, and the first
	// question goes out with the connection.
	waitUntil(t, 3*time.Second, func() bool { _, ok := p.Capacity(runnerID); return ok })
	cap, _ := p.Capacity(runnerID)
	if cap.Sandboxes != 0 {
		t.Errorf("nothing is running yet: %d", cap.Sandboxes)
	}
	// The free space is that of the file system the working copies lie on —
	// exactly the figure that decides whether the next home still fits.
	if cap.TotalBytes == 0 || cap.FreeBytes == 0 || cap.WorkDir != dir {
		t.Errorf("the capacity is about the wrong thing: %+v", cap)
	}
	if cap.MeasuredAt.IsZero() {
		t.Error("a remembered figure without the moment it was taken says nothing")
	}

	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: orgID}); err != nil {
		t.Fatal(err)
	}
	// The disk figure follows a beat later — that is what "in the background"
	// means, and the age beside it is what makes it readable.
	waitUntil(t, 3*time.Second, func() bool { c, _ := p.Capacity(runnerID); return c.Sandboxes == 1 })

	// A runner that is not connected has no figure, and says so rather than
	// reporting an empty disk.
	if _, ok := p.Capacity(uuid.New()); ok {
		t.Error("an unknown runner has no capacity")
	}

	// The live view the runner page is built from.
	live := p.LiveFor(orgID)
	if l, ok := live[runnerID]; !ok || !l.Connected || l.Sandboxes != 1 || l.Outdated {
		t.Errorf("the live view is wrong: %+v", live)
	}
	if len(p.LiveFor(uuid.New())) != 0 {
		t.Error("a foreign organisation sees no runners")
	}
}

// A TCP connection can be dead without either side noticing — a NAT that
// dropped the entry, a network partition, a laptop that closed. Before the
// heartbeat, such a runner sat in the pool as "connected" for ever: every wake
// went to it, waited out the start timeout and then failed, instead of going to
// a runner that works.
//
// The test forces the case a real network only produces occasionally: a
// transport that accepts and then says nothing at all.
func TestSilentRunnerLeavesThePool(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	// A silence, at the speed of a test rather than of a network.
	p.HeartbeatEvery = 50 * time.Millisecond
	p.SilenceAfter = 150 * time.Millisecond

	control, node := NewInProc()
	defer control.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The node registers and then goes quiet — no heartbeat, no answers.
	go func() {
		msg, _ := encode(TypeRegistered, "", Registered{
			RunnerID: uuid.New(), OrgID: orgID, Protocol: Protocol,
		})
		_ = node.Send(ctx, msg)
		<-ctx.Done()
	}()

	attached := make(chan error, 1)
	go func() { attached <- p.Attach(ctx, control, false) }()

	// It is in the pool at first — the handshake was fine, and nothing has
	// happened yet that would say otherwise.
	waitUntil(t, 3*time.Second, func() bool { return len(p.LiveFor(orgID)) == 1 })

	// And it leaves by itself: nothing arrives, so the watchdog closes the
	// connection and the read loop returns.
	waitUntil(t, 5*time.Second, func() bool { return len(p.LiveFor(orgID)) == 0 })

	// Which means a start is refused straight away instead of waiting out its
	// timeout: the whole point.
	_, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: orgID})
	if !errors.Is(err, ErrNoRunner) {
		t.Errorf("a runner that has gone quiet must not be offered: %v", err)
	}
	select {
	case <-attached:
	case <-time.After(5 * time.Second):
		t.Error("Attach did not return after the connection was closed")
	}
}

// Any message counts as a sign of life, and a heartbeat arrives even when a
// runner has nothing else to say. Both together are what keeps "last seen" from
// being the moment a runner CONNECTED — which is the one thing nobody wants to
// know about one that has since gone away.
func TestHeartbeatKeepsARunnerAliveAndRefreshesLastSeen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	heard := make(chan uuid.UUID, 8)
	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.Heard = func(id uuid.UUID) {
		select {
		case heard <- id:
		default:
		}
	}

	id := uuid.New()
	node := NewNode(id, orgID, &Docker{
		RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	// Traffic is proof of life: an answered request reports in as much as a
	// heartbeat does — and one goes out with the connection, because it asks
	// the host about its disk without being told to.
	select {
	case got := <-heard:
		if got != id {
			t.Errorf("reported in for the wrong runner: %s", got)
		}
	case <-time.After(3 * time.Second):
		t.Error("an answer has to count as a sign of life")
	}

	// And the connection stays: the watchdog must not tear down a runner that
	// is simply idle.
	time.Sleep(500 * time.Millisecond)
	if len(p.LiveFor(orgID)) != 1 {
		t.Error("an idle runner must not be dropped")
	}
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// The data-plane check asks every host, and a host answers it out of its read
// loop — the same loop a start occupies while it pulls a multi-gigabyte image.
// Serially, one busy host therefore set the wait for all of them, and behind
// this check sits a view that is polled: the runner page took the sum of every
// host's slowness before it appeared. Now the question goes out to all at once,
// so the wait is the slowest host's, not the sum.
func TestTheCheckAsksEveryHostAtOnce(t *testing.T) {
	orgID := uuid.New()
	p := NewPool(quietLog())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	const answerAfter = 400 * time.Millisecond
	for i := 0; i < 3; i++ {
		control, node := NewInProc()
		defer control.Close()
		go slowRunner(ctx, node, orgID, answerAfter)
		go func() { _ = p.Attach(ctx, control, false) }()
	}
	waitUntil(t, 3*time.Second, func() bool { return len(p.LiveFor(orgID)) == 3 })

	start := time.Now()
	if problems := p.Check(ctx); len(problems) != 0 {
		t.Fatalf("the hosts answered without a complaint: %v", problems)
	}
	if took := time.Since(start); took > 2*answerAfter {
		t.Errorf("three hosts asked one after the other: %s for an answer that takes %s", took, answerAfter)
	}
}

// slowRunner is a host that answers correctly and slowly — a stand-in for one
// whose read loop is busy with something big.
func slowRunner(ctx context.Context, t Transport, orgID uuid.UUID, delay time.Duration) {
	msg, _ := encode(TypeRegistered, "", Registered{RunnerID: uuid.New(), OrgID: orgID, Protocol: Protocol})
	if err := t.Send(ctx, msg); err != nil {
		return
	}
	for {
		in, err := t.Receive(ctx)
		if err != nil {
			return
		}
		var answer Message
		switch in.Type {
		case TypeCheck:
			answer, _ = encode(TypeCheckResult, in.ID, CheckResult{})
		case TypeCapacity:
			answer, _ = encode(TypeCapacityReport, in.ID, CapacityReport{})
		default:
			continue
		}
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return
		}
		if err := t.Send(ctx, answer); err != nil {
			return
		}
	}
}

// hangingDockerBin is a docker whose FIRST call of one verb does not return —
// the shape of a `docker run` that is pulling gigabytes, at the speed of a
// test: it waits for a file instead of for a registry. Only the first, because
// the point of the test is the second agent, whose image is already there.
func hangingDockerBin(t *testing.T, dir, hangOn string) (bin string, release func()) {
	t.Helper()
	gate := filepath.Join(dir, "release")
	seen := filepath.Join(dir, "first")
	path := filepath.Join(dir, "docker")
	script := `#!/bin/sh
if [ "$1" = "` + hangOn + `" ] && [ ! -f ` + seen + ` ]; then
  : > ` + seen + `
  while [ ! -f ` + gate + ` ]; do
    [ -d '` + dir + `' ] || exit 1
    sleep 0.05
  done
fi
if [ "$1" = "wait" ]; then
  while [ ! -f ` + filepath.Join(dir, "stopped") + ` ]; do
    [ -d '` + dir + `' ] || exit 1
    sleep 0.05
  done
  cat ` + filepath.Join(dir, "stopped") + `
  exit 0
fi
echo ok
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, func() { _ = os.WriteFile(gate, []byte("go"), 0o644) }
}

// A start occupies the host for as long as its image takes, and that is allowed
// to be an hour. What is not allowed is that the host stops hearing anything in
// the meantime: on covey.work one agent's pull held up every other agent of the
// organisation, and the runner reported itself as connected throughout, because
// the heartbeat runs in a goroutine of its own.
func TestALongStartDoesNotBlockTheRunner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	bin, release := hangingDockerBin(t, dir, "run")
	defer release()

	runnerID := uuid.New()
	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.StartTimeout = 15 * time.Second
	p.HeartbeatEvery = 100 * time.Millisecond
	node := NewNode(runnerID, org, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: bin,
	}, quietLog())
	t.Cleanup(node.Close)
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	// The first agent's start goes into the pull and stays there.
	stuck := make(chan error, 1)
	go func() {
		_, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org})
		stuck <- err
	}()

	// While it hangs, the host still answers: the disk figure keeps coming.
	first, _ := p.Capacity(runnerID)
	waitUntil(t, 5*time.Second, func() bool {
		c, ok := p.Capacity(runnerID)
		return ok && c.MeasuredAt.After(first.MeasuredAt)
	})
	if problems := p.Check(ctx); len(problems) != 0 {
		t.Errorf("the check has to get through while a start is running: %v", problems)
	}

	// And it still takes work: a second agent is not held hostage by the first
	// one's download.
	select {
	case <-stuck:
		t.Fatal("the first start ended although its docker run is still hanging")
	default:
	}
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org}); err != nil {
		t.Fatalf("the second agent had to get through while the first was pulling: %v", err)
	}

	// And the first one finishes when its pull does — it was never lost, only
	// slow.
	release()
	if err := <-stuck; err != nil {
		t.Errorf("the first start after the pull: %v", err)
	}
}

// The line stands, the host says nothing: a runner whose read loop is stuck
// keeps its heartbeat — it runs in a goroutine of its own — and therefore
// counted as a candidate. Every wake went to it and waited out the start
// timeout, an hour, while the built-in runner stayed down: stepping in requires
// there to be NO candidate. That is what cost covey.work a night.
func TestARunnerThatDoesNotAnswerIsNoCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	org := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.StartTimeout = 5 * time.Second
	p.HeartbeatEvery = 50 * time.Millisecond
	p.SilenceAfter = 250 * time.Millisecond

	// A host that reports in and answers nothing.
	control, node := NewInProc()
	defer control.Close()
	go func() {
		hello, _ := encode(TypeRegistered, "", Registered{RunnerID: uuid.New(), OrgID: org, Protocol: Protocol})
		if err := node.Send(ctx, hello); err != nil {
			return
		}
		beat, _ := encode(TypeHeartbeat, "", nil)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
				if err := node.Send(ctx, beat); err != nil {
					return
				}
			}
		}
	}()
	go func() { _ = p.Attach(ctx, control, false) }()

	// It is connected — and stays connected: the heartbeat is genuine.
	waitUntil(t, 3*time.Second, func() bool { return len(p.LiveFor(org)) == 1 })
	// But after three unanswered questions it is named for what it is.
	waitUntil(t, 5*time.Second, func() bool {
		for _, l := range p.LiveFor(org) {
			return l.Connected && l.Unresponsive
		}
		return false
	})

	// And the built-in runner steps in, instead of every wake waiting out its
	// timeout on a machine that reads nothing.
	dir := t.TempDir()
	ensured := 0
	p.EnsureLocal = func(ctx context.Context, orgID uuid.UUID) error {
		ensured++
		id := uuid.New()
		return p.AttachLocal(ctx, NewNode(id, orgID, &Docker{
			RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
		}, quietLog()))
	}
	started := time.Now()
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org}); err != nil {
		t.Fatalf("the built-in runner had to take it: %v", err)
	}
	if ensured != 1 {
		t.Errorf("EnsureLocal was called %d times", ensured)
	}
	if took := time.Since(started); took > p.StartTimeout {
		t.Errorf("the wake waited out the silent host: %s", took)
	}
}

// A pause takes a host out of service without taking it apart: it keeps its
// token, its tags and its working copies, and it comes back with one click and
// no restart. It is also what replaced the rule that the built-in runner stood
// down by itself as soon as a remote one existed — a rule that inferred an
// intention from a fact and was wrong about it twice in one afternoon.
func TestAPausedRunnerTakesNothingAndComesBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	p, runnerID := newLocalPool(t, dir, fakeDockerBin(t, dir, "nothing"), org)
	ctx := context.Background()

	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org}); err != nil {
		t.Fatalf("before the pause: %v", err)
	}

	p.SetPaused(runnerID, true)
	_, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org})
	if err == nil {
		t.Fatal("a paused host must not take a sandbox")
	}
	// The reason has to be the decision and not a guess about the network:
	// whoever paused it should read that back, not "does not answer".
	if !strings.Contains(err.Error(), "paused") {
		t.Errorf("the message has to name the pause: %v", err)
	}

	p.SetPaused(runnerID, false)
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org}); err != nil {
		t.Fatalf("after resuming: %v", err)
	}
}

// And it survives a reconnect — otherwise restarting the runner would be the
// way around the pause, and a maintenance window that a restart ends is none.
// The connection asks the store for it at the handshake, next to the tags and
// images an operator assigned.
func TestAPauseSurvivesAReconnect(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	org := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	p.Capabilities = func(context.Context, uuid.UUID) (extraTags, images []string, decided, paused bool, err error) {
		return nil, nil, false, true, nil
	}
	id := uuid.New()
	node := NewNode(id, org, &Docker{
		RunnerID: id, Image: "covey-sandbox:test", DataDir: dir, DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	t.Cleanup(node.Close)
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org}); err == nil {
		t.Fatal("the host was paused before it connected — it must not take a sandbox")
	}
}

// The built-in runner no longer stands down when an organisation registers a
// host of its own — but the intention behind that rule was right: whoever adds
// a machine wants the compute there. So it is the last candidate of all, and
// only the last: while a registered host can take the work it does, and when
// none can, the organisation still runs instead of waiting.
func TestTheBuiltInRunnerIsTheLastCandidate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	org := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}

	// The built-in one, in the pool from the start — as on every instance since
	// it stopped standing down.
	builtinDir := t.TempDir()
	builtinID := uuid.New()
	if err := p.AttachLocal(ctx, NewNode(builtinID, org, &Docker{
		RunnerID: builtinID, Image: "covey-sandbox:test", DataDir: builtinDir,
		DockerBin: fakeDockerBin(t, builtinDir, "nothing"),
	}, quietLog())); err != nil {
		t.Fatal(err)
	}
	// And a registered host beside it.
	remoteDir := t.TempDir()
	remoteID := uuid.New()
	control, node := NewInProc()
	defer control.Close()
	go func() {
		n := NewNode(remoteID, org, &Docker{
			RunnerID: remoteID, Image: "covey-sandbox:test", DataDir: remoteDir,
			DockerBin: fakeDockerBin(t, remoteDir, "nothing"),
		}, quietLog())
		defer n.Close()
		_ = n.Run(ctx, node)
	}()
	go func() { _ = p.Attach(ctx, control, false) }()
	waitUntil(t, 3*time.Second, func() bool { return len(p.LiveFor(org)) == 2 })

	sb, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if id, _ := sb.(orchestrator.Placed).Runner(); id != remoteID {
		t.Errorf("the registered host has to be asked first: %s", id)
	}

	// Pause it, and the built-in one carries the organisation rather than
	// letting it stand still.
	p.SetPaused(remoteID, true)
	sb, err = p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org})
	if err != nil {
		t.Fatalf("with the host paused the built-in one has to take it: %v", err)
	}
	if id, _ := sb.(orchestrator.Placed).Runner(); id != builtinID {
		t.Errorf("the built-in one had to take it: %s", id)
	}
}

// deafRunner is a host that answers for a while and then reads nothing — the
// shape of a runner whose loop is stuck inside an image pull. It keeps its
// heartbeat, because that runs in a goroutine of its own: that is what made
// the case so hard to see from the control plane.
func deafRunner(ctx context.Context, t Transport, orgID uuid.UUID, answers int, got chan<- string) {
	hello, _ := encode(TypeRegistered, "", Registered{RunnerID: uuid.New(), OrgID: orgID, Protocol: Protocol})
	if err := t.Send(ctx, hello); err != nil {
		return
	}
	go func() {
		beat, _ := encode(TypeHeartbeat, "", nil)
		for {
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
				if err := t.Send(ctx, beat); err != nil {
					return
				}
			}
		}
	}()
	left := answers
	for {
		in, err := t.Receive(ctx)
		if err != nil {
			return
		}
		select {
		case got <- in.Type:
		default:
		}
		if in.Type == TypeCapacity && left > 0 {
			left--
			answer, _ := encode(TypeCapacityReport, in.ID, CapacityReport{})
			if err := t.Send(ctx, answer); err != nil {
				return
			}
		}
	}
}

// A host can go deaf in the second after it was picked — "answering" is a
// statement about the last 90 seconds, and a start may take an hour because it
// may be a multi-gigabyte pull. On covey.work that combination cost an agent a
// full hour of standing still with no message at all, while the signal that
// would have said so — the capacity question every beat — kept arriving with
// nobody listening.
func TestAStartIsTakenBackFromAHostThatGoesDeaf(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	org := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	p := NewPool(quietLog())
	p.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
	// An hour in production; here the number that says "far longer than the
	// test may take".
	p.StartTimeout = 30 * time.Second
	p.HeartbeatEvery = 60 * time.Millisecond
	p.SilenceAfter = 200 * time.Millisecond

	// The host that stops reading after its first capacity answer.
	seen := make(chan string, 32)
	control, node := NewInProc()
	defer control.Close()
	go deafRunner(ctx, node, org, 1, seen)
	go func() { _ = p.Attach(ctx, control, false) }()
	waitUntil(t, 3*time.Second, func() bool { return len(p.LiveFor(org)) == 1 })

	// And the built-in one behind it, which is where the agent should end up.
	dir := t.TempDir()
	builtinID := uuid.New()
	if err := p.AttachLocal(ctx, NewNode(builtinID, org, &Docker{
		RunnerID: builtinID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())); err != nil {
		t.Fatal(err)
	}

	started := time.Now()
	sb, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: org})
	if err != nil {
		t.Fatalf("the agent had to end up somewhere: %v", err)
	}
	if took := time.Since(started); took > 5*time.Second {
		t.Errorf("the start waited on the deaf host: %s", took)
	}
	if id, _ := sb.(orchestrator.Placed).Runner(); id != builtinID {
		t.Errorf("the sandbox belongs on the host that answers: %s", id)
	}

	// And the abandoned start is taken back: the message lies in front of the
	// stuck loop, and when the pull finishes it must not start a container for
	// an agent that has long since woken elsewhere.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case msg := <-seen:
			if msg == TypeStopSandbox {
				return
			}
		case <-deadline:
			t.Fatal("the start was abandoned but never taken back")
		}
	}
}
