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

// Die deutsche Fassung eines Bundles liegt als `<name>.de.bundle.json` neben
// der englischen, die den blanken Namen trägt (Basissprache). Zwei Dateien
// statt eines Bundles mit zwei Textfeldern, weil ein Bundle exakt das Format
// ist, das Import und Export sprechen — eine Sonderform nur für die Bibliothek
// müsste beide Wege mitpflegen.
const (
	bundleSuffix = ".bundle.json"
	deSuffix     = ".de.bundle.json"
)

// deFile: Dateiname der deutschen Fassung zu einer englischen.
func deFile(file string) string {
	return strings.TrimSuffix(file, bundleSuffix) + deSuffix
}

// namespaceBuiltin: fester Namespace für deterministische Vorlagen-IDs. Damit
// tragen die mitgelieferten Vorlagen über Neustarts und Instanzen hinweg
// stabile IDs (die Instanziierungs-URL /templates/{id}/instantiate bleibt gültig),
// ohne dass ein DB-Eintrag nötig wäre.
var namespaceBuiltin = uuid.MustParse("b0117e5e-c0de-4a11-a9e0-000000000001")

// Builtin ist eine mitgelieferte Vorlage: kuratierte Metadaten (zweisprachig)
// plus das eingebettete agentBundle-JSON, ebenfalls zweisprachig.
type Builtin struct {
	ID            uuid.UUID
	Key           string
	Name          string // englisch (Basissprache)
	Description   string // englisch (Basissprache)
	NameDe        string
	DescriptionDe string
	Bundle        json.RawMessage // englisch (Basissprache)
	BundleDe      json.RawMessage // leer, wenn es keine deutsche Fassung gibt
}

// istDeutsch: Sprachwahl der Oberfläche, wie sie am Endpunkt ankommt ("de",
// "de-DE", ""). Basissprache ist Englisch — alles ohne "de"-Präfix bleibt dort.
func istDeutsch(lang string) bool {
	return strings.HasPrefix(strings.ToLower(lang), "de")
}

// Localized liefert Name und Beschreibung in der passenden Sprache.
func (b Builtin) Localized(lang string) (name, description string) {
	if istDeutsch(lang) && b.NameDe != "" {
		return b.NameDe, b.DescriptionDe
	}
	return b.Name, b.Description
}

// LocalizedBundle liefert das Bundle in der passenden Sprache. Der Agent, den
// jemand aus der Bibliothek instanziiert, spricht dann die Sprache, in der er
// die Bibliothek gelesen hat — ein englischer Anzeigename über einem deutschen
// SOUL.md wäre die schlechtere Hälfte von beidem. Fehlt die deutsche Fassung,
// bleibt es bei der englischen; ein Agent mit Prompt ist besser als keiner.
func (b Builtin) LocalizedBundle(lang string) json.RawMessage {
	if istDeutsch(lang) && len(b.BundleDe) > 0 {
		return b.BundleDe
	}
	return b.Bundle
}

// manifest kuratiert Anzeigename, Beschreibung und Reihenfolge je Bundle-Datei.
// `File` nennt die englische Fassung (Basissprache); eine gleichnamige Datei
// mit `.de.` davor ist die deutsche Entsprechung und wird automatisch gefunden.
// Neue Beispiel-Bundles hier eintragen, damit sie in der Bibliothek auftauchen.
var manifest = []struct {
	File          string
	Name          string
	Description   string
	NameDe        string
	DescriptionDe string
}{
	{
		File:          "coding-agent.bundle.json",
		Name:          "Developer agent (GitLab)",
		Description:   "Picks up GitLab issues, verifies bugs against the source and has a sub-agent develop the fix inside the project checkout — where the project's own CLAUDE.md, skills and subagents apply. Commits the result and runs the review loop.",
		NameDe:        "Entwickler-Agent (GitLab)",
		DescriptionDe: "Nimmt GitLab-Issues auf, verifiziert Bugs am Quelltext und lässt den Fix von einem Sub-Agenten im Projekt-Checkout entwickeln — dort gelten CLAUDE.md, Skills und Subagenten des Projekts. Checkt das Ergebnis ein und führt den Review-Loop.",
	},
	{
		File:          "qa-agent.bundle.json",
		Name:          "QA / test agent (GitLab + email)",
		Description:   "Reviews others' merge requests end-to-end and turns emailed bug reports into GitLab tickets.",
		NameDe:        "QA-/Test-Agent (GitLab + E-Mail)",
		DescriptionDe: "Nimmt fremde Merge Requests als Reviewer end-to-end ab und legt Bug-Reports aus E-Mails als GitLab-Tickets an.",
	},
	{
		File:          "delivery-lead.bundle.json",
		Name:          "Delivery lead (GitLab milestone)",
		Description:   "Drives a GitLab milestone to its deadline: makes tickets implementable (reads the source requirement, writes testable acceptance criteria, points at the affected code), keeps dependent tickets in order, hands work to developer colleagues within a WIP limit and reports status. The engagement itself lives in a wiki brief, not in the config — the same agent runs the next one.",
		NameDe:        "Delivery Lead (GitLab-Meilenstein)",
		DescriptionDe: "Führt einen GitLab-Meilenstein zur Frist: macht Tickets implementierbar (Anforderung im Original lesen, prüfbare Abnahmekriterien, betroffene Codestellen), hält die Reihenfolge abhängiger Tickets, vergibt Arbeit nach WIP-Limit an die Entwickler-Kollegen und berichtet den Stand. Das Vorhaben selbst steht in einem Steckbrief im Wiki, nicht in der Config — derselbe Agent führt jedes weitere Vorhaben.",
	},
	{
		File:          "log-triage-agent.bundle.json",
		Name:          "Log triage agent (email → GitLab)",
		Description:   "Analyzes logs reported by email, checks for duplicates, files GitLab tickets for relevant findings and hands real code bugs to a developer agent.",
		NameDe:        "Log-Triage-Agent (E-Mail → GitLab)",
		DescriptionDe: "Analysiert per E-Mail gemeldete Logs, prüft auf Duplikate, legt für relevante Befunde GitLab-Tickets an und übergibt echte Code-Bugs an einen Entwickler-Agenten.",
	},
	{
		File:          "web-researcher.bundle.json",
		Name:          "Web researcher (browser)",
		Description:   "Researches questions on the open web with a real browser: searches, opens and reads sources, captures evidence as screenshots and delivers a concise, sourced answer.",
		NameDe:        "Web-Rechercheur (Browser)",
		DescriptionDe: "Recherchiert Fragen im offenen Web mit einem echten Browser: sucht, öffnet und liest Quellen, hält Belege als Screenshot fest und liefert eine belegte, knappe Antwort mit Quellen.",
	},
}

// builtins wird einmalig beim Package-Load gebaut; malformte eingebettete
// Bundles lassen den Prozess sofort scheitern (fail fast, es sind unsere Dateien).
var builtins = mustBuild()

func mustBuild() []Builtin {
	out := make([]Builtin, 0, len(manifest))
	for _, m := range manifest {
		raw := mustRead(m.File)
		// Die deutsche Fassung ist optional: Ein neues Bundle darf englisch
		// beginnen, ohne dass die Bibliothek deshalb scheitert. Ist sie da,
		// muss sie valide sein — halb übersetzt ist schlimmer als gar nicht.
		var rawDe json.RawMessage
		if b, err := bundleFS.ReadFile(deFile(m.File)); err == nil {
			if !json.Valid(b) {
				panic(fmt.Sprintf("examples: Bundle %q ist kein valides JSON", deFile(m.File)))
			}
			rawDe = json.RawMessage(b)
		}
		key := "builtin:" + strings.TrimSuffix(m.File, bundleSuffix)
		out = append(out, Builtin{
			ID:            uuid.NewSHA1(namespaceBuiltin, []byte(key)),
			Key:           key,
			Name:          m.Name,
			Description:   m.Description,
			NameDe:        m.NameDe,
			DescriptionDe: m.DescriptionDe,
			Bundle:        json.RawMessage(raw),
			BundleDe:      rawDe,
		})
	}
	return out
}

func mustRead(file string) []byte {
	raw, err := bundleFS.ReadFile(file)
	if err != nil {
		panic(fmt.Sprintf("examples: eingebettetes Bundle %q nicht lesbar: %v", file, err))
	}
	if !json.Valid(raw) {
		panic(fmt.Sprintf("examples: Bundle %q ist kein valides JSON", file))
	}
	return raw
}

// Builtins liefert die mitgelieferten Vorlagen in kuratierter Reihenfolge.
func Builtins() []Builtin { return builtins }
