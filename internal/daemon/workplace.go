package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Workplace describes itself.
//
// An agent was put into a workshop and was not told what stands in it. It
// found out by trying, and where trying is expensive it built its own
// instead: one home held `tools/jdk`, `tools/jdk21` and `tools/flutter` —
// 2.7 GB of tools that the image has shipped since 10 August. Its home is
// written back after every run; the duplication costs not once, but
// every time.
//
// The description lives IN the image (sandbox/workplaces/<profile>.json, copied
// there), and it is read here, in the sandbox. No protocol field, no way over
// the control plane: what the image can do, the image knows.
const workplacePath = "/etc/covey/workplace.json"

// Workplace is the curated answer — written by hand, not generated from a
// package list. A generated list would be complete and would not be
// read; here stands what can be relied on.
type Workplace struct {
	Profile string `json:"profile"`
	Summary string `json:"summary"`
	Tools   []struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
		Note    string `json:"note,omitempty"`
	} `json:"tools"`
	// SDKDirs are the version managers and the place where they keep their
	// SDKs. They are the reason an agent does not have to fetch anything
	// itself — and the place where it looks up what is already there.
	SDKDirs map[string]string `json:"sdk_dirs,omitempty"`
	Notes   []string          `json:"notes,omitempty"`
}

// WorkplaceContext is the paragraph appended to the system prompt — empty
// when the image brings no description (a foreign image, an older one, a
// self-built one). Saying nothing is better than claiming something.
//
// No cache, and that is a decision: in operation coveyd is one process
// per sandbox, so reading once would be right — in the
// integration stack the same daemon runs in one process with everything else,
// and a process-wide cache would make the first run the truth for all
// following ones. One small file per task start is the cheaper price than a
// state that leaks between tests.
func WorkplaceContext() string {
	return readWorkplace(workplacePathFromEnv())
}

func workplacePathFromEnv() string {
	if p := strings.TrimSpace(os.Getenv("COVEY_WORKPLACE_FILE")); p != "" {
		return p
	}
	return workplacePath
}

func readWorkplace(path string) string {
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed path in the image, test override via Env
	if err != nil {
		return ""
	}
	var w Workplace
	if err := json.Unmarshal(raw, &w); err != nil {
		return ""
	}
	return w.Render()
}

// Render turns the description into the paragraph that an agent reads. Kept
// short: it stands in the system prompt in EVERY run, and what is too long
// there pushes other things out.
func (w Workplace) Render() string {
	if w.Profile == "" && len(w.Tools) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## Your workplace")
	if w.Profile != "" {
		fmt.Fprintf(&b, " (%s)", w.Profile)
	}
	b.WriteString("\n\n")
	if w.Summary != "" {
		b.WriteString(w.Summary + "\n\n")
	}
	if len(w.Tools) > 0 {
		b.WriteString("Installed and ready:\n")
		for _, t := range w.Tools {
			b.WriteString("- " + t.Name)
			if t.Version != "" {
				b.WriteString(" " + t.Version)
			}
			if t.Note != "" {
				b.WriteString(" — " + t.Note)
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	if len(w.SDKDirs) > 0 {
		b.WriteString("Version managers (the SDKs live in your home, not in the image):\n")
		namen := make([]string, 0, len(w.SDKDirs))
		for n := range w.SDKDirs {
			namen = append(namen, n)
		}
		sort.Strings(namen)
		for _, n := range namen {
			b.WriteString("- " + n + ": " + w.SDKDirs[n] + "\n")
		}
		b.WriteString("\n")
	}
	for _, n := range w.Notes {
		b.WriteString(n + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
