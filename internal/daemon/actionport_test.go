package daemon

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

/* Der Aufruf, den jeder kompilierte Prompt lehrt, ist
   `curl -s -X POST http://localhost:$COVEY_ACTION_PORT/actions/<system>/<action>`.
   Er muss in der Shell funktionieren, die ein Agent über `dev exec`/`dev start`
   startet — und er darf dort NICHT funktionieren, wo hermetisch gearbeitet
   wird. Beide Hälften hängen an derselben Stelle, deshalb stehen sie hier
   nebeneinander. */

// Das dev-Plugin startet seine Shell so: exec.Command ohne eigenes Env, also
// erbt sie das Environment des Daemon-Prozesses. Wenn der die Portnummer nicht
// trägt, geht der Aufruf des Agenten an Port 80 und verschwindet.
func TestEineShellDesDaemonsErbtDenActionPort(t *testing.T) {
	t.Setenv("COVEY_ACTION_PORT", "43117")

	out, err := exec.Command("sh", "-c", "echo $COVEY_ACTION_PORT").Output()
	if err != nil {
		t.Fatalf("shell: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "43117" {
		t.Errorf("die Shell sieht den Action-Port nicht: %q", got)
	}
}

// Die Gegenprobe, und der Grund, warum das Setzen im Prozess-Environment
// gefahrlos ist: Kinder der Runtime bekommen ihr Environment über childEnv, und
// das entfernt JEDE COVEY_*-Variable. Was ein Lauf braucht, kommt ausdrücklich
// dazu — nicht durch Erben.
func TestChildEnvHaeltCoveyVariablenDraussen(t *testing.T) {
	t.Setenv("COVEY_ACTION_PORT", "43117")
	t.Setenv("COVEY_DAEMON_TOKEN", "geheim")
	t.Setenv("PATH", os.Getenv("PATH"))

	env := childEnv()
	for _, kv := range env {
		if strings.HasPrefix(kv, "COVEY_") {
			t.Errorf("childEnv reicht eine Plattform-Variable durch: %s", strings.SplitN(kv, "=", 2)[0])
		}
	}
	// Was nicht COVEY_ heißt, bleibt: sonst stünde ein Sub-Lauf ohne PATH da.
	if !hasPrefix(env, "PATH=") {
		t.Error("childEnv verliert das übrige Environment")
	}
	// Und was ein Lauf ausdrücklich mitbekommt, ist da.
	if !hasPrefix(childEnv("COVEY_ACTION_PORT=43117"), "COVEY_ACTION_PORT=43117") {
		t.Error("childEnv nimmt die ausdrücklich übergebene Variable nicht auf")
	}
}

func hasPrefix(env []string, prefix string) bool {
	for _, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			return true
		}
	}
	return false
}
