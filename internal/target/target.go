// Package target defines the plugin interface for target systems (Zammad,
// GitLab, …) — the same pattern as the runtime registry in internal/daemon:
// one target system = one subpackage that enters itself into the registry via
// Register in init(). Control Plane (webhook intake, prompt docs, UI) and
// daemon (action execution) read the same registry; there is no hardcoded list.
//
// Lean delivery: a compiled plugin is only linked in if a binary blank-imports
// it (cmd/covey, cmd/coveyd). Whoever wants to ship Covey without Zammad simply
// leaves out the import. Alongside that there are two runtime plugin types that
// need no recompilation at all:
//
//   - Manifest plugins (kind=custom, JSON upload, see manifest.go): a generic
//     REST engine interprets the manifest.
//   - MCP plugins (kind=mcp, see subpackage mcp): a connected MCP server whose
//     tools are discovered (tools/list) and invoked through the action proxy
//     (tools/call).
//
// All three satisfy the same System interface — broker, guard-rails and
// recording apply identically, no matter where the plugin comes from.
package target

import (
	"context"
	"encoding/json"
	"net/http"
)

// System is a connected target system. It bundles the integration surfaces
// (spec/13): actions and prompt docs. The webhook intake is optional (see
// Webhooker) — a target system that only takes in work via polling/heartbeat
// (e.g. GitLab) does not implement it.
type System interface {
	Name() string

	// ActionSubject maps action+params onto the guard-rail subject
	// (e.g. reply with internal=false → "zammad:reply_external").
	ActionSubject(action string, params json.RawMessage) string

	// Execute runs an agent action with brokered credentials (daemon side).
	// The credentials come from the broker per call — they are never
	// persisted.
	Execute(ctx context.Context, action string, params json.RawMessage, cred Credential) (any, error)

	// PromptDoc describes the available actions for the agent's system prompt
	// (it is compiled into the platform protocol section).
	PromptDoc() string
}

// ScopedDocSystem is an optional plugin interface for target systems whose
// prompt doc can be narrowed to the scopes granted in ACCESS.md. It pays off
// where a system carries procedures for several roles: GitLab describes both
// the developer and the QA/reviewer loop, and an agent without the merge scope
// dragged the reviewer part through every turn without ever being able to act
// on it — the doc stands in the context of EVERY turn, so what is unusable
// there is not paid for once but on every one.
//
// Systems without this interface keep delivering their full PromptDoc; the
// store falls back to it (fail-open). A plugin that implements it has to answer
// the full doc for an empty scope list for the same reason — a missing entry
// must never silently take a capability away from an agent.
type ScopedDocSystem interface {
	PromptDocForScopes(scopes []string) string
}

// Webhooker is an optional plugin interface for target systems with an
// incoming webhook (Zammad, manifest plugins). The event router
// (httpapi.handleTargetWebhook) checks for it; a system without Webhooker
// answers webhook posts fail-closed with 404. Target systems that only take in
// work via polling leave this interface out — then there is no incoming
// traffic, no public URL and no webhook secret.
type Webhooker interface {
	// VerifyWebhook checks the integrity of a raw webhook payload (e.g. an
	// HMAC signature). Empty secret = verification disabled (dev).
	VerifyWebhook(secret string, body []byte, header http.Header) bool

	// ParseWebhook turns the payload into the wake event for the orchestrator.
	ParseWebhook(body []byte) (WebhookEvent, error)
}

// Credential is the brokered access pair for a target system.
type Credential struct {
	BaseURL string
	Token   string
}

// WorkChecker is an optional plugin interface for systems without a webhook:
// a cheap upfront check by the Control Plane whether there is any work at all
// (e.g. unread mail via IMAP). Heartbeat entries with nur-wenn: <system> only
// fire if HasWork returns true — otherwise the run, and with it the (expensive)
// agent wake, is skipped. The check runs in the Control Plane; the credential
// never leaves it.
type WorkChecker interface {
	HasWork(ctx context.Context, cred Credential) (bool, error)
}

// KindWorkChecker refines WorkChecker for target systems with several kinds of
// work (e.g. GitLab: issues vs. merge request reviews). A heartbeat with
// nur-wenn: <system>:<kind> (say nur-wenn: gitlab:mr) calls
// HasWorkKind(…, kind) — that way each kind can be gated separately instead of
// letting both heartbeats fire off a shared boolean. Without a sub-scope
// (nur-wenn: <system>) HasWork still applies. An empty/unknown kind must not
// report less than HasWork (fail-open: when in doubt, assume there is work).
type KindWorkChecker interface {
	WorkChecker
	HasWorkKind(ctx context.Context, cred Credential, kind string) (bool, error)
}

// SignedWorkChecker refines WorkChecker with a signature of the work found — a
// short, stable description of WHAT the check responded to (with GitLab for
// instance "mr!9@n1234": merge request 9, newest note 1234).
//
// The reason: a nur-wenn: condition is level-triggered. It reports work as long
// as the state persists — "someone else wrote last in the thread" stays true
// until the agent writes itself. An agent that DELIBERATELY ends a run without
// a comment (the feedback was an approval, there is nothing to do) would
// therefore be woken again in the next interval and would end up commenting
// only to turn off its own alarm clock.
//
// The Control Plane therefore remembers the signature it last fired on and only
// fires again once it CHANGES. So the agent is still woken for every piece of
// news — including one it merely takes note of — but never twice for the same
// one. Whether feedback is work (defects) or not (approval) is thus decided by
// the agent, not by the gate.
//
// An empty signature switches the suppression off: then the heartbeat fires on
// every level as before (fail-open).
type SignedWorkChecker interface {
	WorkChecker
	// HasWorkSigned checks like HasWorkKind (kind == "" → like HasWork) and
	// additionally returns the signature of the work found.
	HasWorkSigned(ctx context.Context, cred Credential, kind string) (bool, string, error)
}

// SignatureWriter says whether one of the system's OWN actions can move the
// work signature — the question the control plane has to answer after a run
// before it may write the state it now sees into the watermark.
//
// The background is a race the signature alone cannot resolve. The signature is
// remembered at dispatch, i.e. BEFORE the run; a run that comments changes it
// itself, so the control plane has to advance it afterwards, otherwise the
// agent's own comment wakes it again. But "the state after the run" also
// contains everything that arrived from outside DURING the run — under a shared
// target-system identity (no bot account per role) authorship cannot tell the
// two apart, and a foreign comment silently absorbed into the watermark is a
// piece of work nobody is ever woken for again.
//
// This interface narrows the race to the cases where the agent really wrote:
// did the run execute no signature-changing action at all, then every change
// since the dispatch comes from outside and the watermark must stay where it
// is. The subject is the one from ActionSubject (e.g. "gitlab:comment_external"),
// because that is what the recording carries.
//
// Systems without this interface keep the old behaviour: after a successful run
// the watermark is advanced unconditionally.
type SignatureWriter interface {
	SignedWorkChecker
	WritesWorkSignature(subject string) bool
}

// workdirKey carries the sandbox working directory through the context to
// Execute. Actions that materialize files in the sandbox (e.g. gitlab
// checkout) unpack them there — the daemon sets the value, because only it
// knows where the runtime's workspace lives.
type ctxKey int

const (
	workdirKey ctxKey = iota
	artifactSinkKey
	subAgentKey
)

// Artifact is a binary side result of an action that belongs in the recording
// (e.g. a screenshot) but NOT in the action result handed to the runtime —
// otherwise it would end up in the LLM context. Plugins pass it through via
// EmitArtifact; the action proxy collects it separately from the returned
// result.
type Artifact struct {
	MIME  string
	Bytes []byte
}

// WithArtifactSink attaches a sink to the context that takes in a plugin's
// EmitArtifact calls. The action proxy sets it per action.
func WithArtifactSink(ctx context.Context, sink func(Artifact)) context.Context {
	return context.WithValue(ctx, artifactSinkKey, sink)
}

// EmitArtifact reports an artifact to the sink in the context (no-op without a
// sink).
func EmitArtifact(ctx context.Context, a Artifact) {
	if sink, ok := ctx.Value(artifactSinkKey).(func(Artifact)); ok && sink != nil {
		sink(a)
	}
}

// WithWorkdir attaches the sandbox working directory to the context.
func WithWorkdir(ctx context.Context, dir string) context.Context {
	return context.WithValue(ctx, workdirKey, dir)
}

// Workdir reads the sandbox working directory from the context. Empty if the
// action runs outside a sandbox (e.g. in Control Plane context).
func Workdir(ctx context.Context) string {
	dir, _ := ctx.Value(workdirKey).(string)
	return dir
}

// BaselineRef is the git tag a checkout puts on the freshly unpacked upstream
// state. It is the anchor between checkout and sub-run: the sub-run reports its
// work as a difference to that commit, not as a difference between two status
// snapshots. Only that way does the work stay visible when the sub-agent
// commits locally in the checkout — which many projects explicitly demand in
// their CLAUDE.md.
const BaselineRef = "covey-baseline"

// SubAgentRequest is a work order for a nested runtime run that starts IN the
// given directory — typically a project checkout. That way the project's own
// Claude Code harness applies there (CLAUDE.md, .claude/agents, skills,
// commands), which the outer run never sees from the agent home.
type SubAgentRequest struct {
	// Dir is the working directory of the sub-run (relative to the sandbox
	// home or absolute).
	Dir string
	// Task is the work order — the sub-agent's only input.
	Task string
	// Model and MaxTurns override the run's defaults (empty/0 = default).
	Model    string
	MaxTurns int
}

// SubAgentResult is the normalized result of a sub-run. ChangedFiles and
// Deleted are repo-relative paths and fit straight into the commit action.
type SubAgentResult struct {
	Result         string   `json:"result"`
	ChangedFiles   []string `json:"changed_files,omitempty"`
	Deleted        []string `json:"deleted,omitempty"`
	CostUSD        float64  `json:"cost_usd,omitempty"`
	TurnsExhausted bool     `json:"turns_exhausted,omitempty"`
	Error          string   `json:"error,omitempty"`
}

// SubAgentRunner executes a sub-run. The daemon attaches it to the context per
// action — just like the workdir and the artifact sink, so that plugins can use
// the capability without importing the daemon.
type SubAgentRunner func(ctx context.Context, req SubAgentRequest) (SubAgentResult, error)

// WithSubAgent attaches the sub-agent runner to the context.
func WithSubAgent(ctx context.Context, run SubAgentRunner) context.Context {
	return context.WithValue(ctx, subAgentKey, run)
}

// SubAgent reads the runner from the context. nil if the action runs outside a
// sandbox (e.g. in Control Plane context) — then there is no runtime that could
// be nested.
func SubAgent(ctx context.Context) SubAgentRunner {
	run, _ := ctx.Value(subAgentKey).(SubAgentRunner)
	return run
}

// WebhookEvent is the normalized result of a webhook payload — everything the
// orchestrator needs for idempotency, correlation and task creation.
type WebhookEvent struct {
	// DedupKey makes processing idempotent (retries by the target system).
	DedupKey string
	// CorrelationKey wakes a blocked task (e.g. "zammad:ticket:42").
	CorrelationKey string
	// Title/TaskBody describe the new backlog task, in case no blocked task
	// correlates.
	Title    string
	TaskBody string
	// ResumeInput is the resume input for a correlated task.
	ResumeInput string
	// Wake: false → the event is registered (dedup) but does not wake —
	// e.g. the echo of the agent's own reply.
	Wake bool
	// CorrelateOnly: the event only wakes an already blocked task
	// (wake-on-correlation) but creates NO new one — e.g. the merge of an MR:
	// if nobody is waiting for it, it is not work.
	CorrelateOnly bool
}

// Descriptor is the plugin unit of a target system: metadata for the UI plus
// the implementation.
type Descriptor struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Kind: "builtin" (compiled) or "custom" (manifest upload).
	Kind string `json:"kind"`
	// Category places the plugin in the store (constants Category…). The
	// plugin declares it itself — the UI derives its filters from the
	// categories that occur, without a list of its own. Empty = CategoryOther.
	Category string `json:"category,omitempty"`
	System   System `json:"-"`
	// NoCredentials: the system needs no brokered secrets (no
	// <name>_token/_url) — it works purely locally in the sandbox (e.g. the
	// dev plugin). ACCESS.md, activation and guard-rails still apply.
	NoCredentials bool `json:"-"`
	// CredentialsOptional: the system is usable WITHOUT a secret, and a stored
	// <name>_token only raises what it may do — e.g. an API key that lifts a
	// public rate limit (vulndb/NVD). The broker then resolves token and URL
	// best effort and grants even when nothing is stored, instead of denying.
	// The difference from NoCredentials: there the plugin never sees a
	// credential; here it sees one as soon as the organization stores one.
	CredentialsOptional bool `json:"-"`
	// BaseURLOptional: the plugin knows its target system's endpoint itself
	// (a fixed default, e.g. the Bot Framework token endpoint for Teams).
	// <name>_url is then an override for special cases, not a mandatory
	// secret — the broker does not refuse without it. A <name>_token stays
	// mandatory.
	BaseURLOptional bool `json:"-"`
	// SetupDoc is the setup guide for the UI (plain text, numbered steps).
	// Placeholders: {public_url} is replaced by the API with the configured
	// COVEY_PUBLIC_URL; <agent-slug> stays as it is and means the slug of the
	// responsible agent (the webhook endpoint accepts its ID instead).
	//
	// It describes what has to happen in the FOREIGN system — create a token,
	// hang in a trigger. What happens in Covey itself is not prose but the
	// fields below: the setup assistant acts on those, and a step it can carry
	// out itself has no business being an instruction.
	SetupDoc string `json:"setup_doc,omitempty"`
	// Scopes are the access levels this plugin understands in ACCESS.md
	// (`- system: zammad scope: read,write,comment`). The plugin declares them
	// because only it knows them; the assistant offers exactly these instead of
	// letting somebody guess a word that is then silently ignored.
	//
	// Empty = the plugin does not distinguish (manifest and MCP plugins), and
	// the assistant leaves the scope out.
	Scopes []string `json:"scopes,omitempty"`
}

// Prober is an optional plugin interface: one cheap, read-only call that shows
// whether the stored credentials actually work — and as whom.
//
// It exists because "saved" and "works" are two different things, and until
// now the difference showed up at the first run of an agent, inside a
// recording, hours later. The returned string is the identity the target
// system reports back (a user name, a login, an address); it is displayed as
// is, so it should be short and recognisable.
//
// Read-only, by contract. A probe that changes anything in the foreign system
// would be a poor kind of test — nobody expects a connection test to leave
// traces.
type Prober interface {
	Probe(ctx context.Context, cred Credential) (string, error)
}

// Categories for the target system store. Purely for placement in the UI —
// behavior never depends on them.
const (
	CategoryTicketing = "ticketing"     // helpdesk, service desk
	CategoryCode      = "code"          // repos, merge requests, CI
	CategoryComms     = "communication" // email, chat
	CategoryFiles     = "files"         // file shares, documents
	CategoryWeb       = "web"           // browser, web applications
	CategoryDev       = "dev"           // tools in the sandbox itself
	CategoryOther     = "other"
)

var (
	registry = map[string]Descriptor{}
	order    []string
)

// Register enters a target system plugin. Called from the respective
// subpackage's init(); the registration order is the display order.
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

// Get returns the registered (compiled) target system for a name.
func Get(name string) (System, bool) {
	d, ok := registry[name]
	if !ok || d.System == nil {
		return nil, false
	}
	return d.System, true
}

// Describe returns the descriptor of a compiled target system — for Control
// Plane decisions that need the metadata rather than the implementation
// (e.g. NoCredentials in the broker).
func Describe(name string) (Descriptor, bool) {
	d, ok := registry[name]
	return d, ok
}

// All returns all registered descriptors in registration order.
func All() []Descriptor {
	out := make([]Descriptor, 0, len(order))
	for _, name := range order {
		out = append(out, registry[name])
	}
	return out
}

// Shutdown hooks: plugins with local state in the sandbox (e.g. the process
// supervisor of the dev plugin) register their cleanup code here. The daemon
// calls Shutdown() when shutting down — otherwise started processes (Chrome,
// dev servers) would outlive the sandbox.
var shutdownHooks []func()

// OnShutdown registers a cleanup hook (called from init() or on first use; not
// concurrently with the registration).
func OnShutdown(fn func()) { shutdownHooks = append(shutdownHooks, fn) }

// Shutdown runs all cleanup hooks (idempotent, in registration order).
func Shutdown() {
	for _, fn := range shutdownHooks {
		fn()
	}
}
