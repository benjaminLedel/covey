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
}

// NeedsCredential is derived rather than declared — one fact, one place.
func (d RuntimeDescriptor) NeedsCredential() bool { return len(d.Credentials) > 0 }

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
