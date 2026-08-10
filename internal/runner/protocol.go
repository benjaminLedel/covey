// Package runner holds the runner protocol (spec/16) and the runner side of
// it: the node that actually starts sandboxes, and the pool that stands in
// front of it as the orchestrator's SandboxProvider.
//
// The protocol is the only way a sandbox starts — including in the normal
// installation, where the control plane speaks it with a runner inside its own
// process. What differs between "in the same process" and "on another host" is
// a Transport, nothing else. A built-in runner that took a shortcut past the
// protocol would defeat the point: the remote path would then be exercised
// only by whoever operates two hosts, and would rot everywhere else.
//
// Deliberately free of the database. On a remote host the runner is exactly
// the component that must not be a database client (spec/16, "Trust
// boundary"); the rows live next door in internal/runner/store.
package runner

import (
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
)

// Message is the envelope for both directions (JSON, transport-agnostic).
//
// ID correlates an answer with its request. It is not a sequence number: with
// several sandboxes starting at once, the answers arrive in whatever order the
// container runtime produces them, and "the next message" would be the wrong
// one often enough to be hard to find.
type Message struct {
	Type    string          `json:"type"`
	ID      string          `json:"id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Control plane → runner.
const (
	TypeStartSandbox = "start_sandbox"
	TypeStopSandbox  = "stop_sandbox"
	// TypeSyncHome writes the home into the store as a snapshot — after the
	// job, and enforceable besides (maintenance, decommissioning a runner).
	TypeSyncHome = "sync_home"
	// TypeCheck asks what stands between this runner and a running sandbox —
	// no Docker daemon, a missing image. Asked instead of guessed, because the
	// answer is specific to the host and only it can give it.
	TypeCheck = "check"
)

// Runner → control plane.
const (
	TypeRegistered     = "registered"
	TypeSandboxStarted = "sandbox_started"
	TypeSandboxFailed  = "sandbox_failed"
	TypeSandboxStopped = "sandbox_stopped"
	// TypeSandboxExited: the sandbox ended on its own — a crash, an OOM. The
	// control plane learns of it as a reported fact instead of inferring it
	// from a ReadyTimeout minutes later.
	TypeSandboxExited = "sandbox_exited"
	TypeCheckResult   = "check_result"
	TypeHomeSynced    = "home_synced"
	TypeHeartbeat     = "heartbeat"
)

// Registered is the runner's first message: what it is and what it can carry.
type Registered struct {
	RunnerID uuid.UUID `json:"runner_id"`
	OrgID    uuid.UUID `json:"org_id"`
	// Protocol is the protocol version this runner speaks. Runner and server
	// are delivered separately, so different versions inevitably meet
	// (spec/16, "Protocol version").
	Protocol int `json:"protocol"`
	// Version is the runner's own build — so that version drift becomes
	// visible instead of merely being suspected.
	Version string   `json:"version,omitempty"`
	Arch    string   `json:"arch,omitempty"`
	Tags    []string `json:"tags,omitempty"`
}

// Protocol is the version this build speaks. It is raised when a message
// changes its meaning — a new, optional field is not a new version.
const Protocol = 1

// StartSandbox brings up compute for one agent. The image is already resolved:
// what a profile name means is decided by the control plane, which knows the
// agent; the runner only knows images.
type StartSandbox struct {
	AgentID uuid.UUID         `json:"agent_id"`
	OrgID   uuid.UUID         `json:"org_id"`
	Image   string            `json:"image"`
	HomeDir string            `json:"home_dir,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	// EgressToken identifies the sandbox to the egress proxy as this agent.
	// Empty = no egress enforcement for this sandbox.
	EgressToken string `json:"egress_token,omitempty"`
	// Snapshot is the state the home is materialised to before the sandbox
	// starts. Empty = whatever lies in the working copy — which is the case on
	// the very first wake, and after that only when the store is switched off.
	Snapshot string `json:"snapshot,omitempty"`
}

// SyncHome writes an agent's home into the store.
type SyncHome struct {
	AgentID uuid.UUID `json:"agent_id"`
	OrgID   uuid.UUID `json:"org_id"`
	// Excludes are the paths left out. Their role is a cost question, not a
	// prerequisite for correctness: the default is empty, and without
	// configuration everything is synced (spec/16).
	Excludes []string `json:"excludes,omitempty"`
}

// HomeSynced reports a completed sync. Only afterwards may anything be cleaned
// up locally.
type HomeSynced struct {
	AgentID      uuid.UUID `json:"agent_id"`
	ManifestHash string    `json:"manifest_hash"`
	TotalSize    int64     `json:"total_size"`
	Blocks       int       `json:"blocks"`
	BytesUp      int64     `json:"bytes_up"`
	Err          string    `json:"err,omitempty"`
}

// StopSandbox shuts compute down; the home stays.
type StopSandbox struct {
	AgentID uuid.UUID `json:"agent_id"`
}

// SandboxResult answers start/stop. Err empty = it worked.
type SandboxResult struct {
	AgentID uuid.UUID `json:"agent_id"`
	Err     string    `json:"err,omitempty"`
}

// SandboxExited reports a sandbox that ended without being asked to.
type SandboxExited struct {
	AgentID uuid.UUID `json:"agent_id"`
	// Reason is meant for a human: exit code, OOM, container gone.
	Reason string `json:"reason"`
}

// Check asks about the images that are actually in use — the answer follows
// the agents, not the configuration.
type Check struct {
	Images []string `json:"images,omitempty"`
}

// CheckResult lists what stands in the way. Empty = nothing does.
type CheckResult struct {
	Problems []string `json:"problems,omitempty"`
}

// encode packs a payload into a message.
func encode(msgType, id string, payload any) (Message, error) {
	m := Message{Type: msgType, ID: id}
	if payload == nil {
		return m, nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return Message{}, fmt.Errorf("runner protocol: encode %s: %w", msgType, err)
	}
	m.Payload = raw
	return m, nil
}

// decode unpacks a payload. A message whose payload does not fit its type is a
// protocol error, not an empty struct: silently carrying on with zero values
// would start a sandbox for the nil agent.
func decode[T any](m Message) (T, error) {
	var out T
	if len(m.Payload) == 0 {
		return out, fmt.Errorf("runner protocol: %s without payload", m.Type)
	}
	if err := json.Unmarshal(m.Payload, &out); err != nil {
		return out, fmt.Errorf("runner protocol: decode %s: %w", m.Type, err)
	}
	return out, nil
}
