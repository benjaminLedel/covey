package daemon

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCodexAdapterAllowsNonGitAgentHomes catches the fleet failure where
// `codex exec` rejected a perfectly valid Covey agent home before doing any
// work. Covey's container is the isolation boundary; an agent is not required
// to start in a Git checkout.
func TestCodexAdapterAllowsNonGitAgentHomes(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, "codex")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$HOME/args.txt\"\n" +
		"printf '%s\\n' '{\"type\":\"turn.completed\",\"text\":\"done\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := (&Codex{Binary: bin}).Run(context.Background(), RunSpec{
		Body: "finish the task", HomeDir: home, WorkDir: home,
		Env: []string{"HOME=" + home},
	}, func(string, json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != "done" || res.Result != "done" {
		t.Fatalf("run did not complete: %+v", res)
	}
	args, err := os.ReadFile(filepath.Join(home, "args.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(args), "--skip-git-repo-check") {
		t.Fatalf("Codex must accept non-Git agent homes, args were:\n%s", args)
	}
}
