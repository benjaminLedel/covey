// Package examples liefert die mitgelieferten Agenten-Bundles als feste
// Vorlagen-Bibliothek. Die *.bundle.json in diesem Verzeichnis sind die
// Single Source of Truth: sie werden per //go:embed ins Binary gezogen und
// erscheinen dadurch in der Vorlagen-Bibliothek (spec: Config-as-Code) — und
// bleiben zugleich als Dateien zum manuellen Bundle-Import erhalten.
package examples

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

//go:embed *.bundle.json
var bundleFS embed.FS

// namespaceBuiltin: fester Namespace für deterministische Vorlagen-IDs. Damit
// tragen die mitgelieferten Vorlagen über Neustarts und Instanzen hinweg
// stabile IDs (die Instanziierungs-URL /templates/{id}/instantiate bleibt gültig),
// ohne dass ein DB-Eintrag nötig wäre.
var namespaceBuiltin = uuid.MustParse("b0117e5e-c0de-4a11-a9e0-000000000001")

// Builtin ist eine mitgelieferte Vorlage: kuratierte Metadaten (zweisprachig)
// plus das eingebettete agentBundle-JSON.
type Builtin struct {
	ID            uuid.UUID
	Key           string
	Name          string // deutsch (Default)
	Description   string // deutsch (Default)
	NameEn        string
	DescriptionEn string
	Bundle        json.RawMessage
}

// Localized liefert Name und Beschreibung in der passenden Sprache. Alles außer
// einem "en"-Präfix fällt auf Deutsch zurück (Spec/Doku sind deutsch).
func (b Builtin) Localized(lang string) (name, description string) {
	if strings.HasPrefix(strings.ToLower(lang), "en") && b.NameEn != "" {
		return b.NameEn, b.DescriptionEn
	}
	return b.Name, b.Description
}

// manifest kuratiert Anzeigename, Beschreibung und Reihenfolge je Bundle-Datei.
// Neue Beispiel-Bundles hier eintragen, damit sie in der Bibliothek auftauchen.
var manifest = []struct {
	File          string
	Name          string
	Description   string
	NameEn        string
	DescriptionEn string
}{
	{
		File:          "coding-agent.bundle.json",
		Name:          "Entwickler-Agent (GitLab)",
		Description:   "Nimmt GitLab-Issues auf, verifiziert Bugs am Quelltext, entwickelt Fixes und eröffnet Merge Requests im Review-Loop.",
		NameEn:        "Developer agent (GitLab)",
		DescriptionEn: "Picks up GitLab issues, verifies bugs against the source, develops fixes and opens merge requests in the review loop.",
	},
	{
		File:          "qa-agent.bundle.json",
		Name:          "QA-/Test-Agent (GitLab + E-Mail)",
		Description:   "Nimmt fremde Merge Requests als Reviewer end-to-end ab und legt Bug-Reports aus E-Mails als GitLab-Tickets an.",
		NameEn:        "QA / test agent (GitLab + email)",
		DescriptionEn: "Reviews others' merge requests end-to-end and turns emailed bug reports into GitLab tickets.",
	},
}

// builtins wird einmalig beim Package-Load gebaut; malformte eingebettete
// Bundles lassen den Prozess sofort scheitern (fail fast, es sind unsere Dateien).
var builtins = mustBuild()

func mustBuild() []Builtin {
	out := make([]Builtin, 0, len(manifest))
	for _, m := range manifest {
		raw, err := bundleFS.ReadFile(m.File)
		if err != nil {
			panic(fmt.Sprintf("examples: eingebettetes Bundle %q nicht lesbar: %v", m.File, err))
		}
		if !json.Valid(raw) {
			panic(fmt.Sprintf("examples: Bundle %q ist kein valides JSON", m.File))
		}
		key := "builtin:" + strings.TrimSuffix(m.File, ".bundle.json")
		out = append(out, Builtin{
			ID:            uuid.NewSHA1(namespaceBuiltin, []byte(key)),
			Key:           key,
			Name:          m.Name,
			Description:   m.Description,
			NameEn:        m.NameEn,
			DescriptionEn: m.DescriptionEn,
			Bundle:        json.RawMessage(raw),
		})
	}
	return out
}

// Builtins liefert die mitgelieferten Vorlagen in kuratierter Reihenfolge.
func Builtins() []Builtin { return builtins }
