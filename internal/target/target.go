// Package target definiert das Plugin-Interface für Zielsysteme (Zammad,
// GitLab, …) — dasselbe Muster wie die Runtime-Registry in internal/daemon:
// ein Zielsystem = ein Unterpackage, das sich in init() via Register einträgt.
// Control Plane (Webhook-Eingang, Prompt-Doku, UI) und Daemon (Action-
// Ausführung) lesen dieselbe Registry; es gibt keine hartkodierte Liste.
//
// Schlanke Auslieferung: Ein kompiliertes Plugin wird nur eingebunden, wenn
// ein Binary es blank-importiert (cmd/covey, cmd/coveyd). Wer Covey ohne
// Zammad ausliefern will, lässt den Import weg. Daneben gibt es zwei
// Laufzeit-Plugin-Typen ganz ohne Neukompilieren:
//
//   - Manifest-Plugins (kind=custom, JSON-Upload, siehe manifest.go): eine
//     generische REST-Engine interpretiert das Manifest.
//   - MCP-Plugins (kind=mcp, siehe Unterpaket mcp): ein angebundener
//     MCP-Server, dessen Tools entdeckt (tools/list) und über den Action-Proxy
//     aufgerufen (tools/call) werden.
//
// Alle drei erfüllen dieselbe System-Schnittstelle — Broker, Guard-Rails und
// Recording greifen identisch, egal woher das Plugin stammt.
package target

import (
	"context"
	"encoding/json"
	"net/http"
)

// System ist ein angebundenes Zielsystem. Es bündelt die
// Integrationsflächen (spec/13): Aktionen und Prompt-Doku. Der
// Webhook-Eingang ist optional (siehe Webhooker) — ein Zielsystem, das rein
// per Polling/Heartbeat aufnimmt (z. B. GitLab), implementiert ihn nicht.
type System interface {
	Name() string

	// ActionSubject mappt Aktion+Params auf das Guard-Rail-Subjekt
	// (z. B. reply mit internal=false → "zammad:reply_external").
	ActionSubject(action string, params json.RawMessage) string

	// Execute führt eine Agent-Aktion mit gebrokerten Credentials aus
	// (Daemon-Seite). Die Credentials kommen pro Aufruf aus dem Broker —
	// sie werden nie persistiert.
	Execute(ctx context.Context, action string, params json.RawMessage, cred Credential) (any, error)

	// PromptDoc beschreibt die verfügbaren Aktionen für den System-Prompt
	// des Agenten (wird in die Plattform-Protokoll-Sektion kompiliert).
	PromptDoc() string
}

// Webhooker ist ein optionales Plugin-Interface für Zielsysteme mit
// eingehendem Webhook (Zammad, Manifest-Plugins). Der Event-Router
// (httpapi.handleTargetWebhook) prüft darauf; ein System ohne Webhooker
// beantwortet Webhook-Posts fail-closed mit 404. Zielsysteme, die rein per
// Polling aufnehmen, lassen dieses Interface weg — dann gibt es keinen
// eingehenden Traffic, keine öffentliche URL und kein Webhook-Secret.
type Webhooker interface {
	// VerifyWebhook prüft die Integrität eines rohen Webhook-Payloads
	// (z. B. HMAC-Signatur). Leeres Secret = Prüfung deaktiviert (Dev).
	VerifyWebhook(secret string, body []byte, header http.Header) bool

	// ParseWebhook macht aus dem Payload das Wake-Event für den Orchestrator.
	ParseWebhook(body []byte) (WebhookEvent, error)
}

// Credential ist das gebrokerte Zugangs-Paar für ein Zielsystem.
type Credential struct {
	BaseURL string
	Token   string
}

// WorkChecker ist ein optionales Plugin-Interface für Systeme ohne Webhook:
// ein billiger Vorab-Check der Control Plane, ob überhaupt Arbeit vorliegt
// (z. B. ungelesene Mails per IMAP). Heartbeat-Einträge mit nur-wenn: <system>
// feuern nur, wenn HasWork true liefert — sonst entfällt der Lauf und damit
// der (teure) Agenten-Wake. Der Check läuft in der Control Plane, das
// Credential verlässt sie dabei nicht.
type WorkChecker interface {
	HasWork(ctx context.Context, cred Credential) (bool, error)
}

// KindWorkChecker verfeinert WorkChecker für Zielsysteme mit mehreren
// Arbeits-Arten (z. B. GitLab: Issues vs. Merge-Request-Reviews). Ein
// Heartbeat mit nur-wenn: <system>:<kind> (etwa nur-wenn: gitlab:mr) ruft
// HasWorkKind(…, kind) — so lässt sich je Art getrennt gaten, statt beide
// Heartbeats über einen gemeinsamen Boolean feuern zu lassen. Ohne Unterscope
// (nur-wenn: <system>) greift weiter HasWork. Ein leerer/unbekannter kind darf
// nicht weniger als HasWork melden (fail-open: im Zweifel Arbeit annehmen).
type KindWorkChecker interface {
	WorkChecker
	HasWorkKind(ctx context.Context, cred Credential, kind string) (bool, error)
}

// workdirKey trägt das Sandbox-Arbeitsverzeichnis durch den Context zu
// Execute. Aktionen, die Dateien in der Sandbox materialisieren (z. B.
// gitlab checkout), entpacken dorthin — der Daemon setzt den Wert, weil nur
// er weiß, wo der Workspace der Runtime liegt.
type ctxKey int

const workdirKey ctxKey = iota

// WithWorkdir hängt das Sandbox-Arbeitsverzeichnis an den Context.
func WithWorkdir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workdirKey, dir)
}

// Workdir liest das Sandbox-Arbeitsverzeichnis aus dem Context. Leer, wenn
// die Aktion außerhalb einer Sandbox läuft (z. B. Control-Plane-Kontext).
func Workdir(ctx context.Context) string {
	dir, _ := ctx.Value(workdirKey).(string)
	return dir
}

// WebhookEvent ist das normalisierte Ergebnis eines Webhook-Payloads —
// alles, was der Orchestrator für Idempotenz, Korrelation und Task-Anlage braucht.
type WebhookEvent struct {
	// DedupKey macht die Verarbeitung idempotent (Retries des Zielsystems).
	DedupKey string
	// CorrelationKey weckt eine geblockte Aufgabe (z. B. "zammad:ticket:42").
	CorrelationKey string
	// Title/TaskBody beschreiben die neue Backlog-Aufgabe, falls keine
	// geblockte Aufgabe korreliert.
	Title    string
	TaskBody string
	// ResumeInput ist die Fortsetzungs-Eingabe für eine korrelierte Aufgabe.
	ResumeInput string
	// Wake: false → Event wird registriert (Dedup), weckt aber nicht —
	// z. B. das Echo der eigenen Agent-Antwort.
	Wake bool
	// CorrelateOnly: das Event weckt nur eine bereits geblockte Aufgabe
	// (Wake-on-correlation), legt aber KEINE neue an — z. B. der Merge eines
	// MR: wartet niemand darauf, ist er keine Arbeit.
	CorrelateOnly bool
}

// Descriptor ist die Plugin-Einheit eines Zielsystems: Metadaten fürs UI
// plus die Implementierung.
type Descriptor struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Kind: "builtin" (kompiliert) oder "custom" (Manifest-Upload).
	Kind   string `json:"kind"`
	System System `json:"-"`
	// NoCredentials: das System braucht keine gebrokerten Secrets (kein
	// <name>_token/_url) — es arbeitet rein lokal in der Sandbox (z. B. das
	// dev-Plugin). ACCESS.md, Aktivierung und Guard-Rails gelten trotzdem.
	NoCredentials bool `json:"-"`
	// SetupDoc ist die Einrichtungs-Anleitung fürs UI (Plain Text, nummerierte
	// Schritte). Platzhalter: {public_url} wird von der API durch die
	// konfigurierte COVEY_PUBLIC_URL ersetzt; <agent-slug> bleibt stehen und
	// meint den Slug des zuständigen Agenten.
	SetupDoc string `json:"setup_doc,omitempty"`
}

var (
	registry = map[string]Descriptor{}
	order    []string
)

// Register trägt ein Zielsystem-Plugin ein. Aufruf aus init() des jeweiligen
// Unterpackages; die Registrierungs-Reihenfolge ist die Anzeige-Reihenfolge.
func Register(d Descriptor) {
	if d.Kind == "" {
		d.Kind = "builtin"
	}
	if _, ok := registry[d.Name]; !ok {
		order = append(order, d.Name)
	}
	registry[d.Name] = d
}

// Get liefert das registrierte (kompilierte) Zielsystem zu einem Namen.
func Get(name string) (System, bool) {
	d, ok := registry[name]
	if !ok || d.System == nil {
		return nil, false
	}
	return d.System, true
}

// Describe liefert den Deskriptor eines kompilierten Zielsystems — für
// Entscheidungen der Control Plane, die Metadaten statt der Implementierung
// brauchen (z. B. NoCredentials im Broker).
func Describe(name string) (Descriptor, bool) {
	d, ok := registry[name]
	return d, ok
}

// All liefert alle registrierten Deskriptoren in Registrierungs-Reihenfolge.
func All() []Descriptor {
	out := make([]Descriptor, 0, len(order))
	for _, name := range order {
		out = append(out, registry[name])
	}
	return out
}

// Shutdown-Hooks: Plugins mit lokalem Zustand in der Sandbox (z. B. der
// Prozess-Supervisor des dev-Plugins) registrieren hier ihren Aufräum-Code.
// Der Daemon ruft Shutdown() beim Herunterfahren — sonst überlebten
// gestartete Prozesse (Chrome, Dev-Server) die Sandbox.
var shutdownHooks []func()

// OnShutdown registriert einen Aufräum-Hook (Aufruf aus init() bzw. bei
// erster Nutzung; nicht nebenläufig zur Registrierung).
func OnShutdown(fn func()) { shutdownHooks = append(shutdownHooks, fn) }

// Shutdown führt alle Aufräum-Hooks aus (idempotent, Reihenfolge der
// Registrierung).
func Shutdown() {
	for _, fn := range shutdownHooks {
		fn()
	}
}
