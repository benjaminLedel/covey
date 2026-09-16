package runner

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/engines"
)

// What the engine catalogue promises is about a container, and the tests beside
// this one stop at the argument list. This one asks the host, because three
// parts of that promise are not statements about our own code: that the bytes on
// the far side of a bind mount are the ones the digest agreed on, that the
// read-only flag holds (a sandbox able to rewrite its own runtime is a supply
// chain, not a sandbox), and that the engine really is absent from the image —
// the one fact this mechanism exists for.
//
// Skipped without docker, like the live service test next door is.
func TestLiveEngineLayerRunsInsideTheSandbox(t *testing.T) {
	if err := exec.Command("docker", "version").Run(); err != nil {
		t.Skip("no docker on this host")
	}
	// Any image with a shell will do. The more ordinary the image, the cleaner
	// the claim, because the claim is that no image carries this engine.
	const image = "alpine:latest"
	if err := exec.Command("docker", "image", "inspect", image).Run(); err != nil {
		t.Skipf("%s is not on this host", image)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	dir := t.TempDir()
	art := engineTarball(t)
	artPath := filepath.Join(dir, "sevencode.tgz")
	if err := os.WriteFile(artPath, art, 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(art)
	catPath := filepath.Join(dir, "engines.json")
	catBody := []byte(`{"schema":1,"engines":[{"name":"sevencode","versions":[` +
		`{"version":"1.0.8","kind":"tarball","url":"file://` + artPath +
		`","integrity":"sha256:` + hex.EncodeToString(sum[:]) + `"}]}]}`)
	if err := os.WriteFile(catPath, catBody, 0o644); err != nil {
		t.Fatal(err)
	}

	// A host that cannot show its own directory to a container can say nothing
	// about a mount: Docker on the Mac shares only what it is told to share, and
	// a temporary directory usually is not on that list. Then this is skipped,
	// not failed — the argument list next door still holds either way.
	if out, err := runInSandbox(ctx, image, []string{"-v", dir + ":/probe:ro"}, nil,
		"head -c 12 /probe/engines.json"); err != nil || !strings.Contains(out, `{"schema":1`) {
		t.Skipf("this docker cannot bind-mount the test directory: %v: %s", err, out)
	}

	p := &Docker{
		Image:       image,
		DataDir:     dir,
		Engines:     engines.NewSource("file://"+catPath, nil, nil),
		EngineStore: &engines.Store{Dir: filepath.Join(dir, "engines")},
	}
	env, mount, err := p.engineLayer(ctx, StartSandbox{AgentID: uuid.New(), Engine: "sevencode"})
	if err != nil {
		t.Fatalf("engineLayer: %v", err)
	}
	if len(mount) != 2 || mount[0] != "-v" {
		t.Fatalf("expected one bind mount, got %v", mount)
	}

	// The line the daemon's adapter would run: the variable the catalogue named,
	// resolved inside the container. Then the same path written to — the layer is
	// mounted read-only, and a sandbox that could edit its own runtime would make
	// every digest in this file a suggestion.
	out, err := runInSandbox(ctx, image, mount, env, strings.Join([]string{
		`echo "path=$COVEY_SEVENCODE_BIN"`,
		`"$COVEY_SEVENCODE_BIN" >/dev/null && echo ran=ok`,
		`touch "$COVEY_SEVENCODE_BIN.w" 2>/dev/null || echo writable=no`,
	}, "; "))
	if err != nil {
		t.Fatalf("the container refused to run: %v: %s", err, out)
	}
	for _, want := range []string{
		"path=/opt/engines/sevencode/1.0.8/bin/sevencode",
		"ran=ok",
		"writable=no",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("the sandbox did not report %q. Output:\n%s", want, out)
		}
	}

	// And the same image with nothing mounted: the engine is not in it. Without
	// this the run above could have been satisfied by an image that ships the
	// engine, which is the state the layer was built to replace.
	out, err = runInSandbox(ctx, image, nil, nil,
		"test -e /opt/engines/sevencode && echo present || echo absent")
	if err != nil {
		t.Fatalf("the bare image did not answer: %v: %s", err, out)
	}
	if !strings.Contains(out, "absent") {
		t.Errorf("%s seems to carry the engine already, so the run above proves nothing:\n%s", image, out)
	}
}

// runInSandbox runs one shell line in a throwaway container, with the mount and
// the variables a catalogue entry produced for it. `--entrypoint sh` because the
// entrypoints of these images are daemons and servers, and this test wants a
// shell rather than a workload.
func runInSandbox(ctx context.Context, image string, mount, env []string, script string) (string, error) {
	args := []string{"run", "--rm", "--entrypoint", "sh"}
	args = append(args, mount...)
	for _, e := range env {
		args = append(args, "-e", e)
	}
	args = append(args, image, "-c", script)
	out, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	return string(out), err
}
