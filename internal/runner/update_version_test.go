package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// The version ends up in a URL path and a file name on the host, so the runner
// refuses anything that is not a release tag before it downloads anything.
func TestAnUpdateRefusesAVersionThatIsNotAReleaseTag(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "covey-runner")
	if err := os.WriteFile(exe, []byte("the old one"), 0o755); err != nil {
		t.Fatal(err)
	}
	node := NewNode(uuid.New(), uuid.New(), &Docker{DataDir: dir}, quietLog())
	node.Restart = func() error { t.Error("must not restart"); return nil }
	node.executable = func() (string, error) { return exe, nil }

	for _, v := range []string{"../../x", "v1.2.3/../../evil", "v1.2.3?x=1", "latest", "1.2.3", "v1.2", "v1.2.3-", "v1.2.3 x"} {
		res := node.updateSelf(context.Background(), Update{Version: v})
		if res.Err == "" || !strings.Contains(res.Err, "release version") {
			t.Errorf("version %q was not refused: %+v", v, res)
		}
	}
	for _, v := range []string{"v0.9.0", "v10.20.30", "v1.2.3-rc1", "v1.2.3-rc.1"} {
		if !IsReleaseTag(v) {
			t.Errorf("%q is a release tag", v)
		}
	}
}
