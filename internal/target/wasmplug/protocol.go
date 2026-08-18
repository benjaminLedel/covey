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
	// Op is what the host wants: "execute", "prompt_doc", "probe", "poll",
	// "webhook" or "describe". A module answers "describe" without any
	// credentials being involved — that is how its capabilities are discovered.
	Op string `json:"op"`
	// Action and Params apply to op=execute.
	Action string          `json:"action,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	// Kind is the sub-scope of a poll (`nur-wenn: <system>:<kind>`).
	Kind string `json:"kind,omitempty"`
	// Scopes are the agent's access levels, for op=prompt_doc.
	Scopes []string `json:"scopes,omitempty"`
	// Body is the webhook payload for op=webhook. It arrives ALREADY VERIFIED:
	// the host has checked the signature, because doing so needs the shared
	// secret and a module never sees one (see internal/target/webhooksig).
	Body json.RawMessage `json:"body,omitempty"`
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
	// ReadFile asks the host for a file out of the agent's workspace. The
	// module names a relative path and gets text back — it has no filesystem
	// of its own and cannot leave the workspace, because the host resolves the
	// path inside it (os.Root) rather than trusting what it was handed.
	ReadFile *ReadFileRequest `json:"read_file,omitempty"`
	// Describe answers op=describe.
	Describe *Description `json:"describe,omitempty"`
	// Event answers op=webhook: what the payload means for the backlog.
	Event *WebhookEvent `json:"event,omitempty"`
}

// WebhookEvent is what a module makes of an inbound payload — the same decision
// a compiled plugin's ParseWebhook returns, and the reason webhooks belong in a
// module at all: which ticket this is about, whether it is news or the agent's
// own echo, and the sentences a person will read in the backlog. None of that
// is a field lookup, which is why the manifest engine can only approximate it.
type WebhookEvent struct {
	// DedupKey makes a retry by the target system idempotent.
	DedupKey string `json:"dedup_key,omitempty"`
	// CorrelationKey wakes a blocked task ("zammad:ticket:42").
	CorrelationKey string `json:"correlation_key,omitempty"`
	// Title/TaskBody describe the new task, should nothing correlate.
	Title    string `json:"title,omitempty"`
	TaskBody string `json:"task_body,omitempty"`
	// ResumeInput is what a correlated task resumes with.
	ResumeInput string `json:"resume_input,omitempty"`
	// Wake false: the event is recorded for dedup but wakes nobody — the echo
	// of the agent's own reply is the case this exists for.
	Wake bool `json:"wake,omitempty"`
	// CorrelateOnly: wake a blocked task if there is one, but never create a
	// new one — an event nobody waits for is not work.
	CorrelateOnly bool `json:"correlate_only,omitempty"`
}

// ReadFileRequest names one file, relative to the agent's workspace. What is
// absent again says the most: no absolute path, no way up and out, no
// directory listing. A module that wants to know which lock file a project has
// tries the three names it knows — that is a question with three answers, not a
// reason to hand out the tree.
type ReadFileRequest struct {
	Path string `json:"path"`
}

// ReadFileResponse is what the host writes back to stdin after a ReadFile.
//
// Text, not bytes: this exists so a plugin can read what a project DECLARES —
// lock files, manifests, configuration — and all of that is text. A binary
// would have to be base64'd through a JSON line at twice its size, and the one
// plugin that wanted it would be doing something better done with a fetch.
type ReadFileResponse struct {
	Text string `json:"text,omitempty"`
	// Error is set when the file is missing, too large, outside the workspace,
	// or not text. "Missing" is a normal answer, not a failure: it is how a
	// module finds out which of three lock files a project actually has.
	Error string `json:"error,omitempty"`
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
	// Workdir declares that the module reads files from the agent's workspace.
	// Declared, not asked for at runtime — the same rule as Hosts, and for the
	// same reason: an operator decides before installing, not from a log
	// afterwards. Without it a read_file is refused, and outside a sandbox
	// (control plane, probe, poll) there is no workspace to read from at all.
	Workdir bool `json:"workdir,omitempty"`
	// Webhook declares that the module answers op=webhook, and how the host is
	// to check the signature before it does. Absent = no webhook entrance: the
	// router answers 404 and the setup shows no webhook step, rather than
	// offering a door that leads nowhere.
	Webhook *WebhookDesc `json:"webhook,omitempty"`
}

// WebhookDesc is the module's half of webhook handling: the algorithm and the
// header, nothing else. The check itself is the host's, because it needs the
// shared secret — a module that were handed the secret in order to verify with
// it could also carry it away.
type WebhookDesc struct {
	// Signature: "hmac-sha1" | "hmac-sha256" | "" (the system signs nothing).
	Signature string `json:"signature,omitempty"`
	// SignatureHeader carries it (default "X-Hub-Signature").
	SignatureHeader string `json:"signature_header,omitempty"`
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
