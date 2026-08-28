package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"covey/internal/orchestrator"

	"github.com/google/uuid"
)

/* Cancelling is a signal, not a join.

   Close cancelled its sandbox watchers and returned, and a watcher does not
   notice a cancelled context until it comes past it again — with `docker wait`
   still running, a container still standing and a working copy still open.
   Whoever cleaned up afterwards therefore cleaned up under running work: in the
   tests a directory removed while the docker double was still writing into it
   (#114, "TempDir RemoveAll cleanup: directory not empty" in a test whose own
   assertions all held), in production a teardown cut short by the process
   exiting.

   Same defect one layer up, and fixed there first: #98. */

func TestCloseWaitsForTheWatchersItCancelled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the docker double is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	p := NewPool(quietLog())
	p.Profiles = map[string]string{"base": "covey-sandbox:test"}
	p.StartTimeout = 10 * time.Second
	if err := p.AttachLocal(ctx, node); err != nil {
		t.Fatalf("built-in runner: %v", err)
	}
	if _, err := p.Start(context.Background(), orchestrator.SandboxSpec{
		AgentID: agentID, OrgID: orgID, Image: "base",
		Env: map[string]string{"COVEY_DAEMON_TOKEN": "tok"},
	}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The watcher is the `docker wait` of the double, and it writes down its
	// pid. That pid is the whole test: it is the process that would still be
	// writing into this directory after Close.
	pid := waitForPid(t, filepath.Join(dir, "waitpid"))

	node.Close()

	if err := syscall.Kill(pid, 0); err == nil {
		t.Fatalf("the watcher (pid %d) is still alive after Close returned — "+
			"whoever tears down now tears down under a live writer", pid)
	}
}

// waitForPid reads the pid the docker double writes when it starts waiting.
// Polled, because the watcher is a goroutine: the file appears shortly after
// Start, not within it.
func waitForPid(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		raw, err := os.ReadFile(path)
		if err == nil {
			if pid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil && pid > 0 {
				return pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the watcher never started: %s was not written", path)
	return 0
}
