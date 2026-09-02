package integration

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"covey/internal/orchestrator"
	"covey/internal/runner"
	"covey/internal/sandbox"
)

// The daemon token is minted before the start and read when the daemon dials
// — and a first start on a fresh host pulls gigabytes in between. A token whose
// TTL ended in that gap yielded a container refused with 401 and an operator
// told to check COVEY_PUBLIC_URL. The token now outlives the start bound (#164).
func TestTheDaemonTokenOutlivesTheStartBound(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake binary is a shell script")
	}
	dir := t.TempDir()
	dockerBin := fakeDocker(t, dir)
	var pool *runner.Pool
	s := newStackWith(t, stackOpts{
		readyTimeout: 5 * time.Second,
		provider: func(homeBase string, log *slog.Logger) orchestrator.SandboxProvider {
			pool = runner.NewPool(log)
			pool.Profiles = map[string]string{sandbox.DefaultName(): "covey-sandbox:test"}
			pool.StartTimeout = 47 * time.Minute // far beyond the stack's 5-minute token TTL
			return pool
		},
	})
	ctx := context.Background()
	runnerID := uuid.New()
	node := runner.NewNode(runnerID, s.orgID, &runner.Docker{
		RunnerID: runnerID, Image: "covey-sandbox:test", DataDir: dir, DockerBin: dockerBin,
	}, slog.Default())
	t.Cleanup(node.Close)
	if err := pool.AttachLocal(ctx, node); err != nil {
		t.Fatal(err)
	}

	agent := s.newSupportAgent("long-start")
	if _, err := s.backlog.Create(ctx, s.orgID, agent.ID, "A wake whose start is long", "", "manual", 3); err != nil {
		t.Fatal(err)
	}
	var token string
	waitFor(t, "docker run with the daemon token", 20*time.Second, func() bool {
		args, _ := os.ReadFile(filepath.Join(dir, "args"))
		for _, line := range strings.Split(string(args), "\n") {
			if !strings.HasPrefix(line, "run ") {
				continue
			}
			for _, field := range strings.Fields(line) {
				if v, ok := strings.CutPrefix(field, "COVEY_DAEMON_TOKEN="); ok {
					token = v
				}
			}
		}
		return token != ""
	})
	// The container never dials; end the wake so the stack can shut down.
	_ = os.WriteFile(filepath.Join(dir, "exit"), []byte("1\n"), 0o644)

	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		Exp int64 `json:"exp"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	if left := time.Until(time.Unix(claims.Exp, 0)); left < 45*time.Minute {
		t.Fatalf("the token expires in %s — before a long start could end", left.Round(time.Minute))
	}
}
