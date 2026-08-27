package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Der Arbeitsplatz beschreibt sich selbst.
//
// Ein Agent wurde bisher in eine Werkstatt gestellt und bekam nicht gesagt, was
// darin steht. Er fand es durch Versuchen heraus, und wo Versuchen teuer ist,
// baute er sich lieber eigenes: In einem Home lagen `tools/jdk`, `tools/jdk21`
// und `tools/flutter` — 2,7 GB Werkzeuge, die das Image seit dem 10. August
// mitbringt. Sein Home wird nach jedem Lauf zurückgeschrieben; die Doppelung
// kostet also nicht einmal, sondern immer.
//
// Die Beschreibung liegt IM Image (sandbox/workplaces/<profil>.json, dorthin
// kopiert), und gelesen wird sie hier, in der Sandbox. Kein Protokollfeld, kein
// Weg über die Steuerebene: Was das Image kann, weiß das Image.
const workplacePath = "/etc/covey/workplace.json"

// Workplace ist die kuratierte Auskunft — von Hand geschrieben, nicht aus einer
// Paketliste erzeugt. Eine erzeugte Liste wäre vollständig und würde nicht
// gelesen; hier steht, worauf man sich verlassen kann.
type Workplace struct {
	Profile string `json:"profile"`
	Summary string `json:"summary"`
	Tools   []struct {
		Name    string `json:"name"`
		Version string `json:"version,omitempty"`
		Note    string `json:"note,omitempty"`
	} `json:"tools"`
	// SDKDirs sind die Versionsmanager und der Ort, an dem sie ihre SDKs
	// ablegen. Sie sind der Grund, warum ein Agent nichts selbst holen muss —
	// und der Ort, an dem er nachsieht, was schon da ist.
	SDKDirs map[string]string `json:"sdk_dirs,omitempty"`
	Notes   []string          `json:"notes,omitempty"`
}

// WorkplaceContext ist der Absatz, der an den Systemprompt gehängt wird — leer,
// wenn das Image keine Beschreibung mitbringt (ein fremdes Image, ein älteres,
// ein selbst gebautes). Nichts zu sagen ist besser als etwas zu behaupten.
//
// Ohne Cache, und das ist eine Entscheidung: Im Betrieb ist coveyd ein eigener
// Prozess je Sandbox, ein einmaliges Lesen wäre also richtig — im
// Integrationsstapel läuft derselbe Daemon in einem Prozess mit allem anderen,
// und ein prozessweiter Cache machte den ersten Lauf zur Wahrheit für alle
// folgenden. Eine kleine Datei je Aufgabenstart ist der billigere Preis als ein
// Zustand, der zwischen Tests hindurchleckt.
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
	raw, err := os.ReadFile(path) // #nosec G304 -- fester Pfad im Image, Test-Override über Env
	if err != nil {
		return ""
	}
	var w Workplace
	if err := json.Unmarshal(raw, &w); err != nil {
		return ""
	}
	return w.Render()
}

// Render macht aus der Beschreibung den Absatz, den ein Agent liest. Kurz
// gehalten: Er steht in JEDEM Lauf im Systemprompt, und was dort zu lang ist,
// verdrängt anderes.
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
