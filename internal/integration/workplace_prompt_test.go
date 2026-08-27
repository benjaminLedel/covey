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

/* Ein Agent stand in einer Werkstatt und bekam nicht gesagt, was darin steht.
   In einem Home lagen `tools/jdk`, `tools/jdk21` und `tools/flutter` — 2,7 GB
   Werkzeuge, die das Image seit Wochen mitbringt, und das Home wird nach jedem
   Lauf zurückgeschrieben (#102).

   Geprüft wird hier der ganze Weg: die Datei liegt im Image, der Daemon liest
   sie in der Sandbox und hängt sie an den Systemprompt — nicht die Steuerebene,
   die das Image nicht einmal dem Namen nach kennen muss. */

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
	// Im Betrieb liegt sie unter /etc/covey/workplace.json IM Image; der
	// eingebaute Daemon dieses Stapels liest denselben Pfad aus der Umgebung.
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
	// Und die Konfiguration des Agenten steht weiterhin darin — angehängt,
	// nicht ersetzt.
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
