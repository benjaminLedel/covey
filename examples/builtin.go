// Package examples provides the bundled agent bundles as a fixed template
// library. The *.bundle.json in this directory are the single source of truth:
// they are pulled into the binary via //go:embed and thereby show up in the
// template library (spec: config-as-code) — while remaining available as files
// for a manual bundle import.
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

// The German version of a bundle sits next to the English one as
// `<name>.de.bundle.json`; the English one carries the bare name (base
// language). Two files instead of one bundle with two text fields, because a
// bundle is exactly the format import and export speak — a special form just
// for the library would have to be maintained on both paths.
const (
	bundleSuffix = ".bundle.json"
	deSuffix     = ".de.bundle.json"
)

// deFile: file name of the German version belonging to an English one.
func deFile(file string) string {
	return strings.TrimSuffix(file, bundleSuffix) + deSuffix
}

// namespaceBuiltin: fixed namespace for deterministic template IDs. It gives
// the bundled templates stable IDs across restarts and instances (the
// instantiation URL /templates/{id}/instantiate stays valid) without needing a
// database row.
var namespaceBuiltin = uuid.MustParse("b0117e5e-c0de-4a11-a9e0-000000000001")

// Builtin is a bundled template: curated metadata (bilingual) plus the
// embedded agentBundle JSON, likewise bilingual.
type Builtin struct {
	ID            uuid.UUID
	Key           string
	Name          string // English (base language)
	Description   string // English (base language)
	NameDe        string
	DescriptionDe string
	Bundle        json.RawMessage // English (base language)
	BundleDe      json.RawMessage // empty when there is no German version
}

// isGerman: the UI's language choice as it arrives at the endpoint ("de",
// "de-DE", ""). The base language is English — anything without a "de" prefix
// stays there.
func isGerman(lang string) bool {
	return strings.HasPrefix(strings.ToLower(lang), "de")
}

// Localized returns name and description in the matching language.
func (b Builtin) Localized(lang string) (name, description string) {
	if isGerman(lang) && b.NameDe != "" {
		return b.NameDe, b.DescriptionDe
	}
	return b.Name, b.Description
}

// LocalizedBundle returns the bundle in the matching language. The agent
// somebody instantiates from the library then speaks the language they read
// the library in — an English display name above a German SOUL.md would be the
// worse half of both. If the German version is missing, the English one stays;
// an agent with a prompt beats no agent at all.
func (b Builtin) LocalizedBundle(lang string) json.RawMessage {
	if isGerman(lang) && len(b.BundleDe) > 0 {
		return b.BundleDe
	}
	return b.Bundle
}

// manifest curates display name, description and ordering per bundle file.
// `File` names the English version (base language); a file of the same name
// with `.de.` in front of the suffix is the German counterpart and is found
// automatically. Register new example bundles here so they appear in the
// library.
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
	{
		File:          "dependency-security-agent.bundle.json",
		Name:          "Dependency security agent (vulndb + GitLab)",
		Description:   "Scans the lock files of the projects in its register for known vulnerabilities (npm, Composer, Dart/Flutter), assesses every hit against the project — direct or transitive, which fix branch applies — and files traceable GitLab tickets with evidence, after a mandatory duplicate check. The upgrade goes to a developer colleague.",
		NameDe:        "Dependency-Security-Agent (vulndb + GitLab)",
		DescriptionDe: "Scannt die Lockdateien der Projekte aus seinem Verzeichnis auf bekannte Schwachstellen (npm, Composer, Dart/Flutter), bewertet jeden Treffer am Projekt — direkt oder transitiv, welcher Fix-Zweig gilt — und legt nach verpflichtender Duplikatsprüfung belegte GitLab-Tickets an. Das Upgrade geht an einen Entwickler-Kollegen.",
	},
}

// builtins is built once at package load; malformed embedded bundles fail the
// process immediately (fail fast — they are our own files).
var builtins = mustBuild()

func mustBuild() []Builtin {
	out := make([]Builtin, 0, len(manifest))
	for _, m := range manifest {
		raw := mustRead(m.File)
		// The German version is optional: a new bundle may start out English
		// without the library failing over it. If it is there, it has to be
		// valid — half translated is worse than not at all.
		var rawDe json.RawMessage
		if b, err := bundleFS.ReadFile(deFile(m.File)); err == nil {
			if !json.Valid(b) {
				panic(fmt.Sprintf("examples: bundle %q is not valid JSON", deFile(m.File)))
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
		panic(fmt.Sprintf("examples: embedded bundle %q not readable: %v", file, err))
	}
	if !json.Valid(raw) {
		panic(fmt.Sprintf("examples: bundle %q is not valid JSON", file))
	}
	return raw
}

// Builtins returns the bundled templates in curated order.
func Builtins() []Builtin { return builtins }
