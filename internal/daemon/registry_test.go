package daemon

import "testing"

// The registry is the one list. Control plane and daemon read it, and an
// engine that registers itself has to be answerable through every lookup —
// otherwise validation and execution disagree about what exists.
func TestEveryRegisteredEngineAnswersEveryLookup(t *testing.T) {
	all := Runtimes()
	if len(all) == 0 {
		t.Fatal("no runtime registered — the registry would have no engine at all")
	}
	for _, d := range all {
		if !IsRuntime(d.Name) {
			t.Errorf("%s is in the list but IsRuntime says no", d.Name)
		}
		got, ok := Describe(d.Name)
		if !ok || got.Name != d.Name {
			t.Errorf("Describe(%q) = %q, %v", d.Name, got.Name, ok)
		}
		if d.Label == "" || d.Description == "" {
			t.Errorf("%s has no label or no description — the interface would show a blank", d.Name)
		}
		// Exactly one of EnvVar and Path: the delivery form is declared, not
		// guessed. Assuming it was uniform is what broke at the second engine.
		for _, c := range d.Credentials {
			if (c.EnvVar == "") == (c.Path == "") {
				t.Errorf("%s/%s declares env_var=%q and path=%q — exactly one of them belongs there",
					d.Name, c.Kind, c.EnvVar, c.Path)
			}
			if c.Secret == "" {
				t.Errorf("%s/%s names no secret", d.Name, c.Kind)
			}
			if c.Kind != CredAPIKey && c.Kind != CredSubscription {
				t.Errorf("%s carries an unknown credential kind %q", d.Name, c.Kind)
			}
		}
		if d.NeedsCredential() != (len(d.Credentials) > 0) {
			t.Errorf("%s: NeedsCredential disagrees with its own credential list", d.Name)
		}
		// The declared default has to be a member of the declared list.
		if def := d.DefaultModel(); def != "" && !d.AcceptsModel(def) {
			t.Errorf("%s substitutes %q, which it does not itself accept", d.Name, def)
		}
	}
}

func TestIsRuntimeAndDescribeRefuseTheUnknown(t *testing.T) {
	if IsRuntime("gibtesnicht") {
		t.Error("an unregistered engine counts as registered")
	}
	if _, ok := Describe("gibtesnicht"); ok {
		t.Error("Describe answered for an unregistered engine")
	}
}

// Money is spent per token on an API key and not on a seat. That difference
// decides the honest unit of a limit.
func TestCredentialMetered(t *testing.T) {
	if !(RuntimeCredential{Kind: CredAPIKey}).Metered() {
		t.Error("an API key does not count as metered")
	}
	if (RuntimeCredential{Kind: CredSubscription}).Metered() {
		t.Error("a seat counts as metered")
	}
}

func TestCredentialLookupByKind(t *testing.T) {
	d := RuntimeDescriptor{Credentials: []RuntimeCredential{
		{Kind: CredAPIKey, Secret: "k", EnvVar: "K"},
		{Kind: CredSubscription, Secret: "t", Path: ".config/t"},
	}}
	c, ok := d.Credential(CredSubscription)
	if !ok || c.Path != ".config/t" {
		t.Errorf("Credential(subscription) = %+v, %v", c, ok)
	}
	if _, ok := d.Credential("keine-art"); ok {
		t.Error("an unknown kind was answered")
	}
	if _, ok := (RuntimeDescriptor{}).Credential(CredAPIKey); ok {
		t.Error("an engine without credentials answered one")
	}
}

// The first entry is the default — one field instead of two, and a default
// that cannot name a model outside the list.
func TestModelsFirstEntryIsTheDefault(t *testing.T) {
	d := RuntimeDescriptor{Capabilities: RuntimeCapabilities{Models: []string{"a", "b"}}}
	if got := d.DefaultModel(); got != "a" {
		t.Errorf("DefaultModel = %q, expected the first entry", got)
	}
	if !d.AcceptsModel("b") {
		t.Error("a declared model was refused")
	}
	if d.AcceptsModel("c") {
		t.Error("an undeclared model was accepted")
	}
	// The empty id means "the engine's default" and always passes.
	if !d.AcceptsModel("") {
		t.Error("the empty model was refused")
	}
}

// An engine in front of a single provider declares no list: the model range is
// the provider's to publish and ours to pass through.
func TestAnEngineWithoutAListSubstitutesNothingAndAcceptsAnything(t *testing.T) {
	var d RuntimeDescriptor
	if got := d.DefaultModel(); got != "" {
		t.Errorf("DefaultModel = %q, expected nothing", got)
	}
	if !d.AcceptsModel("ein-id-das-niemand-kennt") {
		t.Error("an engine without a list refused a model")
	}
}

func TestEffortLevels(t *testing.T) {
	d := RuntimeDescriptor{Capabilities: RuntimeCapabilities{EffortLevels: []string{"low", "high"}}}
	if !d.AcceptsEffort("") {
		t.Error("the empty level was refused — it means the engine's own default")
	}
	if !d.AcceptsEffort("high") {
		t.Error("a declared level was refused")
	}
	if d.AcceptsEffort("mittel") {
		t.Error("an undeclared level was accepted")
	}
	// An engine with no such control accepts only the empty level, rather than
	// storing one where nothing reads it.
	var none RuntimeDescriptor
	if none.AcceptsEffort("low") {
		t.Error("an engine without effort levels accepted one")
	}
	if !none.AcceptsEffort("") {
		t.Error("an engine without effort levels refused the empty level")
	}
}

// The registry-level lookups are what the HTTP validation calls. An unknown
// engine has to fail CLOSED there — anything else stores a value that the
// first run will choke on.
func TestRegistryLookupsFailClosedOnAnUnknownEngine(t *testing.T) {
	if got := Models("gibtesnicht"); got != nil {
		t.Errorf("Models = %v, expected nil", got)
	}
	if got := DefaultModel("gibtesnicht"); got != "" {
		t.Errorf("DefaultModel = %q, expected nothing", got)
	}
	if got := EffortLevels("gibtesnicht"); got != nil {
		t.Errorf("EffortLevels = %v, expected nil", got)
	}
	if AcceptsModel("gibtesnicht", "irgendeins") {
		t.Error("an unknown engine accepted a model")
	}
	if !AcceptsModel("gibtesnicht", "") {
		t.Error("an unknown engine refused the empty model")
	}
	if AcceptsEffort("gibtesnicht", "low") {
		t.Error("an unknown engine accepted an effort level")
	}
	if !AcceptsEffort("gibtesnicht", "") {
		t.Error("an unknown engine refused the empty effort level")
	}
}

// The registry-level lookups have to answer the same as the descriptor's own
// methods — two paths to the same question, and the HTTP layer takes the
// shorter one.
func TestRegistryLookupsAgreeWithTheDescriptor(t *testing.T) {
	for _, d := range Runtimes() {
		if got := DefaultModel(d.Name); got != d.DefaultModel() {
			t.Errorf("%s: DefaultModel %q vs %q", d.Name, got, d.DefaultModel())
		}
		if len(Models(d.Name)) != len(d.Capabilities.Models) {
			t.Errorf("%s: Models disagree", d.Name)
		}
		if len(EffortLevels(d.Name)) != len(d.Capabilities.EffortLevels) {
			t.Errorf("%s: EffortLevels disagree", d.Name)
		}
		for _, m := range d.Capabilities.Models {
			if !AcceptsModel(d.Name, m) {
				t.Errorf("%s refuses its own model %q through the registry", d.Name, m)
			}
		}
		for _, l := range d.Capabilities.EffortLevels {
			if !AcceptsEffort(d.Name, l) {
				t.Errorf("%s refuses its own effort level %q through the registry", d.Name, l)
			}
		}
	}
}

// Registration order is display order, and a second registration under the
// same name replaces rather than duplicates.
func TestRegisterRuntimeReplacesAndKeepsTheOrder(t *testing.T) {
	before := len(Runtimes())
	name := "test-engine-für-die-registry"
	t.Cleanup(func() {
		delete(runtimeRegistry, name)
		for i, n := range runtimeOrder {
			if n == name {
				runtimeOrder = append(runtimeOrder[:i], runtimeOrder[i+1:]...)
				break
			}
		}
	})

	RegisterRuntime(RuntimeDescriptor{Name: name, Label: "erst"})
	if len(Runtimes()) != before+1 {
		t.Fatalf("registration did not add an engine (%d → %d)", before, len(Runtimes()))
	}
	RegisterRuntime(RuntimeDescriptor{Name: name, Label: "dann"})
	if got := len(Runtimes()); got != before+1 {
		t.Errorf("re-registration added a second entry (%d)", got)
	}
	if d, _ := Describe(name); d.Label != "dann" {
		t.Errorf("re-registration did not replace: label = %q", d.Label)
	}
	if Runtimes()[before].Name != name {
		t.Error("the new engine is not at the end — registration order is display order")
	}
}

// newRuntimes instantiates what a daemon client can actually run: an engine
// without a factory is metadata for the control plane and must not appear as
// an executable runtime.
func TestNewRuntimesSkipsWhatHasNoFactory(t *testing.T) {
	got := newRuntimes()
	want := 0
	for _, d := range Runtimes() {
		if d.New != nil {
			want++
		}
	}
	if len(got) != want {
		t.Fatalf("newRuntimes built %d runtimes, expected %d", len(got), want)
	}
	for name, r := range got {
		if r == nil {
			t.Errorf("%s was built as nil", name)
		}
	}
}
