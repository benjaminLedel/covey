package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"covey/internal/backlog"
)

/* An agent stood in a workshop and was not told what stands in it. In one
   home lay `tools/jdk`, `tools/jdk21` and `tools/flutter` — 2.7 GB of tools
   the image has brought along for weeks, and the home is written back after
   every run (#102).

   Checked here is the whole path: the file lies in the image, the daemon
   reads it in the sandbox and appends it to the system prompt — not the
   control plane, which need not know the image even by name. */

func TestDerLaufKenntSeinenArbeitsplatz(t *testing.T) {
	beschreibung := filepath.Join(t.TempDir(), "workplace.json")
	if err := os.WriteFile(beschreibung, []byte(`{
	  "profile":"dev",
	  "summary":"Die Werkbank dieses Tests.",
	  "tools":[{"name":"openjdk","version":"21","note":"JAVA_HOME=/opt/java"}],
	  "sdk_dirs":{"fvm":"~/fvm — Flutter-SDKs, nicht im Image"},
	  "notes":["Kein root, kein apt."]
	}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// In operation it lies under /etc/covey/workplace.json IN the image; the
	// built-in daemon of this stack reads the same path from the environment.
	t.Setenv("COVEY_WORKPLACE_FILE", beschreibung)

	ctx := context.Background()
	s := newStack(t)
	agent := s.newSupportAgent("kennt-seinen-platz")

	task, err := s.backlog.Create(ctx, s.orgID, agent.ID, "Prompt zeigen", "[mock:prompt]", "manual", 3)
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "task done", 30*time.Second, func() bool {
		return s.taskState(task.ID) == backlog.StateDone
	})
	got, err := s.backlog.Get(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Result == nil {
		t.Fatal("der Lauf liefert keinen Systemprompt")
	}
	prompt := *got.Result

	for _, muss := range []string{"Your workplace", "openjdk 21", "~/fvm", "Kein root"} {
		if !strings.Contains(prompt, muss) {
			t.Fatalf("%q steht nicht im Prompt des Laufs:\n%s", muss, kürzen(prompt))
		}
	}
	// And the configuration of the agent still stands in it — appended, not
	// replaced.
	if !strings.Contains(prompt, agent.DisplayName) {
		t.Fatalf("die Konfiguration des Agenten fehlt:\n%s", kürzen(prompt))
	}
}

func kürzen(s string) string {
	if len(s) <= 1500 {
		return s
	}
	return s[:700] + "\n…\n" + s[len(s)-700:]
}
