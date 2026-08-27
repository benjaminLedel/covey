package sandbox

import (
	"embed"
	"encoding/json"
)

// Die Selbstbeschreibungen der Arbeitsplätze — dieselben Dateien, die in die
// Images kopiert werden (Dockerfile.sandbox, Dockerfile.sandbox.dev).
//
// Eine Datei, zwei Verbraucher: der Agent liest sie zur Laufzeit in seiner
// Sandbox (internal/daemon/workplace.go), die Oberfläche zeigt sie beim
// Auswählen eines Arbeitsplatzes. Zwei getrennte Listen wären in einem Monat
// zwei verschiedene Wahrheiten.
//
//go:embed workplaces/*.json
var workplaceFS embed.FS

// WorkplaceDoc ist die Beschreibung, wie die Oberfläche sie ausgibt. Bewusst
// dieselben Feldnamen wie im Image gelesen wird — es ist dieselbe Datei.
type WorkplaceDoc struct {
	Profile string `json:"profile"`
	Summary string `json:"summary"`
	Tools   []struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
		Note    string `json:"note,omitempty"`
	} `json:"tools"`
	SDKDirs map[string]string `json:"sdk_dirs,omitempty"`
	Notes   []string          `json:"notes,omitempty"`
}

// Workplace liefert die Beschreibung eines Profils. Für ein eigenes Image
// (kein Profil, sondern eine Referenz) gibt es keine — dann ist die ehrliche
// Antwort, dass die Plattform es nicht weiß.
func Workplace(profile string) (WorkplaceDoc, bool) {
	raw, err := workplaceFS.ReadFile("workplaces/" + profile + ".json")
	if err != nil {
		return WorkplaceDoc{}, false
	}
	var d WorkplaceDoc
	if err := json.Unmarshal(raw, &d); err != nil {
		return WorkplaceDoc{}, false
	}
	return d, true
}
