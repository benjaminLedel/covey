package wasmplug

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Stored is how a wasm plugin sits in the database and travels to the daemon:
// the module as base64 inside the same JSON definition every other plugin kind
// uses. That is deliberate — it keeps the whole chain (the row, the broker,
// `inject_target`, the daemon's cache) knowing only "a kind and some JSON".
//
// Describe rides along because the store list is built on every page view and
// compiling a module costs seconds. What a plugin IS should not require running
// it.
type Stored struct {
	Wasm     string      `json:"wasm"`
	Describe Description `json:"describe"`
}

// Pack validates a module and wraps it for storage: it is compiled once, right
// here, and asked what it is. A module that does not compile, or will not say
// what it is, never reaches the database — the alternative is finding out at
// the first action of the first agent, hours later.
func Pack(ctx context.Context, module []byte) ([]byte, Description, error) {
	m, err := Compile(ctx, module)
	if err != nil {
		return nil, Description{}, err
	}
	defer m.Close(ctx)

	desc := m.Describe()
	raw, err := json.Marshal(Stored{
		Wasm:     base64.StdEncoding.EncodeToString(module),
		Describe: desc,
	})
	return raw, desc, err
}

// Unpack reads a stored definition back into a usable module.
func Unpack(ctx context.Context, raw []byte) (*System, error) {
	var s Stored
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("wasm: stored definition unreadable: %w", err)
	}
	module, err := base64.StdEncoding.DecodeString(s.Wasm)
	if err != nil {
		return nil, fmt.Errorf("wasm: stored module unreadable: %w", err)
	}
	m, err := Compile(ctx, module)
	if err != nil {
		return nil, err
	}
	return NewSystem(m), nil
}

// StoredDescription reads only the description — no compiling, which is what
// makes it usable in a list.
func StoredDescription(raw []byte) (Description, bool) {
	var s Stored
	if err := json.Unmarshal(raw, &s); err != nil || s.Describe.Name == "" {
		return Description{}, false
	}
	return s.Describe, true
}

// The values Description.Credentials knows. Anything else reads as the
// default: a word the host does not recognise must not open the broker.
const (
	CredentialsRequired = ""
	CredentialsOptional = "optional"
	CredentialsNone     = "none"
)

// Descriptor translates a module's self-declaration into the descriptor the
// control plane decides on, so that a stored plugin answers the same questions
// a compiled one answers through target.Describe — above all which secrets the
// broker must insist on.
//
// "none" is NoCredentials and "optional" is CredentialsOptional; both make
// <name>_url optional as well, because a plugin that may run without a token
// has no brokered system it could not run without either.
func (d Description) Descriptor() target.Descriptor {
	out := target.Descriptor{
		Name: d.Name, Label: d.Label, Description: d.Description,
		Kind: "wasm", Category: d.Category, Scopes: d.Scopes,
	}
	if out.Category == "" {
		out.Category = target.CategoryOther
	}
	switch d.Credentials {
	case CredentialsNone:
		out.NoCredentials, out.BaseURLOptional = true, true
	case CredentialsOptional:
		out.CredentialsOptional, out.BaseURLOptional = true, true
	}
	return out
}
