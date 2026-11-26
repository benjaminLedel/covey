package store

import (
	"encoding/json"
	"strings"
	"testing"

	"covey/internal/target/wasmplug"
)

func storedWasm(t *testing.T, credentials string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(wasmplug.Stored{Describe: wasmplug.Description{Name: "pub", Credentials: credentials}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// target.Describe knows only compiled plugins, so a wasm plugin's declaration
// that it needs no credentials reached neither the broker nor the setup
// assistant. Plugin.Descriptor reads it from the stored description.
func TestPluginDescriptorReadsAWasmPluginsCredentials(t *testing.T) {
	none, ok := Plugin{Name: "pub", Kind: "wasm", Manifest: storedWasm(t, "none")}.Descriptor()
	if !ok || !none.NoCredentials || !none.BaseURLOptional {
		t.Errorf("credentials none: %+v, %v", none, ok)
	}
	opt, ok := Plugin{Name: "pub", Kind: "wasm", Manifest: storedWasm(t, "optional")}.Descriptor()
	if !ok || !opt.CredentialsOptional || !opt.BaseURLOptional || opt.NoCredentials {
		t.Errorf("credentials optional: %+v, %v", opt, ok)
	}
	req, ok := Plugin{Name: "pub", Kind: "wasm", Manifest: storedWasm(t, "")}.Descriptor()
	if !ok || req.NoCredentials || req.CredentialsOptional || req.BaseURLOptional {
		t.Errorf("no declaration has to stay required: %+v, %v", req, ok)
	}
	// A manifest plugin has no descriptor, whatever its file says.
	if _, ok := (Plugin{Name: "pub", Kind: "custom", Manifest: storedWasm(t, "none")}).Descriptor(); ok {
		t.Error("a manifest plugin has no descriptor")
	}
}

func TestWasmSetupDocFollowsTheDeclaration(t *testing.T) {
	if doc := wasmSetupDoc("pub", ""); !strings.Contains(doc, "pub_token = API token") || strings.Contains(doc, "Optionally") {
		t.Errorf("required:\n%s", doc)
	}
	if doc := wasmSetupDoc("pub", "optional"); !strings.Contains(doc, "Optionally store") {
		t.Errorf("optional:\n%s", doc)
	}
	if doc := wasmSetupDoc("pub", "none"); strings.Contains(doc, "pub_token") || !strings.Contains(doc, "No secrets are needed") {
		t.Errorf("none:\n%s", doc)
	}
}
