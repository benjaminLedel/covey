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

	"covey/internal/sandbox"
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
	// TypeAddServices brings services up beside a sandbox that is already
	// running. The path an agent takes for itself: it finds a compose file in a
	// checkout it made during THIS run, and a mechanism that only took effect
	// at the next wake would be one nobody uses.
	TypeAddServices = "add_services"
	// TypeSetLogLevel raises or lowers what a runner reports. Normally info;
	// debug for as long as somebody is looking at a problem on that one host.
	// A level that could only be set at start-up would mean an SSH session and
	// a restart to answer a question — and a restart is what destroys the
	// state the question was about.
	TypeSetLogLevel = "set_log_level"
	// TypeUpdate tells the runner to replace its own binary and start again.
	// Runner and control plane are delivered separately, so they drift apart —
	// and the fix for a bug in the data plane is otherwise an SSH session per
	// host. A capability statement that can only be corrected on the machine is
	// one that stays wrong; the same is true of a version.
	TypeUpdate = "update"
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
	TypeUpdateResult   = "update_result"
	// TypeLogLevelResult confirms the level a runner is now reporting at — the
	// level it APPLIED, not the one it was asked for, so that a refused value
	// shows up in the interface instead of looking like it took.
	TypeLogLevelResult = "log_level_result"
	// TypeProgress says what a host is DOING while a start takes its time.
	// Without it a wake that fetches a multi-gigabyte image is indistinguishable
	// from one that hangs: the agent stands in "triggered" for ten minutes and
	// the recording — the one place somebody looks — has nothing at all to say.
	// It is not an answer to anything; it arrives unasked, several times per
	// start.
	TypeProgress = "progress"
	// TypeLog carries what the runner writes to its own log, so that it can be
	// read where the runner is administered instead of only on the host. It
	// arrives unasked and in batches: a line per message would turn a busy
	// start into hundreds of frames, and the lines are worth having together
	// anyway — what one says is rarely the answer, what five say in a row
	// usually is.
	TypeLog = "log"
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

// Progress is one line about a start that is under way. Not a measurement and
// not a percentage — what it answers is "is anything happening", and the honest
// form of that is a phase with the thing it is working on.
type Progress struct {
	AgentID uuid.UUID `json:"agent_id"`
	Phase   string    `json:"phase"`
	Detail  string    `json:"detail,omitempty"`
	Bytes   int64     `json:"bytes,omitempty"`
	MS      int64     `json:"ms,omitempty"`
	// How far along the phase is, in the two units a phase can be measured in:
	// bytes over the wire and entries dealt with. Both totals are 0 where the
	// phase does not know its own end — a scan finds out how much there is by
	// walking it — and then the running figure alone is the sign of life.
	//
	// Kept apart rather than one pair of "count/total", because an image counts
	// bytes and a home counts files, and a display that has to guess which one
	// it was given ends up showing "3.4 GB of 9,870".
	BytesTotal int64 `json:"bytes_total,omitempty"`
	Count      int64 `json:"count,omitempty"`
	CountTotal int64 `json:"count_total,omitempty"`
	// Done marks the last report of a phase: from here the figures are the
	// result and no longer a snapshot. Whoever renders progress needs to know
	// when to stop showing it as running.
	Done bool `json:"done,omitempty"`
}

// The phases a start goes through on the host. Named rather than numbered:
// they are what somebody reads in the recording, and their order is not the
// point — which one is taking the time is.
const (
	// PhaseHome: the working copy is being brought to the state of the
	// snapshot. Costs nothing on the host the agent last ran on and minutes on
	// a fresh one.
	PhaseHome = "home"
	// PhaseImage: the workplace image is not on this host and is being
	// fetched. This is the phase that makes a start take an hour.
	PhaseImage = "image"
	// PhaseHomeSync: the working copy is being written back into the store.
	// The other direction of PhaseHome, and the one that reported nothing at
	// all until it was finished — for a grown home that was a quarter of an
	// hour in which the platform looked stuck and could not say otherwise.
	PhaseHomeSync = "home_sync"
	// PhaseHomeSynced: the working copy went into the store — written by the
	// control plane, which is where the figures arrive.
	PhaseHomeSynced = "home_synced"
)

// Update is an order to replace one's own binary. Version empty = the newest
// published release; BaseURL empty = the releases of the public repository.
// Both exist for the same case: an installation that does not fetch from
// GitHub, and one that wants a version other than the newest.
type Update struct {
	Version string `json:"version,omitempty"`
	BaseURL string `json:"base_url,omitempty"`
}

// UpdateResult says what happened. From and To are the versions before and
// after — "already up to date" is an answer, not a failure, and it needs both
// figures to be readable as one.
type UpdateResult struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	// Planned: the host was busy and kept the wish. It carries it out at the
	// next moment it has nothing in its hands. Without this an agent with a
	// warm sandbox made a host unupdatable for good — it is never empty, and
	// every attempt was refused with the same sentence.
	Planned bool `json:"planned,omitempty"`
	// Restarting: the binary was replaced and the runner is going down to come
	// back. The connection ends right after this message, and that is not a
	// fault — without saying so, an operator would see a host that vanishes at
	// the moment they pressed the button.
	Restarting bool `json:"restarting,omitempty"`
	// Busy: refused because this host is carrying sandboxes. A field and not
	// only a sentence, because the control plane acts on it — it turns the
	// refusal into a plan for the next gap — and acting on a string somebody
	// may reword is how that stops working silently.
	Busy bool   `json:"busy,omitempty"`
	Err  string `json:"err,omitempty"`
}

// CapacityReport is what a runner is carrying.
type CapacityReport struct {
	Sandboxes int `json:"sandboxes"`
	// MaxSandboxes is what this host allows at once, 0 = no limit. Reported
	// beside the count rather than derived from it: "3 running" and "3 of 4"
	// are different answers to "can this machine take another one".
	MaxSandboxes int    `json:"max_sandboxes,omitempty"`
	TotalBytes   int64  `json:"total_bytes"`
	FreeBytes    int64  `json:"free_bytes"`
	WorkDir      string `json:"work_dir,omitempty"`
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
	// Window is the flow control of a streaming answer (open, zip): how many
	// chunks the runner may have in flight before it waits for a stream_credit.
	// 0 = none, the runner sends as fast as the link takes — which is what a
	// control plane from before #156 asks for, and what it can handle only by
	// blocking its read loop on a slow reader, which then blocks everything
	// else on that connection.
	Window int `json:"window,omitempty"`
}

// TypeStreamCredit (control plane → runner) lets a stream continue: the reader
// has consumed chunks and the runner may send as many again. A negative credit
// cancels the stream — the browser closed the download, and the rest of a 4 GB
// file has nowhere to go.
const TypeStreamCredit = "stream_credit"

// StreamCredit is the payload; the message's ID names the stream.
type StreamCredit struct {
	Chunks int `json:"chunks"`
}

// FeatureStreamCredit: this runner honours HomeOp.Window and stream_credit. A
// control plane sends credits only to a host that announced it, because to an
// older one they are unknown messages — one warning line per chunk.
const FeatureStreamCredit = "stream_credit"

// streamWindow is how many chunks a stream may have in flight: enough that a
// fast reader never waits for the round trip, few enough that a slow one costs
// the connection a couple of megabytes of buffer and nothing else.
const streamWindow = 4

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
	// Features are what this build can do beyond the messages that have always
	// existed. A new, optional field rather than a new protocol version — the
	// handshake demands an exact version match, so raising it would disconnect
	// every runner in the field, which is precisely the population that needs
	// to be told something instead of dropped.
	//
	// It answers one question: may the control plane offer an action whose
	// silence would otherwise be indistinguishable from a hang? An older runner
	// ignores an unknown message, and the caller would wait out the timeout for
	// a message that will never be answered.
	Features []string `json:"features,omitempty"`
	// Running are the agents whose sandboxes this host holds at the moment it
	// connects — found on the host, not remembered. The control plane
	// reconciles: what it placed here it adopts, and what it does not know is
	// stopped and its home secured (#155). That is the body spec/16's `prune`
	// was meant to have. Absent from an older runner, which then reports
	// nothing and is reconciled with nothing.
	Running []uuid.UUID `json:"running,omitempty"`
	// MaxSandboxes is how many sandboxes this host will carry at once, 0 = no
	// limit. It belongs to the RUNNER and is configured there, because what it
	// states is a property of the iron — how much RAM this machine has, how
	// many containers its disk stands. The control plane can rank hosts; it
	// cannot know that one of them is a laptop.
	//
	// Reported rather than assigned, and enforced on both sides: the scheduler
	// stops choosing a host that is full, and the host refuses a start beyond
	// its limit anyway. An older control plane that does not know the field
	// still cannot overload a runner that does.
	MaxSandboxes int `json:"max_sandboxes,omitempty"`
}

// FeatureSelfUpdate: this runner understands `update` and can replace its own
// binary. Missing from every build before it existed — those are installed once
// by hand, and can then update themselves.
const FeatureSelfUpdate = "self_update"

// FeatureLogShipping: this runner sends its own log up the link. A build from
// before it does not, and silence from such a host is indistinguishable from a
// host that has nothing to say — which is the worse of the two, because the
// empty panel looks like an answer. The interface asks for this flag so it can
// say "this build does not ship its log yet" instead.
//
// A flag and not a protocol bump: the handshake demands an exact version match,
// so raising it would disconnect every runner in the field — precisely the
// population that needs to be told something rather than dropped.
const FeatureLogShipping = "log_shipping"

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
	// Fallbacks are older states, newest first, for the case that Snapshot
	// cannot be read — a block the store lost takes its snapshot with it, and
	// without a way back that ends the agent (#138).
	//
	// The list travels with the start rather than being asked for afterwards:
	// the runner is the only one that finds out a manifest is unreadable, and a
	// second round trip to learn what else there is would make every failed
	// wake a conversation. An older runner ignores the field and behaves as
	// before — additive, like every other field here.
	Fallbacks []string `json:"fallbacks,omitempty"`
	// SnapshotAt is when Snapshot was taken, by the control plane's clock. It is
	// what lets the runner tell a restore from a reversal (#153): a working copy
	// that was synced or put to work AFTER this moment is a later state than the
	// snapshot, and materialising the snapshot over it would undo a run. Zero =
	// an older control plane that cannot say; the runner then behaves as before.
	SnapshotAt time.Time `json:"snapshot_at,omitempty"`
	// Excludes are the sync's exclusions (see SyncHome), for the sync a start
	// runs itself when the copy turns out to be the later state.
	Excludes []string `json:"excludes,omitempty"`
	// ImageHint is how one obtains this image, for the case that it is not
	// there. It travels along because only the control plane can answer it: the
	// runner sees an image reference, and which profile it belongs to — and
	// whether the instance has renamed it — is known here. Empty = an image
	// the catalogue does not know.
	ImageHint string `json:"image_hint,omitempty"`
	// Services run BESIDE this sandbox for as long as it lives: a database, a
	// queue, whatever the project needs in order to be started at all. The
	// runner brings them up on a network belonging to this sandbox, where each
	// is reachable under its name — the agent connects to `db:5432` and neither
	// operates them nor knows the host they run on.
	//
	// Where the list comes from is decided on the control plane; the protocol
	// stays out of it. That is deliberate, because the source is the part still
	// expected to change: today the agent declares it, later a project may.
	//
	// Empty = no services, which is the normal case and the one every agent
	// that only writes wiki pages stays in.
	Services []sandbox.Service `json:"services,omitempty"`
}

// AddServices asks for services beside a sandbox that is already up. The
// control plane has already decided WHETHER they may run — the organisation's
// allowlist is its question, and the runner would have to be a database client
// to ask it (spec/16, "Trust boundary").
type AddServices struct {
	AgentID  uuid.UUID         `json:"agent_id"`
	Services []sandbox.Service `json:"services"`
}

// TypeServicesAdded is the answer: what came up, with the image each one
// actually started from.
const TypeServicesAdded = "services_added"

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
	// Services are the ones that came up with this sandbox, with the image
	// each one actually started from. Empty for an agent that declared none.
	Services []sandbox.ServiceRun `json:"services,omitempty"`
	Err      string               `json:"err,omitempty"`
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

// LogEntry is one line a runner wrote. Deliberately flat and stringly typed:
// this is the shape slog hands over, it survives a JSON round trip without a
// schema, and whatever a future runner version invents as an attribute arrives
// at an older control plane as text instead of as an error.
type LogEntry struct {
	Time    time.Time         `json:"t"`
	Level   string            `json:"l"`
	Msg     string            `json:"m"`
	Attrs   map[string]string `json:"a,omitempty"`
	AgentID uuid.UUID         `json:"agent_id,omitempty"`
}

// LogBatch is what one flush carries. Dropped says how many lines were thrown
// away before this batch because the buffer was full — a number, once, instead
// of a silence that looks like quiet.
type LogBatch struct {
	Entries []LogEntry `json:"entries"`
	Dropped int        `json:"dropped,omitempty"`
}

// SetLogLevel asks a runner to report at this level from now on.
type SetLogLevel struct {
	Level string `json:"level"`
}

// LogLevels are the ones a runner accepts. Not slog's whole range: the two
// above info are what a runner writes anyway, and offering a level nobody can
// choose usefully only invites choosing it.
const (
	LogLevelDebug = "debug"
	LogLevelInfo  = "info"
)

// ValidLogLevel keeps a typo from silencing a host. An unknown level is
// refused rather than rounded to the nearest one — "warn" would look like it
// worked and hide exactly the lines somebody switched the level to see.
func ValidLogLevel(level string) bool {
	return level == LogLevelDebug || level == LogLevelInfo
}
