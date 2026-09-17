package httpapi

import (
	"testing"

	"covey/internal/runner"
)

// Whether a host refused because it CARRIES sandboxes decides whether the
// refusal becomes a plan. The field is the information; the sentence is the
// fallback for a runner that predates the field — and that one is
// exactly the regular case here: a host that cannot be updated right
// now has been running on the old binary for a while.
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
