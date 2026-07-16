package daemon

// Runtimes werden als selbstbeschreibende Plugins behandelt: eine Runtime =
// eine Datei, die sich in init() via RegisterRuntime einträgt und dabei
// Metadaten (Name/Label/Beschreibung fürs UI), ihre Einrichtungs-Anleitung und
// eine Fabrik mitbringt. Daemon (Ausführung) und Control Plane (UI/Validierung)
// lesen dieselbe Registry — es gibt keine zweite, hartkodierte Liste.

// RuntimeDescriptor ist die Plugin-Einheit einer Runtime.
type RuntimeDescriptor struct {
	Name            string         `json:"name"`
	Label           string         `json:"label"`
	Description     string         `json:"description"`
	NeedsCredential bool           `json:"needs_credential"`
	Setup           []SetupStep    `json:"setup"`
	New             func() Runtime `json:"-"` // Fabrik der Implementierung (daemon-seitig)
}

// SetupStep ist ein Schritt der Einrichtungs-Anleitung. Text darf
// `inline-code` in Backticks enthalten; das UI rendert diese Spannen monospaced.
type SetupStep struct {
	Text  string   `json:"text"`
	Items []string `json:"items,omitempty"`
}

var (
	runtimeRegistry = map[string]RuntimeDescriptor{}
	runtimeOrder    []string
)

// RegisterRuntime trägt ein Runtime-Plugin ein. Aufruf aus init() der
// jeweiligen Runtime-Datei; die Reihenfolge der Registrierung ist die
// Anzeige-Reihenfolge im UI.
func RegisterRuntime(d RuntimeDescriptor) {
	if _, ok := runtimeRegistry[d.Name]; !ok {
		runtimeOrder = append(runtimeOrder, d.Name)
	}
	runtimeRegistry[d.Name] = d
}

// Runtimes liefert alle registrierten Deskriptoren in Registrierungs-Reihenfolge.
func Runtimes() []RuntimeDescriptor {
	out := make([]RuntimeDescriptor, 0, len(runtimeOrder))
	for _, name := range runtimeOrder {
		out = append(out, runtimeRegistry[name])
	}
	return out
}

// IsRuntime prüft, ob ein Runtime-Name registriert ist.
func IsRuntime(name string) bool {
	_, ok := runtimeRegistry[name]
	return ok
}

// newRuntimes instanziiert alle registrierten Plugins für einen Daemon-Client.
func newRuntimes() map[string]Runtime {
	m := make(map[string]Runtime, len(runtimeRegistry))
	for name, d := range runtimeRegistry {
		if d.New != nil {
			m[name] = d.New()
		}
	}
	return m
}
