package daemon

import (
	"encoding/json"
	"os"
	"testing"

	"covey/internal/engines"
)

// The catalogue names engines, and a name this build does not register is
// carried along and ignored rather than refused — that is what lets a newer
// document serve an older covey. The same property turns a typo into a silent
// nothing: the runner installs a CLI nobody will ever start, the doctor says the
// engine is unknown here, and the operator is left between two sources that
// contradict each other. So the published document is checked against the
// registry that has to answer for it, and the variable it promises is checked
// against the one the adapter reads.
func TestShippedCatalogueNamesEnginesThisBuildRegisters(t *testing.T) {
	body, err := os.ReadFile("../engines/engine-catalog.json")
	if err != nil {
		t.Fatalf("reading the catalogue: %v", err)
	}
	var cat engines.Catalog
	if err := json.Unmarshal(body, &cat); err != nil {
		t.Fatalf("the catalogue has to parse here too: %v", err)
	}
	for _, e := range cat.Engines {
		d, ok := Describe(e.Name)
		if !ok {
			t.Errorf("%s: no engine of this build — the catalogue would install a CLI nothing starts",
				e.Name)
			continue
		}
		last := e.Versions[len(e.Versions)-1]
		if last.BinaryEnv != "" && CLI(e.Name).Env != last.BinaryEnv {
			t.Errorf("%s: the catalogue sets %s, the adapter reads %s — the run would not find its CLI",
				e.Name, last.BinaryEnv, CLI(e.Name).Env)
		}
		if last.Binary != "" && d.CLI.Name == "" {
			t.Errorf("%s: the catalogue names a binary %q, the descriptor says no CLI is needed",
				e.Name, last.Binary)
		}
	}
}
