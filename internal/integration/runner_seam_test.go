package integration

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/orchestrator"
	"covey/internal/runner"
)

// Every sandbox starts through the runner protocol — including on a single
// machine, where the runner sits in the control plane's own process. These
// tests take that path in full: orchestrator → pool → protocol → node →
// docker (faked). What they are guarding is the reason the seam exists at all:
// if the in-process case took a shortcut past the protocol, the remote path
// would be exercised only by whoever operates two machines, and would rot
// everywhere else.

// fakeDocker writes a docker stand-in. It never starts a daemon — which is the
// point here: the sandbox comes up and nothing connects, exactly the situation
// a crash produces. `wait` blocks until the file `exit` appears and then
// reports that code, so the test decides when the container dies.
func fakeDocker(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "docker")
	script := `#!/bin/sh
printf '%s ' "$@" >> ` + filepath.Join(dir, "args") + `
printf '\n' >> ` + filepath.Join(dir, "args") + `
if [ "$1" = "wait" ]; then
  while [ ! -f ` + filepath.Join(dir, "exit") + ` ]; do sleep 0.05; done
  cat ` + filepath.Join(dir, "exit") + `
  exit 0
fi
echo ok
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// A dead sandbox is a reported fact, not something inferred from a timeout.
// The ReadyTimeout here is deliberately far longer than the test's patience:
// if the report did not arrive, the test would fail on its own deadline — and
// that is precisely the minutes an operator used to spend waiting, only to be
// told that the daemon had not connected.
func TestDeadSandboxEndsTheWakeInsteadOfTimingOut(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	dockerBin := fakeDocker(t, dir)

	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		// Long on purpose: the death has to end the wake, not the clock.
		readyTimeout: 90 * time.Second,
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.DefaultImage = "covey-sandbox:test"
			pool.StartTimeout = 20 * time.Second
			return pool
		},
		afterOrch: func(o *orchestrator.Orchestrator) { pool.SandboxDied = o.SandboxDied },
	})
	ctx := context.Background()

	// The built-in runner of this organisation — one process, one connection,
	// the same protocol a foreign host would speak.
	runnerID := uuid.New()
	node := runner.NewNode(runnerID, s.orgID, &runner.Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: dockerBin,
	}, slog.Default())
	if err := pool.AttachLocal(ctx, node); err != nil {
		t.Fatalf("built-in runner: %v", err)
	}

	agent := s.newSupportAgent("dying-sandbox")
	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Ein Lauf, der nie beginnt", "", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}

	// Wait until the sandbox is up (the fake docker has been called), then let
	// the container die the way an OOM kill does.
	waitFor(t, "sandbox started", 20*time.Second, func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, "args"))
		return err == nil && strings.Contains(string(raw), "covey-sandbox-"+agent.ID.String())
	})
	if err := os.WriteFile(filepath.Join(dir, "exit"), []byte("137\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Well inside the ReadyTimeout: the report is what ends this, not the clock.
	var found string
	waitFor(t, "the run ends after the death of the sandbox", 30*time.Second, func() bool {
		events, err := s.obs.Events(ctx, agent.ID, nil, 0, 500)
		if err != nil {
			return false
		}
		for _, e := range events {
			if payload := string(e.Payload); strings.Contains(payload, "wake_failed") {
				found = payload
				return true
			}
		}
		return false
	})
	if !strings.Contains(found, "did not survive") {
		t.Fatalf("the recording has to say that the sandbox died: %s", found)
	}
	if s.taskState(task.ID) == backlog.StateDone {
		t.Errorf("a task whose sandbox died must not count as done")
	}
	// The reason has to travel: without it the entry says only that something
	// went wrong, and the search starts at the runtime instead of at memory.
	if !strings.Contains(found, "137") {
		t.Errorf("the reason belongs in the message, got %q", found)
	}
}
