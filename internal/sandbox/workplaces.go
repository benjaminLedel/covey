package sandbox

import (
	"embed"
	"encoding/json"
)

// The self-descriptions of the workplaces — the same files that are copied
// into the images (Dockerfile.sandbox, Dockerfile.sandbox.dev).
//
// One file, two consumers: the agent reads it at runtime in its
// sandbox (internal/daemon/workplace.go), the UI shows it when a
// workplace is selected. Two separate lists would be two different
// truths within a month.
//
//go:embed workplaces/*.json
var workplaceFS embed.FS

// WorkplaceDoc is the description as the UI outputs it. Deliberately the same
// field names as are read in the image — it is the same file.
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

// Workplace returns the description of a profile. For an own image (not a
// profile, but a reference) there is none — then the honest answer is that the
// platform does not know.
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
