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
	"time"

	"github.com/google/uuid"

	"covey/internal/sandboxfs"
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
	// TypeHomeOp is file access to a home: list, read, write, delete. It does
	// not go through the daemon protocol on purpose — the home has to be
	// readable while the sandbox is asleep, and asleep is the normal state. The
	// runner link exists continuously, the daemon link only during a run.
	TypeHomeOp = "home_op"
	// TypeCheck asks what stands between this runner and a running sandbox —
	// no Docker daemon, a missing image. Asked instead of guessed, because the
	// answer is specific to the host and only it can give it.
	TypeCheck = "check"
	// TypeCapacity asks what this runner is carrying: running sandboxes, free
	// disk. The basis for scheduling and for the warning before the disk runs
	// short — not after.
	TypeCapacity = "capacity"
	// TypePullImage tells the runner to fetch a workplace image NOW instead of
	// at the first wake. `docker run` would pull it by itself — but then the
	// first agent of the day waits several gigabytes long for it, and from the
	// outside that looks like a hanging wake, not like a download. Asked for
	// deliberately, it happens while somebody is looking.
	TypePullImage = "pull_image"
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
	TypeSandboxExited  = "sandbox_exited"
	TypeCheckResult    = "check_result"
	TypeHomeSynced     = "home_synced"
	TypeHomeResult     = "home_result"
	TypeCapacityReport = "capacity_report"
	TypePullResult     = "pull_result"
	// TypeHeartbeat is the sign of life. It is not decoration: a TCP connection
	// can be dead without either side noticing — a NAT that dropped the entry,
	// a network partition, a laptop that closed. The pool would then keep
	// assigning sandboxes to a runner that no longer hears anything, and every
	// wake would sit out its timeout before failing, instead of going to a
	// runner that works.
	TypeHeartbeat = "heartbeat"
)

// HeartbeatInterval is how often a runner reports in, and Silence how long the
// control plane waits before it treats a connection as gone. Three missed beats
// — short enough that a dead link is noticed within a wake, long enough that a
// slow moment does not tear down a working runner.
const (
	HeartbeatInterval = 30 * time.Second
	Silence           = 3 * HeartbeatInterval
)

// CapacityReport is what a runner is carrying.
type CapacityReport struct {
	Sandboxes  int    `json:"sandboxes"`
	TotalBytes int64  `json:"total_bytes"`
	FreeBytes  int64  `json:"free_bytes"`
	WorkDir    string `json:"work_dir,omitempty"`
}

// The file operations of home_op. One message with an operation name rather
// than one message kind per operation: they share their answer shape, and a
// dozen near-identical kinds would only spread the same switch over the
// protocol.
const (
	OpList   = "list"
	OpRead   = "read"
	OpOpen   = "open" // stream a file in chunks
	OpPlan   = "plan" // measure an archive before the first byte is out
	OpZip    = "zip"  // stream the archive in chunks
	OpWrite  = "write"
	OpMkdir  = "mkdir"
	OpRemove = "remove"
	OpMove   = "move"
	OpUsage  = "usage"
	// OpRestore materialises a snapshot over the working copy — the rollback
	// that falls out of the construction anyway (spec/16).
	OpRestore = "restore"
)

// HomeOp is one file operation on an agent's home.
type HomeOp struct {
	AgentID uuid.UUID `json:"agent_id"`
	Op      string    `json:"op"`
	Path    string    `json:"path,omitempty"`
	// To is the destination of a move; Paths the selection of an archive.
	To    string   `json:"to,omitempty"`
	Paths []string `json:"paths,omitempty"`
	// Data is the content of a write, in one message. Bounded by
	// sandboxfs.MaxWriteBytes, which the control plane checks before sending.
	Data []byte `json:"data,omitempty"`
	// Snapshot and OrgID belong to a restore: which state to bring the working
	// copy to, and whose blocks to read.
	Snapshot string    `json:"snapshot,omitempty"`
	OrgID    uuid.UUID `json:"org_id,omitempty"`
}

// HomeResult answers a home_op. Streaming answers (open, zip) arrive as
// several results with the same correlation ID; the last one carries EOF.
type HomeResult struct {
	Err string `json:"err,omitempty"`
	// ErrKind names the error so the other side can map it back to the one the
	// HTTP layer already knows — "not found" has to stay a 404 across the link,
	// not become a 500.
	ErrKind string `json:"err_kind,omitempty"`

	Listing *sandboxfs.Listing  `json:"listing,omitempty"`
	File    *sandboxfs.File     `json:"file,omitempty"`
	Entry   *sandboxfs.Entry    `json:"entry,omitempty"`
	Usage   *sandboxfs.Usage    `json:"usage,omitempty"`
	Info    *sandboxfs.FileInfo `json:"info,omitempty"`
	Plan    *ZipMeasure         `json:"plan,omitempty"`

	Data []byte `json:"data,omitempty"`
	EOF  bool   `json:"eof,omitempty"`
}

// ZipMeasure is a ZipPlan as it travels: what was selected and how big it is.
// The items themselves stay on the runner — planning is cheap, and planning
// afresh when writing is cheaper than carrying a file list of a whole home
// through the control channel.
type ZipMeasure struct {
	Name  string   `json:"name"`
	Files int      `json:"files"`
	Bytes int64    `json:"bytes"`
	Paths []string `json:"paths"`
}

// chunkLimit bounds one message's payload. The control channel is meant to stay
// narrow — a 4 GB download travels as many small messages rather than as one
// that no read limit would survive.
const chunkLimit = 512 << 10

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
	// Images this runner holds. On a runner the image is a statement of
	// capacity: it gets only agents whose workplace it can actually provide.
	// Empty = it makes no claim, and then it is not excluded on that ground —
	// which is what the built-in runner does, since the control plane can look
	// at its images itself.
	Images []string `json:"images,omitempty"`
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
	// ImageHint is how one obtains this image, for the case that it is not
	// there. It travels along because only the control plane can answer it: the
	// runner sees an image reference, and which profile it belongs to — and
	// whether the instance has renamed it — is known here. Empty = an image
	// the catalogue does not know.
	ImageHint string `json:"image_hint,omitempty"`
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
	// DurationMS and Reason are filled in by the control plane, not the runner:
	// it is the one that knows how long it waited and what asked for the sync.
	DurationMS int    `json:"-"`
	Reason     string `json:"-"`
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
	// Hints maps image → how one obtains it, for the same reason as
	// StartSandbox.ImageHint: the catalogue lives on the control plane.
	Hints map[string]string `json:"hints,omitempty"`
	// Report are images whose presence is asked about WITHOUT it being a
	// problem when they are missing — the interface's list of workplaces. The
	// difference from Images matters: a fresh installation must not be warned
	// about a dev image nobody wants, but it may still show that it is not
	// there.
	Report []string `json:"report,omitempty"`
}

// CheckResult lists what stands in the way. Empty = nothing does.
// PullImage is the request: one image, on this runner.
type PullImage struct {
	Image string `json:"image"`
}

// PullResult reports what came of it. Err empty = the image now lies here.
type PullResult struct {
	Image string `json:"image"`
	Err   string `json:"err,omitempty"`
}

type CheckResult struct {
	Problems []string `json:"problems,omitempty"`
	// Present answers Check.Report: image → is it here.
	Present map[string]bool `json:"present,omitempty"`
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
