package daemon

// Runtimes are treated as self-describing plugins: one runtime = one file that
// registers itself in init() via RegisterRuntime, bringing metadata
// (name/label/description for the UI), its setup instructions and a factory.
// Daemon (execution) and control plane (UI/validation) read the same registry —
// there is no second, hardcoded list.

// RuntimeDescriptor is the plugin unit of a runtime.
type RuntimeDescriptor struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	// Credentials are the LLM credentials this engine can run on, in order of
	// PRECEDENCE: the first one an organisation has deposited wins. An API key
	// therefore stands before a subscription token, so that whoever holds both
	// uses the one they are billed for deliberately rather than by accident.
	// Empty = the engine needs none (the mock).
	Credentials []RuntimeCredential `json:"credentials"`
	// Capabilities says which parts of the agent model this engine covers. Not
	// every engine covers all of it, and the difference has to be visible when
	// an agent is assigned rather than when the first run fails.
	Capabilities RuntimeCapabilities `json:"capabilities"`
	Setup        []SetupStep         `json:"setup"`
	New          func() Runtime      `json:"-"` // factory of the implementation (daemon side)
}

// Credential kinds. Stable identifiers within an engine — a configured runtime
// stores one of these, so renaming one is a migration.
const (
	CredAPIKey       = "api_key"      // metered: every token is billed
	CredSubscription = "subscription" // quota: a seat, paid for regardless of use
)

// RuntimeCredential describes one credential an engine can run on: which secret
// it is looked up under, how it has to reach the sandbox, and what kind of
// capacity it represents.
//
// The delivery form is not uniform across engines, and assuming it is was the
// first thing that broke at the second one: Claude Code takes its credential as
// an environment variable, Codex takes an API key the same way but its
// ChatGPT-plan login as a FILE (spec/19). Exactly one of EnvVar and Path is set.
type RuntimeCredential struct {
	Kind   string `json:"kind"`  // CredAPIKey | CredSubscription
	Label  string `json:"label"` // for the interface ("API key", "subscription")
	Secret string `json:"secret"`
	// EnvVar delivers the value as an environment variable of the run.
	EnvVar string `json:"env_var,omitempty"`
	// Path delivers it as a file relative to the agent home, written before the
	// run and removed after it — a credential left lying there would be a
	// long-lived secret in the sandbox (spec/04).
	Path string `json:"path,omitempty"`
}

// Metered reports whether using this credential spends money per token (an API
// key) as opposed to drawing on a quota that is paid for either way (a seat).
// It decides the honest unit of a limit: money where money is spent, the window
// quota where it is not.
func (c RuntimeCredential) Metered() bool { return c.Kind == CredAPIKey }

// RuntimeCapabilities are the declared properties of an engine.
type RuntimeCapabilities struct {
	// Resume: can the engine continue a session it started earlier? The whole
	// `blocked` mechanism rests on it (spec/03) — an engine without it can
	// carry agents that finish in one run and cannot carry one that waits for
	// an answer.
	Resume bool `json:"resume"`
	// SkillsDir is where the daemon materialises an agent's skills, relative to
	// the home. It follows the engine's own convention (Claude Code looks in
	// `.claude/skills`); empty means the engine knows no skills, and then none
	// are written rather than being written where nothing reads them.
	SkillsDir string `json:"skills_dir,omitempty"`
	// Models are the model ids this engine is known to carry. Empty means the
	// engine takes whatever it is given — which is right for an engine sitting
	// in front of ONE provider, whose model list is the provider's to publish
	// and ours to pass through.
	//
	// It is not right in front of a gateway. There the list is the instance's,
	// it contains ids no harness has ever heard of, and being listed is not the
	// same as running: educa serves a model that answers 500 on every request
	// (spec/23). So an engine that knows which ids it actually carries names
	// them, and everything else is refused where it is entered rather than at
	// the first run.
	//
	// A non-empty list also means the engine has NO DEFAULT: the empty model is
	// refused with it. That is one rule instead of two flags — whoever names
	// their models is saying that picking one is part of the setup.
	Models []string `json:"models,omitempty"`
	// EffortLevels are the reasoning-effort levels this engine accepts, in
	// ascending order. The names are the engine's own — Claude Code spells them
	// low/medium/high/xhigh/max, another engine spells them differently, and a
	// level one engine knows is a run-time error at the next. Empty means the
	// engine has no such control: then the field is not offered and not
	// accepted, rather than being stored where nothing reads it.
	EffortLevels []string `json:"effort_levels,omitempty"`
}

// NeedsCredential is derived rather than declared — one fact, one place.
func (d RuntimeDescriptor) NeedsCredential() bool { return len(d.Credentials) > 0 }

// AcceptsEffort reports whether a reasoning-effort level is valid for this
// engine. The empty level always is: it means "the engine's own default".
func (d RuntimeDescriptor) AcceptsEffort(level string) bool {
	if level == "" {
		return true
	}
	for _, l := range d.Capabilities.EffortLevels {
		if l == level {
			return true
		}
	}
	return false
}

// AcceptsModel reports whether a model id is valid for this engine. An engine
// without a declared list accepts anything, the empty id included — that is the
// unchanged behaviour of the engines in front of a single provider.
func (d RuntimeDescriptor) AcceptsModel(model string) bool {
	if len(d.Capabilities.Models) == 0 {
		return true
	}
	for _, m := range d.Capabilities.Models {
		if m == model {
			return true
		}
	}
	return false
}

// Models returns the model ids an engine declares — by name, so the control
// plane can validate without knowing the engine. Empty means "not declared",
// which is not the same as "none".
func Models(runtime string) []string {
	d, ok := runtimeRegistry[runtime]
	if !ok {
		return nil
	}
	return d.Capabilities.Models
}

// AcceptsModel is the registry-level lookup behind the HTTP validation. An
// unknown engine accepts only the empty id (fail-closed), as with effort.
func AcceptsModel(runtime, model string) bool {
	d, ok := runtimeRegistry[runtime]
	if !ok {
		return model == ""
	}
	return d.AcceptsModel(model)
}

// EffortLevels returns the levels an engine accepts — by name, so the control
// plane can validate without knowing the engine. Unknown runtime = no levels,
// which makes the caller reject any non-empty effort (fail-closed).
func EffortLevels(runtime string) []string {
	d, ok := runtimeRegistry[runtime]
	if !ok {
		return nil
	}
	return d.Capabilities.EffortLevels
}

// AcceptsEffort is the registry-level lookup behind the HTTP validation.
func AcceptsEffort(runtime, level string) bool {
	d, ok := runtimeRegistry[runtime]
	if !ok {
		return level == ""
	}
	return d.AcceptsEffort(level)
}

// Credential returns the declared credential of a kind.
func (d RuntimeDescriptor) Credential(kind string) (RuntimeCredential, bool) {
	for _, c := range d.Credentials {
		if c.Kind == kind {
			return c, true
		}
	}
	return RuntimeCredential{}, false
}

// SetupStep is one step of the setup instructions. Text may contain
// `inline-code` in backticks; the UI renders those spans monospaced.
type SetupStep struct {
	Text  string   `json:"text"`
	Items []string `json:"items,omitempty"`
}

var (
	runtimeRegistry = map[string]RuntimeDescriptor{}
	runtimeOrder    []string
)

// RegisterRuntime registers a runtime plugin. Called from init() of the
// respective runtime file; the order of registration is the display order in
// the UI.
func RegisterRuntime(d RuntimeDescriptor) {
	if _, ok := runtimeRegistry[d.Name]; !ok {
		runtimeOrder = append(runtimeOrder, d.Name)
	}
	runtimeRegistry[d.Name] = d
}

// Runtimes returns all registered descriptors in registration order.
func Runtimes() []RuntimeDescriptor {
	out := make([]RuntimeDescriptor, 0, len(runtimeOrder))
	for _, name := range runtimeOrder {
		out = append(out, runtimeRegistry[name])
	}
	return out
}

// IsRuntime reports whether a runtime name is registered.
func IsRuntime(name string) bool {
	_, ok := runtimeRegistry[name]
	return ok
}

// Describe returns an engine's descriptor.
func Describe(name string) (RuntimeDescriptor, bool) {
	d, ok := runtimeRegistry[name]
	return d, ok
}

// newRuntimes instantiates all registered plugins for one daemon client.
func newRuntimes() map[string]Runtime {
	m := make(map[string]Runtime, len(runtimeRegistry))
	for name, d := range runtimeRegistry {
		if d.New != nil {
			m[name] = d.New()
		}
	}
	return m
}
