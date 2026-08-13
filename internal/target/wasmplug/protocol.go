// Package wasmplug runs target-system plugins that are real code — compiled to
// WebAssembly rather than described as a manifest (spec/22).
//
// Why WebAssembly and not a Go plugin (.so): a shared object demands the exact
// same Go toolchain, the exact same version of every shared dependency and the
// same platform, which ends the single static binary; and it runs INSIDE the
// control plane's process, next to the master key. Neither is acceptable for
// code that arrives from a catalogue. A wasm module is one artefact with one
// digest, runs the same everywhere, and can do only what the host hands it.
//
// The security property that makes third-party code tolerable here is worth
// stating plainly: the module gets NO network, NO filesystem, NO clock beyond
// what WASI grants, and — the important one — it NEVER SEES THE CREDENTIAL. It
// asks the host to make a request; the host adds the brokered token and returns
// the response. A plugin therefore cannot leak a token: it has none, and it has
// no socket to leak it through.
//
// The protocol is deliberately stdio and line-based rather than a wasm memory
// ABI. It costs a little performance (an action is network-bound anyway) and
// buys a lot: no alloc/free dance across the boundary, and any language that
// can read stdin and write stdout can implement it — Go, TinyGo, Rust.
package wasmplug

import "encoding/json"

// Invocation is what the host writes to the module's stdin, as one JSON line.
type Invocation struct {
	// Op is what the host wants: "execute", "prompt_doc", "probe", "poll" or
	// "describe". A module answers "describe" without any credentials being
	// involved — that is how its capabilities are discovered.
	Op string `json:"op"`
	// Action and Params apply to op=execute.
	Action string          `json:"action,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	// Kind is the sub-scope of a poll (`nur-wenn: <system>:<kind>`).
	Kind string `json:"kind,omitempty"`
	// Scopes are the agent's access levels, for op=prompt_doc.
	Scopes []string `json:"scopes,omitempty"`
}

// Message is one line the module writes to stdout. Exactly one field is set.
type Message struct {
	// Fetch asks the host to perform an HTTP request against the target
	// system. The host adds the base URL and the credential — which is why the
	// module never needs either.
	Fetch *FetchRequest `json:"fetch,omitempty"`
	// Log is a diagnostic line for the operator; it never reaches the agent.
	Log string `json:"log,omitempty"`
	// Result ends the invocation successfully.
	Result json.RawMessage `json:"result,omitempty"`
	// Error ends the invocation with a failure the agent gets to see.
	Error string `json:"error,omitempty"`
	// Describe answers op=describe.
	Describe *Description `json:"describe,omitempty"`
}

// FetchRequest is a request the module wants made on its behalf. Note what is
// absent: no host, no scheme, no headers for authentication. The path is
// relative to the brokered base URL, exactly as in a manifest — a plugin
// cannot name the server it talks to, so it cannot redirect a token anywhere.
type FetchRequest struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Header map[string]string `json:"header,omitempty"`
	Body   json.RawMessage   `json:"body,omitempty"`
}

// FetchResponse is what the host writes back to stdin after a Fetch.
type FetchResponse struct {
	Status int             `json:"status"`
	Body   json.RawMessage `json:"body,omitempty"`
	// Text carries a non-JSON body verbatim.
	Text string `json:"text,omitempty"`
	// Error is set when the request could not be made at all (DNS, timeout,
	// a refused egress). The module decides what that means for the action.
	Error string `json:"error,omitempty"`
}

// Description is a module's self-declaration, asked once when it is loaded. It
// takes the place of the manifest's static fields: a compiled plugin says what
// it is in code, and the platform still learns it as data.
type Description struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	// Actions the agent may call, with the guard-rail subject each maps to.
	Actions []ActionDesc `json:"actions,omitempty"`
	// Scopes is the vocabulary this plugin understands in ACCESS.md.
	Scopes []string `json:"scopes,omitempty"`
	// Auth says how the brokered token reaches the target system. The module
	// never sees the token — it says where the HOST should put it. Empty =
	// "Authorization: Bearer {token}", the same default a manifest has.
	Auth AuthDesc `json:"auth,omitempty"`
	// Hosts are additional hosts this plugin needs to reach, beyond the one
	// brokered base URL — an OAuth token endpoint, a second API, a public
	// vulnerability database. They are declared, not requested at runtime, so
	// that an operator sees them BEFORE installing rather than in a log
	// afterwards.
	//
	// Two things do not follow from declaring a host. The organization's egress
	// allowlist still decides whether the request leaves at all. And the
	// brokered credential is NEVER sent to a declared host — it belongs to the
	// system the organization pointed the plugin at, and a token that travels
	// to a second host is a token that leaked.
	Hosts []string `json:"hosts,omitempty"`
	// Probe/Poll say whether the module answers those ops at all. A module
	// that does not must not be offered a connection test it can only fail.
	Probe bool `json:"probe,omitempty"`
	Poll  bool `json:"poll,omitempty"`
}

// AuthDesc mirrors the manifest's auth block: which header carries the token
// and in what shape.
type AuthDesc struct {
	Header string `json:"header,omitempty"`
	Format string `json:"format,omitempty"`
}

// ActionDesc describes one action for the guard rails and the prompt.
type ActionDesc struct {
	Name string `json:"name"`
	Doc  string `json:"doc,omitempty"`
	// Subject overrides the guard-rail subject (default <system>:<action>).
	Subject string `json:"subject,omitempty"`
	// Scope is the access level this action belongs to.
	Scope string `json:"scope,omitempty"`
}

// PollResult is what a module returns for op=poll: whether there is work, and
// the signature of what it responded to (see target.SignedWorkChecker).
type PollResult struct {
	HasWork   bool   `json:"has_work"`
	Signature string `json:"signature,omitempty"`
}
