package orchestrator

import (
	"strings"
	"testing"
)

// Eine erteilte Freigabe ist die Antwort auf die Parameter, die ein Mensch
// gelesen hat — nicht auf die Aktion. Seit require_approval eine Meta-Action
// parken lässt und der Agent sie danach WIEDERHOLT, entscheidet allein die
// Bindung darüber, ob er sie mit denselben Parametern wiederholen muss.
func TestBindingUnterscheidetParameter(t *testing.T) {
	freigegeben := map[string]any{"op": "create_agent", "slug": "helper", "display_name": "Helper"}
	andere := map[string]any{"op": "create_agent", "slug": "backdoor", "display_name": "Helper"}

	if bindingOf(freigegeben) == bindingOf(andere) {
		t.Fatal("ein anderer slug muss eine andere Bindung ergeben — sonst ist die Freigabe fuer den einen die Eintrittskarte fuer den anderen")
	}
	// Dieselben Parameter zweimal: der Verbrauch findet die Freigabe nur, wenn
	// die Bindung stabil ist. Reihenfolge im Literal darf keine Rolle spielen.
	wieder := map[string]any{"display_name": "Helper", "slug": "helper", "op": "create_agent"}
	if bindingOf(freigegeben) != bindingOf(wieder) {
		t.Fatal("gleiche Parameter muessen dieselbe Bindung ergeben, sonst wird keine Freigabe je verbraucht")
	}
}

// Auch die Dateiliste zählt: set_agent_config mit anderen Dateien ist eine
// andere Handlung, selbst wenn Agent und Aktion gleich bleiben.
func TestBindingUmfasstDateien(t *testing.T) {
	a := map[string]any{"op": "set_agent_config", "agent": "kollege", "files": []string{"SOUL.md"}}
	b := map[string]any{"op": "set_agent_config", "agent": "kollege", "files": []string{"SOUL.md", "ACCESS.md"}}
	if bindingOf(a) == bindingOf(b) {
		t.Fatal("eine andere Dateiliste muss eine andere Bindung ergeben")
	}
}

// read_recording setzt seine Bindung selbst (den gelesenen Lauf). Die bleibt
// vorne lesbar stehen, der Fingerabdruck kommt dahinter — sonst wäre die
// Bindung im Freigabe-Dialog eine Zeichenkette ohne Aussage.
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

// Ohne Bindung wäre die Freigabe eine Lizenz auf die Aktion. Der leere String
// ist deshalb kein zulässiges Ergebnis, auch nicht für leere Parameter.
func TestBindingIstNieLeer(t *testing.T) {
	if got := bindingOf(map[string]any{}); got == "" {
		t.Fatal("eine leere Bindung liesse die Freigabe fuer jede Wiederholung gelten")
	}
}
