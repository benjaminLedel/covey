package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSandboxImagesStartTheDaemonByItsPath is #699: the sandbox PATH starts
// with directories of the agent's home, and an ENTRYPOINT that names coveyd
// bare started whatever the agent had put there under that name.
func TestSandboxImagesStartTheDaemonByItsPath(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("..", "..", "Dockerfile.sandbox*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no sandbox Dockerfiles found: %v", err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if !strings.HasPrefix(line, "ENTRYPOINT") {
				continue
			}
			if !strings.Contains(line, `"/usr/local/bin/coveyd"`) {
				t.Errorf("%s: %s — the daemon has to be named by its absolute path", filepath.Base(f), line)
			}
		}
	}
}
