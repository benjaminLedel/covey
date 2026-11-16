package main

import (
	"os"
	"runtime"
	"testing"
)

// TestDaemonEnvLeavesNothingToInherit: once read, the daemon's connection to
// the control plane is no longer in the environment any child would inherit.
func TestDaemonEnvLeavesNothingToInherit(t *testing.T) {
	t.Setenv("COVEY_WS_URL", "wss://control.example.org/daemon")
	t.Setenv("COVEY_DAEMON_TOKEN", "daemon-secret")
	t.Setenv("COVEY_AGENT_ID", "0000")
	ws, tok, id := daemonEnv()
	if ws == "" || tok != "daemon-secret" || id != "0000" {
		t.Fatalf("daemonEnv = %q %q %q", ws, tok, id)
	}
	for _, k := range []string{"COVEY_WS_URL", "COVEY_DAEMON_TOKEN"} {
		if _, ok := os.LookupEnv(k); ok {
			t.Errorf("%s is still in the environment", k)
		}
	}
}

// TestProtectProcessClearsDumpable: on Linux the daemon must not be readable
// through /proc by the other processes of the sandbox user.
func TestProtectProcessClearsDumpable(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("prctl is Linux only")
	}
	if err := protectProcess(); err != nil {
		t.Fatal(err)
	}
	if processDumpable() {
		t.Fatal("process is still dumpable after protectProcess")
	}
}
