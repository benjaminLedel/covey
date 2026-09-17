package daemon

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

/* The call that every compiled prompt teaches is
   `curl -s -X POST http://localhost:$COVEY_ACTION_PORT/actions/<system>/<action>`.
   It has to work in the shell an agent starts via `dev exec`/`dev start` —
   and it must not work where work is done hermetically. Both halves hang
   on the same spot, which is why they stand here
   side by side. */

// The dev plugin starts its shell like this: exec.Command without its own env, so
// it inherits the environment of the daemon process. If that does not carry the
// port number, the agent's call goes to port 80 and disappears.
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

// The counter-check, and the reason why setting it in the process environment
// is safe: children of the runtime get their environment via childEnv, and
// that removes EVERY COVEY_* variable. What a run needs is added
// expressly — not by inheriting.
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
	// What is not named COVEY_ stays: otherwise a sub-run would stand without PATH.
	if !hasPrefix(env, "PATH=") {
		t.Error("childEnv verliert das übrige Environment")
	}
	// And what a run is expressly given is there.
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
