package runner

import (
	"bytes"
	"context"
	"crypto/rand"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/sandboxfs"
)

// streamingPair wires a pool and a node with a home holding one large file,
// and hands back the tree the file browser would read it through.
func streamingPair(t *testing.T, size int) (*Pool, *Node, []byte, uuid.UUID) {
	t.Helper()
	dir := t.TempDir()
	orgID, runnerID, agentID := uuid.New(), uuid.New(), uuid.New()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p := NewPool(quietLog())
	p.HomeInfo = func(context.Context, uuid.UUID) (uuid.UUID, uuid.UUID, string, error) {
		return orgID, runnerID, "", nil
	}
	node := NewNode(runnerID, orgID, &Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir,
		DockerBin: fakeDockerBin(t, dir, "nothing"),
	}, quietLog())
	t.Cleanup(node.Close)
	attachRemote(t, ctx, p, node)

	home, _, _ := node.Docker.AgentHome(agentID)
	if err := os.MkdirAll(home, 0o755); err != nil {
		t.Fatal(err)
	}
	content := make([]byte, size)
	if _, err := rand.Read(content); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "big.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	return p, node, content, agentID
}

// A slow reader of a large file must not cost the connection its other
// answers. The runner used to send as fast as the link took, the read loop
// blocked on the bounded channel, and a host in the middle of a download
// counted as "not answering" — every start under way on it was taken back
// (#156). With the window the runner waits for the reader instead.
func TestASlowDownloadDoesNotBlockTheConnection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	// Three times the window, so an unthrottled runner would fill the channel.
	p, node, content, agentID := streamingPair(t, 3*streamWindow*chunkLimit)
	tree, err := p.AgentFiles(agentID)
	if err != nil {
		t.Fatal(err)
	}
	rc, info, err := tree.Open("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer rc.Close()
	if info.Size != int64(len(content)) {
		t.Fatalf("size %d, want %d", info.Size, len(content))
	}
	// One chunk, then the browser stalls.
	first := make([]byte, chunkLimit)
	if _, err := io.ReadFull(rc, first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)

	// The connection still answers within a beat's fraction while the
	// download stands.
	c := p.connFor(node.OrgID, node.RunnerID)
	ask, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := c.ask(ask, TypeCapacity, nil, 2*time.Second); err != nil {
		t.Fatalf("the connection is blocked behind the download: %v", err)
	}

	rest, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(first, rest...), content) {
		t.Fatal("the download does not match the file")
	}
}

// Closing the download ends the pump on the runner: the rest of the file is
// not read for nobody.
func TestClosingADownloadStopsThePump(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	p, node, _, agentID := streamingPair(t, 3*streamWindow*chunkLimit)
	tree, err := p.AgentFiles(agentID)
	if err != nil {
		t.Fatal(err)
	}
	rc, _, err := tree.Open("big.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(rc, make([]byte, chunkLimit)); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 3*time.Second, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		return len(node.streams) == 1
	})
	rc.Close()
	waitUntil(t, 3*time.Second, func() bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		return len(node.streams) == 0
	})
}

// An archive whose plan fails answers with a message that ENDS the stream. It
// used to answer without EOF, and the reader waited out its whole timeout for
// a chunk that was never coming (#156).
func TestAZipThatCannotBePlannedEndsAtOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	p, _, _, agentID := streamingPair(t, 16)
	tree, err := p.AgentFiles(agentID)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		var sink bytes.Buffer
		// A plan nobody measured, pointing at nothing.
		done <- tree.WriteZip(&sink, sandboxfs.ZipPlan{Name: "x.zip", Paths: []string{"does-not-exist"}})
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an archive of nothing has to fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failed archive left the reader waiting")
	}
}
