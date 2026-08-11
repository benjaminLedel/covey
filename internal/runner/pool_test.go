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
)

func quietLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

// fakeDockerBin writes a docker stand-in that records its arguments and can be
// told to fail for one subcommand. `wait` blocks until the file `stopped`
// appears — that is how a container that is still running is modelled.
func fakeDockerBin(t *testing.T, dir string, failOn string) string {
	t.Helper()
	path := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s ' "$@" >> ` + filepath.Join(dir, "args") + `
printf '\n' >> ` + filepath.Join(dir, "args") + `
if [ "$1" = "` + failOn + `" ]; then echo 'boom' >&2; exit 1; fi
if [ "$1" = "wait" ]; then
  while [ ! -f ` + filepath.Join(dir, "stopped") + ` ]; do sleep 0.05; done
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
	p.DefaultImage = "covey-sandbox:test"
	p.Profiles = map[string]string{"base": "covey-sandbox:test", "dev": "covey-sandbox-dev:test"}
	p.StartTimeout = 10 * time.Second

	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: dockerBin,
	}, quietLog())
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

// The image hangs off the agent (spec/16). One column carries both a profile
// name and an image of an organisation's own, so the resolution is the whole
// rule — and if it is wrong, an agent silently works in a foreign workplace:
// either without the toolchain it needs, or with one it should not have.
func TestImageForResolvesProfilesAndOwnImages(t *testing.T) {
	p := &Pool{
		DefaultImage: "covey-sandbox:latest",
		Profiles: map[string]string{
			"base": "covey-sandbox:latest",
			"dev":  "covey-sandbox-dev:latest",
		},
	}
	faelle := []struct {
		name, want, expected string
	}{
		{"nothing named → the instance default", "", "covey-sandbox:latest"},
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
			if got := p.imageFor(f.want); got != f.expected {
				t.Errorf("imageFor(%q) = %q, expected %q", f.want, got, f.expected)
			}
		})
	}

	// A profile without a configured image must not swallow the value: an
	// instance without COVEY_SANDBOX_IMAGE_DEV would otherwise start every dev
	// agent in an empty image name.
	leer := &Pool{DefaultImage: "covey-sandbox:latest", Profiles: map[string]string{"dev": ""}}
	if got := leer.imageFor("dev"); got != "dev" {
		t.Errorf("an unconfigured profile stays the literal value, got %q", got)
	}
}

// Runner and server are delivered separately, so different versions inevitably
// meet. A refusal has to name which side is behind — a runner that quietly
// fails to connect costs an evening of searching.
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
	p.DefaultImage = "covey-sandbox:test"
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

	// The agent wants the dev workplace, which this host does not hold. The
	// remedy has to be in the message — there is deliberately no fallback.
	_, err = p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: orgID, Image: "dev"})
	if err == nil || !strings.Contains(err.Error(), "covey-sandbox-dev:test") {
		t.Errorf("a missing image has to be named: %v", err)
	}
	if err != nil && !strings.Contains(err.Error(), "build it there") {
		t.Errorf("the message has to name the remedy: %v", err)
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
	p.DefaultImage = "covey-sandbox:test"
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
	quiet.DefaultImage = "covey-sandbox:test"
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
	p.DefaultImage = "covey-sandbox:test"
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	cap, err := p.Capacity(ctx, runnerID)
	if err != nil {
		t.Fatalf("Capacity: %v", err)
	}
	if cap.Sandboxes != 0 {
		t.Errorf("nothing is running yet: %d", cap.Sandboxes)
	}
	// The free space is that of the file system the working copies lie on —
	// exactly the figure that decides whether the next home still fits.
	if cap.TotalBytes == 0 || cap.FreeBytes == 0 || cap.WorkDir != dir {
		t.Errorf("the capacity is about the wrong thing: %+v", cap)
	}

	if _, err := p.Start(ctx, orchestrator.SandboxSpec{AgentID: uuid.New(), OrgID: orgID}); err != nil {
		t.Fatal(err)
	}
	if cap, err = p.Capacity(ctx, runnerID); err != nil || cap.Sandboxes != 1 {
		t.Errorf("the running sandbox has to show up: %+v, %v", cap, err)
	}

	// A runner that is not connected has no capacity to report, and says so.
	if _, err := p.Capacity(ctx, uuid.New()); !errors.Is(err, ErrNoRunner) {
		t.Errorf("an unknown runner: %v", err)
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
