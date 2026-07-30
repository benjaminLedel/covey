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

// SignedWorkChecker verfeinert WorkChecker um eine Signatur des gefundenen
// Arbeitsvorrats — eine kurze, stabile Beschreibung dessen, WORAUF der Check
// angeschlagen hat (bei GitLab etwa „mr!9@n1234": Merge Request 9, neuester
// Beitrag 1234).
//
// Der Grund: Eine nur-wenn:-Bedingung ist pegelgesteuert. Sie meldet Arbeit,
// solange der Zustand besteht — „im Thread hat zuletzt jemand anderes
// geschrieben" bleibt wahr, bis der Agent selbst schreibt. Ein Agent, der einen
// Lauf BEWUSST kommentarlos beendet (die Rückmeldung war eine Freigabe, es gibt
// nichts zu tun), würde deshalb im nächsten Intervall erneut geweckt und
// kommentiert am Ende nur noch, um seinen eigenen Wecker abzustellen.
//
// Die Control Plane merkt sich darum die Signatur, auf die zuletzt gefeuert
// wurde, und feuert erst wieder, wenn sie sich ÄNDERT. Der Agent wird also
// weiterhin zu jeder Neuigkeit geweckt — auch zu einer, die er nur zur Kenntnis
// nimmt — aber nie zweimal zur selben. Ob eine Rückmeldung Arbeit ist (Mängel)
// oder nicht (Freigabe), entscheidet damit der Agent, nicht das Gate.
//
// Eine leere Signatur schaltet die Unterdrückung ab: dann feuert der Heartbeat
// wie bisher bei jedem Pegel (fail-open).
type SignedWorkChecker interface {
	WorkChecker
	// HasWorkSigned prüft wie HasWorkKind (kind == "" → wie HasWork) und
	// liefert zusätzlich die Signatur des gefundenen Vorrats.
	HasWorkSigned(ctx context.Context, cred Credential, kind string) (bool, string, error)
}

// workdirKey trägt das Sandbox-Arbeitsverzeichnis durch den Context zu
// Execute. Aktionen, die Dateien in der Sandbox materialisieren (z. B.
// gitlab checkout), entpacken dorthin — der Daemon setzt den Wert, weil nur
// er weiß, wo der Workspace der Runtime liegt.
type ctxKey int

const (
	workdirKey ctxKey = iota
	artifactSinkKey
	subAgentKey
)

// Artifact ist ein Binär-Nebenergebnis einer Aktion, das ins Recording gehört
// (z. B. ein Screenshot), aber NICHT ins Aktions-Ergebnis der Runtime — sonst
// landete es im LLM-Kontext. Plugins reichen es über EmitArtifact durch; der
// Action-Proxy sammelt es getrennt vom zurückgegebenen Ergebnis ein.
type Artifact struct {
	MIME  string
	Bytes []byte
}

// WithArtifactSink hängt eine Senke an den Context, die EmitArtifact-Aufrufe
// eines Plugins entgegennimmt. Der Action-Proxy setzt sie pro Aktion.
func WithArtifactSink(ctx context.Context, sink func(Artifact)) context.Context {
	return context.WithValue(ctx, artifactSinkKey, sink)
}

// EmitArtifact meldet ein Artefakt an die Senke im Context (No-op ohne Senke).
func EmitArtifact(ctx context.Context, a Artifact) {
	if sink, ok := ctx.Value(artifactSinkKey).(func(Artifact)); ok && sink != nil {
		sink(a)
	}
}

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

// BaselineRef ist der git-Tag, den ein Checkout auf den frisch entpackten
// Upstream-Stand setzt. Er ist der Anker zwischen Checkout und Sub-Lauf: Der
// Sub-Lauf meldet seine Arbeit als Differenz zu diesem Commit, nicht als
// Differenz zweier Status-Abbilder. Nur so bleibt die Arbeit sichtbar, wenn
// der Sub-Agent im Checkout lokal committet — was viele Projekte in ihrer
// CLAUDE.md ausdrücklich verlangen.
const BaselineRef = "covey-baseline"

// SubAgentRequest ist ein Arbeitsauftrag an einen geschachtelten Runtime-Lauf,
// der IM angegebenen Verzeichnis startet — typischerweise ein Projekt-Checkout.
// Damit greift dort der Claude-Code-Harness des Projekts selbst (CLAUDE.md,
// .claude/agents, skills, commands), den der äußere Lauf vom Agenten-Home aus
// nie sieht.
type SubAgentRequest struct {
	// Dir ist das Arbeitsverzeichnis des Sub-Laufs (relativ zum Sandbox-Home
	// oder absolut).
	Dir string
	// Task ist der Arbeitsauftrag — die einzige Eingabe des Sub-Agenten.
	Task string
	// Model und MaxTurns überschreiben die Vorgaben des Laufs (leer/0 = Default).
	Model    string
	MaxTurns int
}

// SubAgentResult ist das normierte Ergebnis eines Sub-Laufs. ChangedFiles und
// Deleted sind repo-relative Pfade und passen direkt in die commit-Aktion.
type SubAgentResult struct {
	Result         string   `json:"result"`
	ChangedFiles   []string `json:"changed_files,omitempty"`
	Deleted        []string `json:"deleted,omitempty"`
	CostUSD        float64  `json:"cost_usd,omitempty"`
	TurnsExhausted bool     `json:"turns_exhausted,omitempty"`
	Error          string   `json:"error,omitempty"`
}

// SubAgentRunner führt einen Sub-Lauf aus. Der Daemon hängt ihn pro Aktion an
// den Context — genau wie Workdir und die Artefakt-Senke, damit Plugins die
// Fähigkeit nutzen können, ohne den Daemon zu importieren.
type SubAgentRunner func(ctx context.Context, req SubAgentRequest) (SubAgentResult, error)

// WithSubAgent hängt den Sub-Agent-Runner an den Context.
func WithSubAgent(ctx context.Context, run SubAgentRunner) context.Context {
	return context.WithValue(ctx, subAgentKey, run)
}

// SubAgent liest den Runner aus dem Context. nil, wenn die Aktion außerhalb
// einer Sandbox läuft (z. B. Control-Plane-Kontext) — dann gibt es keine
// Runtime, die sich schachteln ließe.
func SubAgent(ctx context.Context) SubAgentRunner {
	run, _ := ctx.Value(subAgentKey).(SubAgentRunner)
	return run
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
	Kind string `json:"kind"`
	// Category ordnet das Plugin im Store ein (Konstanten Category…). Das
	// Plugin deklariert sie selbst — die UI leitet ihre Filter aus den
	// vorkommenden Kategorien ab, ohne eigene Liste. Leer = CategoryOther.
	Category string `json:"category,omitempty"`
	System   System `json:"-"`
	// NoCredentials: das System braucht keine gebrokerten Secrets (kein
	// <name>_token/_url) — es arbeitet rein lokal in der Sandbox (z. B. das
	// dev-Plugin). ACCESS.md, Aktivierung und Guard-Rails gelten trotzdem.
	NoCredentials bool `json:"-"`
	// BaseURLOptional: das Plugin kennt den Endpoint seines Zielsystems selbst
	// (fester Default, z. B. der Bot-Framework-Token-Endpoint bei Teams).
	// <name>_url ist dann ein Override für Sonderfälle, kein Pflicht-Secret —
	// der Broker verweigert ohne es nicht. Ein <name>_token bleibt Pflicht.
	BaseURLOptional bool `json:"-"`
	// SetupDoc ist die Einrichtungs-Anleitung fürs UI (Plain Text, nummerierte
	// Schritte). Platzhalter: {public_url} wird von der API durch die
	// konfigurierte COVEY_PUBLIC_URL ersetzt; <agent-slug> bleibt stehen und
	// meint den Slug des zuständigen Agenten (ersatzweise akzeptiert der
	// Webhook-Endpoint auch dessen ID).
	SetupDoc string `json:"setup_doc,omitempty"`
}

// Kategorien für den Zielsystem-Store. Rein für die Einordnung im UI —
// Verhalten hängt nie daran.
const (
	CategoryTicketing = "ticketing"     // Helpdesk, Service-Desk
	CategoryCode      = "code"          // Repos, Merge Requests, CI
	CategoryComms     = "communication" // E-Mail, Chat
	CategoryFiles     = "files"         // Dateiablagen, Dokumente
	CategoryWeb       = "web"           // Browser, Web-Anwendungen
	CategoryDev       = "dev"           // Werkzeuge in der Sandbox selbst
	CategoryOther     = "other"
)

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
	if d.Category == "" {
		d.Category = CategoryOther
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
