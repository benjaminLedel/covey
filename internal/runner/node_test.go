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

	"github.com/google/uuid"
)

// A sandbox watcher outlives the call that started it on purpose — a death
// nobody asked for has to be reported even after the request is long answered,
// and a connection that drops is not the end of the sandbox behind it. What a
// watcher must not outlive is the node: it blocks in a `docker wait` child
// process, and nothing else in the system ever ends that. A node that
// disappears without cancelling leaves it polling for a container that will
// never exist again — past the test, past the test binary, until the host is
// rebooted. Thirty of them polling at 20 Hz look, from the outside, like a
// machine restarting on its own.
//
// Both halves are asserted here, because a fix for one breaks the other: end
// the watchers when Run returns and the reconnect stops reporting deaths.
func TestTheWatcherSurvivesTheConnectionButNotTheNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	orgID, runnerID := uuid.New(), uuid.New()
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	t.Cleanup(node.Close)

	// The two ends by hand rather than through AttachLocal: this test is about
	// what happens when the transport goes and the context stays, and only the
	// control end can produce that.
	control, nodeEnd := NewInProc()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- node.Run(ctx, nodeEnd) }()

	if _, err := control.Receive(ctx); err != nil { // registered
		t.Fatalf("registration: %v", err)
	}
	start, err := encode(TypeStartSandbox, "1", StartSandbox{AgentID: uuid.New(), OrgID: orgID})
	if err != nil {
		t.Fatal(err)
	}
	if err := control.Send(ctx, start); err != nil {
		t.Fatalf("start: %v", err)
	}

	// Only once the child process exists can the test say anything about
	// whether it goes away.
	var pid int
	waitUntil(t, 10*time.Second, func() bool {
		raw, err := os.ReadFile(filepath.Join(dir, "waitpid"))
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(raw)))
		return err == nil && pid > 0
	})

	// The connection ends, the node lives on — this is the reconnect in
	// RunNode, and the sandbox it was watching is still running.
	_ = control.Close()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run has to end with the transport")
	}
	time.Sleep(200 * time.Millisecond)
	if err := syscall.Kill(pid, 0); err != nil { // signal 0: is it still there?
		t.Fatalf("a dropped connection must not end the watcher: %v", err)
	}

	node.Close()

	waitUntil(t, 10*time.Second, func() bool {
		return syscall.Kill(pid, 0) != nil
	})
}
