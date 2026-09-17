package orchestrator

import (
	"strings"
	"testing"
)

// An approved grant is the answer to the parameters a human read — not to
// the action. Since require_approval lets a meta-action park, and the agent
// REPEATS it afterwards, only the binding decides whether it has to repeat
// it with the same parameters.
func TestBindingUnterscheidetParameter(t *testing.T) {
	freigegeben := map[string]any{"op": "create_agent", "slug": "helper", "display_name": "Helper"}
	andere := map[string]any{"op": "create_agent", "slug": "backdoor", "display_name": "Helper"}

	if bindingOf(freigegeben) == bindingOf(andere) {
		t.Fatal("ein anderer slug muss eine andere Bindung ergeben — sonst ist die Freigabe fuer den einen die Eintrittskarte fuer den anderen")
	}
	// The same parameters twice: consuming finds the grant only when the
	// binding is stable. Order in the literal must not matter.
	wieder := map[string]any{"display_name": "Helper", "slug": "helper", "op": "create_agent"}
	if bindingOf(freigegeben) != bindingOf(wieder) {
		t.Fatal("gleiche Parameter muessen dieselbe Bindung ergeben, sonst wird keine Freigabe je verbraucht")
	}
}

// The file list counts too: set_agent_config with other files is a different
// action, even when agent and action stay the same.
func TestBindingUmfasstDateien(t *testing.T) {
	a := map[string]any{"op": "set_agent_config", "agent": "kollege", "files": []string{"SOUL.md"}}
	b := map[string]any{"op": "set_agent_config", "agent": "kollege", "files": []string{"SOUL.md", "ACCESS.md"}}
	if bindingOf(a) == bindingOf(b) {
		t.Fatal("eine andere Dateiliste muss eine andere Bindung ergeben")
	}
}

// read_recording sets its own binding (the run it read). That stays readable
// up front, the fingerprint comes behind it — otherwise the binding in the
// approval dialog would be a string with no statement.
func TestBindingBehaeltDenLaufVorn(t *testing.T) {
	lauf := "3f1d9c02-0000-4000-8000-000000000001"
	got := bindingOf(map[string]any{"op": "read_recording", "agent": "kollege", "binding": lauf})
	if !strings.HasPrefix(got, lauf+":") {
		t.Fatalf("der Lauf muss vorne stehen: %q", got)
	}
	if got == lauf {
		t.Fatalf("der Fingerabdruck fehlt: %q", got)
	}
}

// Without a binding the grant would be a licence on the action. The empty
// string is therefore not an allowed result, not even for empty parameters.
func TestBindingIstNieLeer(t *testing.T) {
	if got := bindingOf(map[string]any{}); got == "" {
		t.Fatal("eine leere Bindung liesse die Freigabe fuer jede Wiederholung gelten")
	}
}
