package daemon

// Runtimes are treated as self-describing plugins: one runtime = one file that
// registers itself in init() via RegisterRuntime, bringing metadata
// (name/label/description for the UI), its setup instructions and a factory.
// Daemon (execution) and control plane (UI/validation) read the same registry —
// there is no second, hardcoded list.

// RuntimeDescriptor is the plugin unit of a runtime.
type RuntimeDescriptor struct {
	Name            string         `json:"name"`
	Label           string         `json:"label"`
	Description     string         `json:"description"`
	NeedsCredential bool           `json:"needs_credential"`
	Setup           []SetupStep    `json:"setup"`
	New             func() Runtime `json:"-"` // factory of the implementation (daemon side)
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
