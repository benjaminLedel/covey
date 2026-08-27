package httpapi

import (
	"testing"

	"covey/internal/runner"
)

// Ob ein Host abgelehnt hat, WEIL er Sandboxen trägt, entscheidet, ob aus der
// Absage ein Plan wird. Das Feld ist die Auskunft; der Satz ist der Rückfall
// für einen Runner, der von vor dem Feld stammt — und genau der ist der
// Regelfall dieser Funktion: ein Host, der gerade nicht aktualisiert werden
// kann, läuft seit einer Weile auf dem alten Binary.
func TestBeschaeftigtErkennenAuchOhneDasNeueFeld(t *testing.T) {
	faelle := []struct {
		name string
		res  runner.UpdateResult
		will bool
	}{
		{"neues Feld", runner.UpdateResult{Busy: true, Err: "this host is carrying 1 sandbox(es)"}, true},
		{"alter Runner, nur der Satz", runner.UpdateResult{Err: "this host is carrying 2 sandbox(es) — an update would leave them unwatched"}, true},
		{"anderer Fehler", runner.UpdateResult{Err: "download failed: 404"}, false},
		{"alles gut", runner.UpdateResult{From: "v0.8.0", To: "v0.8.1", Restarting: true}, false},
	}
	for _, f := range faelle {
		if got := runnerIsBusy(f.res); got != f.will {
			t.Errorf("%s: %v statt %v", f.name, got, f.will)
		}
	}
}
