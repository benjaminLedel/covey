package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/homestore"
	"covey/internal/orchestrator"
	"covey/internal/sandbox"
	"covey/internal/sandboxfs"
)

// Pool is the control plane's side of the runner protocol and at the same time
// the orchestrator's SandboxProvider: from up there, "start a sandbox" looks
// exactly as it did when the local Docker CLI was called directly.
//
// It knows agents and profiles; a runner knows images and containers. That cut
// is why the runner needs no access to anything the platform holds.
type Pool struct {
	Log *slog.Logger
	// Profiles maps a
	// profile name to its image (spec/16). A value that is neither is taken as
	// an image reference — the "org-owned: anything" row of the profile table.
	Profiles map[string]string
	// Catalog is the published workplace catalogue (spec/16): which image
	// belongs to which Covey version, pinned by digest. Optional — without it
	// Profiles stands, which is the state before the catalogue existed.
	Catalog *sandbox.Source
	// OrgImages resolves the workplaces an organisation brought along itself:
	// name → image (spec/16). nil = none, then only the catalogue's names
	// resolve and anything else stays a literal image reference.
	OrgImages func(ctx context.Context, orgID uuid.UUID) map[string]string
	// AllOrgImages is the same across all organisations — for the readiness
	// check, which asks about the host and not about a tenant. Two
	// organisations may use the same name for different images; for a question
	// that only asks "does this image exist here" that is harmless, and the
	// alternative would be to ask the same host once per tenant.
	AllOrgImages func(ctx context.Context) map[string]string
	// EnvImages are the instance's explicit overrides
	// (COVEY_SANDBOX_IMAGE_<PROFILE>). They beat the catalogue: whoever named
	// an image on their own host has the last word, and a remote file must not
	// overrule it.
	EnvImages map[string]string

	// profMu guards the resolved map. It is recomputed rather than held,
	// because the catalogue behind it can change while the process runs — a
	// release is published and the next wake takes the image built for it.
	profMu  sync.RWMutex
	profMap map[string]string
	profAt  time.Time
	// AgentImages reports which workplaces the agents are configured for
	// (value → number of agents); the self-check asks the runners about
	// exactly those. nil = unknown, then only the default is asked about.
	AgentImages func(ctx context.Context) (map[string]int, error)
	// SandboxDied is called when a runner reports the end of a sandbox nobody
	// asked for. nil = the report is only logged.
	SandboxDied func(agentID uuid.UUID, reason string)
	// Heard is called when a runner reports in — the moment "last seen"
	// actually means something. Without it the figure would be the time a
	// runner CONNECTED, which is the one thing nobody wants to know about a
	// runner that has since gone away.
	Heard func(runnerID uuid.UUID)
	// RunnerLabel is how a host is called in the interface right now — the
	// operator's name for it, and what belongs in the recording beside the id.
	// An id alone answers "which host" only for whoever keeps a mapping in
	// their head.
	RunnerLabel func(ctx context.Context, runnerID uuid.UUID) string

	// LastRunner names the host this agent last worked on — its home's working
	// copy is there. Answering it is worth an ordering, not a requirement: a
	// host that is gone must not keep an agent waiting, and a home is
	// materialised from the store anywhere else. nil = no affinity, which is
	// what an installation without a home store looks like.
	LastRunner func(ctx context.Context, agentID uuid.UUID) (uuid.UUID, bool)

	// Capabilities asks what an operator assigned to a runner in the
	// interface — tags on top of what the host reports, and the workplaces it
	// is to provide. nil = nothing is assigned anywhere, which is what a pool
	// without a database looks like (tests, the runner side).
	Capabilities func(ctx context.Context, runnerID uuid.UUID) (extraTags, images []string, decided, paused bool, err error)
	// Phases is what the hosts are busy with at this moment, per agent. Set by
	// NewPool; whoever builds a Pool by hand and leaves it nil loses the live
	// display and nothing else.
	Phases *Phases
	// Progress receives what a host says about a start that is under way —
	// fetching an image, materialising a working copy. nil = nobody is
	// listening, and the runner's lines are dropped. The control plane wires it
	// to the recording: a wake that downloads four gigabytes has to be
	// distinguishable from one that hangs, and the recording is where somebody
	// looks for the difference.
	Progress func(orgID, runnerID uuid.UUID, p Progress)

	// PlannedUpdate asks whether an update is waiting for this host to become
	// idle, and UpdatePlanned* record what came of it. nil = nothing is
	// planned anywhere, which is what a pool without a database looks like.
	//
	// The wait is the point: a runner that carries sandboxes refuses an update
	// for a good reason, and until now the waiting was left to a human who had
	// to press the button again at the right minute.
	PlannedUpdate     func(ctx context.Context, runnerID uuid.UUID) (string, error)
	PlannedUpdateDone func(ctx context.Context, runnerID uuid.UUID, version string)
	// RunnerDownloadBase is where a planned update fetches its binary from —
	// the same address the interface would have sent.
	RunnerDownloadBase string

	// LogLevelFor answers what level this runner is meant to report at. Asked
	// on every registration, because the level is a property of the runner and
	// not of the connection: a host somebody switched to debug that drops out
	// for a minute must come back at debug, or the switch in the interface is
	// lying about the state of the world.
	LogLevelFor func(ctx context.Context, runnerID uuid.UUID) string

	// Logs receives what a runner writes to its own log. Like Progress it
	// answers nothing and belongs to no correlation — it arrives unasked and
	// goes straight to whoever keeps it.
	Logs func(orgID, runnerID uuid.UUID, batch LogBatch)

	// EnsureLocal is asked when an organisation has no connected runner: the
	// built-in one is created on first use, because organisations come into
	// being while the process runs. nil = nothing is created, and the
	// organisation simply has no runner.
	EnsureLocal func(ctx context.Context, orgID uuid.UUID) error
	// LatestSnapshot is the state an agent's home is materialised to on wake.
	// nil or an empty hash = the working copy on the runner applies, which is
	// what the very first wake looks like.
	LatestSnapshot func(ctx context.Context, agentID uuid.UUID) (string, error)
	// SnapshotChain are the states to try, newest first, when the newest one
	// cannot be read (#138). nil = no way back, the behaviour before.
	SnapshotChain func(ctx context.Context, agentID uuid.UUID) ([]string, error)
	// SnapshotTaken files a completed sync. Only afterwards may anything be
	// cleaned up locally — no prune before a successful sync (spec/16).
	SnapshotTaken func(ctx context.Context, agentID, runnerID uuid.UUID, res HomeSynced) error
	// SnapshotFailed files a sync that did NOT happen. It exists because the
	// failure used to leave one line in the runner's debug log and nothing
	// else: the interface went on showing the last snapshot that did work,
	// with no hint that everything since was unprotected. On a production
	// instance that state lasted weeks and cost a 39-minute run.
	//
	// A failure is not an error of the caller's — the run is over either way —
	// so this reports rather than returns. nil = nothing is recorded, which is
	// what the tests use.
	SnapshotFailed func(ctx context.Context, agentID, runnerID uuid.UUID, reason, msg string)
	// AgentHome answers the three things file access needs about an agent:
	// whose organisation it is, which runner it last ran on, and which snapshot
	// its home was last synced to. nil = the pool can only serve agents whose
	// runner is connected, and nothing from a snapshot.
	HomeInfo func(ctx context.Context, agentID uuid.UUID) (orgID, lastRunner uuid.UUID, snapshot string, err error)
	// Blobs is the home store. With it a home stays readable while its runner
	// is offline — from its last snapshot, read-only.
	Blobs homestore.BlobStore
	// HomeExcludes are the paths left out of the sync. Empty = everything is
	// synced, which is the default on purpose: the list is a cost question, not
	// a prerequisite for correctness.
	HomeExcludes []string
	// HeartbeatEvery is how often the watchdog looks, and SilenceAfter how long
	// a runner may be quiet before its connection is treated as gone. 0 = the
	// protocol's defaults. Settable because a test must not have to wait out a
	// real silence to check what happens after one.
	HeartbeatEvery time.Duration
	SilenceAfter   time.Duration
	// StartTimeout bounds how long a start may take before it counts as
	// failed. Without it a runner that has gone quiet would hold the wake
	// until the orchestrator's ReadyTimeout — and the message would then blame
	// the daemon for something the runner never did.
	//
	// 0 = defaultStartTimeout. The instance sets it through
	// COVEY_SANDBOX_START_TIMEOUT.
	StartTimeout time.Duration

	mu    sync.Mutex
	conns map[uuid.UUID]*conn // runner ID → connection
	// ensuring serialises EnsureLocal per organisation: without it, two
	// simultaneous wakes in a fresh organisation would each start a built-in
	// runner, and the second would take the first one's place.
	ensuring map[uuid.UUID]*sync.Mutex
	// dirty are the homes the file browser has changed since the last sync. A
	// write through the browser lives only in the runner's working copy, and an
	// agent that wakes elsewhere in the meantime would materialise a snapshot
	// that does not have it.
	dirty map[uuid.UUID]*dirtyHome
	// local is the built-in runner when it runs in this process. It is kept
	// only for the short path to the home: reading a file from the directory
	// next door does not need a round trip through the protocol.
	local *Node
}

// conn is one connected runner.
type conn struct {
	runnerID uuid.UUID
	orgID    uuid.UUID
	builtin  bool
	protocol int
	version  string
	arch     string
	// tags/images are the EFFECTIVE sets the scheduler matches against: what
	// the runner reported about itself, plus (tags) or replaced by (images)
	// what an operator assigned in the interface. The reported halves stay
	// beside them — for the runner view, and to recompute when an assignment
	// changes while the connection stands.
	tags           []string
	images         []string
	reportedTags   []string
	reportedImages []string
	// features are what this build of the runner announced it can do — see
	// Registered.Features.
	features []string
	t        Transport
	pool     *Pool

	mu      sync.Mutex
	waiters map[string]chan Message
	// lastHeard is when anything last arrived from this runner. Any message
	// counts, not only a heartbeat: traffic is proof of life, and a runner
	// busy answering does not have to say so twice.
	lastHeard time.Time
	// streams marks the correlations that answer with several messages. Without
	// it the first chunk would end the correlation and the rest would arrive
	// with nobody waiting.
	streams map[string]bool
	// capacity is the last thing this host said about its disk, and capacityAt
	// when it said it. Kept rather than asked at the moment somebody looks:
	// see capacityWatch. capacityAt is also the answer to a second question —
	// see answering.
	capacity CapacityReport
	// updating/updateTried gehören zum geplanten Update: eins zur Zeit, und
	// nach einem Fehlschlag nicht sofort wieder.
	updating    bool
	updateTried time.Time
	capacityAt  time.Time
	// openedAt is when this connection came up. It stands in for an answer
	// that has not arrived yet: a host that has just connected is given the
	// same grace as one that has just answered.
	openedAt time.Time
	// pausedNow: an operator has taken this host out of service. It keeps its
	// connection, its token and its working copies — it just gets nothing new.
	pausedNow bool
	// maxSandboxes is what this host said it will carry at once, 0 = no limit.
	// It comes from the runner's own configuration: how much a machine can
	// take is a fact about the machine.
	maxSandboxes int
	// sandboxes counts what is running here — the whole of the scheduling
	// weight for now: no bin packing, no resource modelling.
	sandboxes int
	// gone is closed when this connection is over. It is what turns a dropped
	// runner into an answer: without it an outstanding question waits for its
	// own timeout — thirty minutes for a home sync — for an answer nobody will
	// ever send. Measured on covey.work: a runner restarted mid-sync at 14:33
	// and the agent read `securing` until 15:02, with nothing happening in
	// between.
	gone     chan struct{}
	goneOnce sync.Once
}

// end closes this connection out: every question still waiting on it is
// answered with the fact that there is nothing left to wait for. Idempotent,
// because a connection can be dropped from more than one place.
func (c *conn) end() {
	c.goneOnce.Do(func() { close(c.gone) })
}

// pending: how many questions this connection is still waiting on. It is the
// second half of "is this host idle" — the first, the sandbox count, says
// nothing about the home it may be writing at this moment, and writing a home
// is the most valuable thing a host does.
func (c *conn) pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// ErrNoRunner: this organisation has nothing that could carry the sandbox.
var ErrNoRunner = errors.New("no runner available")

// ErrRunnerGone: the connection ended while this request was outstanding. A
// distinct error, because the two cases call for different things: a timeout
// means the host is slow and may still answer, this means it is gone and the
// request has to be made again on whatever connection comes back.
var ErrRunnerGone = errors.New("the runner disconnected")

// errNoneInOrg: the organisation has no connected runner at all. It used to be
// the ONLY case in which a built-in runner was created — a runner that is there
// but does not fit was treated as a different situation, because a built-in one
// beside it is a mixed pool.
//
// That rule cost covey.work its whole data plane: one remote runner registered,
// the control plane restarted, and the organisation had a runner that holds
// covey-sandbox:latest and an agent that needs the deploy image from the
// private registry. Every wake failed every 30 seconds while the machine that
// held that image stood right there. A mixed pool is a trade-off; a control
// plane that stops working because somebody added a host is not.
//
// So the fallback now applies to any pick that finds no candidate, and the
// mixed pool is named in the log instead of happening quietly.
var errNoneInOrg = fmt.Errorf("%w: this organisation has no connected runner", ErrNoRunner)

// defaultStartTimeout is generous on purpose, and the reason is what a start
// actually does on a host that does not have the image yet: `docker run` pulls
// it, and a workplace image is several gigabytes. Two minutes were enough while
// the deploy pre-pulled every image onto the one machine it knew. On a runner
// of its own the first start after every new image is a download, and it failed
// with "did not answer start_sandbox: context deadline exceeded" — measured on
// covey.work, where the pull alone took just under three minutes.
//
// A runner that is genuinely DEAD is not caught by this at all: the heartbeat
// is (three missed beats, ~90 seconds), and it closes the connection
// regardless of what a start is waiting for. So this bound is about slowness,
// not about liveness — and slowness deserves the hour.
const defaultStartTimeout = 60 * time.Minute

func NewPool(log *slog.Logger) *Pool {
	if log == nil {
		log = slog.Default()
	}
	return &Pool{
		Log: log, conns: map[uuid.UUID]*conn{},
		ensuring: map[uuid.UUID]*sync.Mutex{},
		dirty:    map[uuid.UUID]*dirtyHome{},
		Phases:   NewPhases(),
	}
}

// Attach takes a connected runner into the pool and speaks the protocol with
// it until the transport ends. builtin marks the runner the control plane runs
// itself.
func (p *Pool) Attach(ctx context.Context, t Transport, builtin bool) error {
	c, err := p.register(ctx, t, builtin)
	if err != nil {
		return err
	}
	defer p.detach(c)
	return c.readLoop(ctx)
}

// AttachRemote takes a runner that has authenticated with its token. The
// identity from the handshake is checked against the token's: a runner that
// named a foreign ID would otherwise take another one's place in the pool, and
// with it another organisation's sandboxes.
//
// noteCapabilities receives what the runner reported about itself, so that
// version drift becomes visible instead of merely being suspected.
func (p *Pool) AttachRemote(ctx context.Context, t Transport, runnerID, orgID uuid.UUID,
	noteCapabilities func(version, arch string, protocol int)) error {
	c, err := p.register(ctx, t, false)
	if err != nil {
		return err
	}
	if c.runnerID != runnerID || c.orgID != orgID {
		p.detach(c)
		return fmt.Errorf("runner %s reports a different identity (%s/%s) than its token — refused",
			runnerID, c.runnerID, c.orgID)
	}
	if noteCapabilities != nil {
		noteCapabilities(c.version, c.arch, c.protocol)
	}
	defer p.detach(c)
	return c.readLoop(ctx)
}

// register performs the handshake and takes the runner into the pool.
func (p *Pool) register(ctx context.Context, t Transport, builtin bool) (*conn, error) {
	// The first message has to be `registered`: without an identity the pool
	// does not know whose organisation this runner serves, and assigning it
	// anything would be a guess about the tenant.
	msg, err := t.Receive(ctx)
	if err != nil {
		return nil, err
	}
	if msg.Type != TypeRegistered {
		return nil, fmt.Errorf("runner: first message %q, expected %q", msg.Type, TypeRegistered)
	}
	reg, err := decode[Registered](msg)
	if err != nil {
		return nil, err
	}
	if reg.Protocol != Protocol {
		// Refused with a reason, and naming which side is behind: a runner
		// that quietly fails to connect costs an evening of searching.
		side := "the runner"
		if reg.Protocol > Protocol {
			side = "the control plane"
		}
		return nil, fmt.Errorf("runner %s speaks protocol %d, this control plane speaks %d — %s needs updating",
			reg.RunnerID, reg.Protocol, Protocol, side)
	}

	c := &conn{
		runnerID: reg.RunnerID, orgID: reg.OrgID, builtin: builtin,
		protocol: reg.Protocol, version: reg.Version, arch: reg.Arch,
		tags: reg.Tags, images: reg.Images,
		reportedTags: reg.Tags, reportedImages: reg.Images,
		features: reg.Features, maxSandboxes: reg.MaxSandboxes,
		t: t, pool: p, waiters: map[string]chan Message{}, streams: map[string]bool{},
		lastHeard: time.Now(), openedAt: time.Now(), gone: make(chan struct{}),
	}
	// What the interface assigned to this host applies from the first wake on,
	// not from the next restart of the runner: the capabilities live here, the
	// host only says what it knows about itself.
	if p.Capabilities != nil {
		extra, images, decided, paused, err := p.Capabilities(ctx, reg.RunnerID)
		if err != nil {
			p.Log.Warn("assigned capabilities not readable — the runner's own claim applies",
				"runner", reg.RunnerID, "err", err)
		} else {
			c.applyAssigned(extra, images, decided)
			// A pause survives a reconnect. Otherwise restarting the runner
			// would be the way around it, and a maintenance window that a
			// restart ends is none.
			c.setPaused(paused)
		}
	}
	p.mu.Lock()
	p.conns[reg.RunnerID] = c
	p.mu.Unlock()
	p.Log.Info("runner connected", "runner", reg.RunnerID, "org", reg.OrgID,
		"builtin", builtin, "version", reg.Version)
	// The stored log level, re-applied. In the background on purpose: a
	// registration that waited for it would let a slow answer delay the first
	// wake, and the worst case of failing here is a host that reports at info
	// until somebody switches it again.
	if p.LogLevelFor != nil {
		go func() {
			level := p.LogLevelFor(context.WithoutCancel(ctx), reg.RunnerID)
			if !ValidLogLevel(level) || level == LogLevelInfo {
				return
			}
			ask, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
			defer cancel()
			if _, err := c.ask(ask, TypeSetLogLevel, SetLogLevel{Level: level}, 15*time.Second); err != nil {
				p.Log.Warn("log level could not be restored on the runner",
					"runner", reg.RunnerID, "level", level, "err", err)
			}
		}()
	}
	return c, nil
}

// applyAssigned recomputes the effective sets. Tags are a union — a host does
// not stop being arm64 because somebody labelled it "build". Images are a
// replacement when the operator has decided, because the point of deciding is
// to be able to take back a claim the registration invented.
func (c *conn) applyAssigned(extraTags, images []string, decided bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tags = union(c.reportedTags, extraTags)
	if decided {
		c.images = images
	} else {
		c.images = c.reportedImages
	}
}

// setPaused / paused: whether this host takes new sandboxes. Kept on the
// connection and not only in the database, because that is where the scheduler
// asks — a query per pick would put Postgres in the path of every wake.
func (c *conn) setPaused(v bool) {
	c.mu.Lock()
	c.pausedNow = v
	c.mu.Unlock()
}

func (c *conn) paused() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.pausedNow
}

// SetPaused carries a pause made in the interface to a runner that is connected
// right now — it must apply to the next wake, not to the next reconnect.
func (p *Pool) SetPaused(runnerID uuid.UUID, paused bool) {
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil {
		return
	}
	c.setPaused(paused)
}

// SetCapabilities carries an assignment made in the interface to a runner that
// is connected right now. Without it the change would apply at the next
// reconnect — and "why is it not taking anything, I gave it the tag" is exactly
// the question this feature exists to answer.
func (p *Pool) SetCapabilities(runnerID uuid.UUID, extraTags, images []string, decided bool) {
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil {
		return
	}
	c.applyAssigned(extraTags, images, decided)
}

// union keeps the order of the first set and appends what is new, compared
// without regard to case — the same comparison hasAll makes.
func union(a, b []string) []string {
	out := append([]string{}, a...)
	for _, x := range b {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		found := false
		for _, have := range out {
			if strings.EqualFold(strings.TrimSpace(have), x) {
				found = true
				break
			}
		}
		if !found {
			out = append(out, x)
		}
	}
	return out
}

func (p *Pool) detach(c *conn) {
	// Zuerst: wer noch auf eine Antwort wartet, wartet ab jetzt vergeblich und
	// soll das sofort erfahren statt in einer halben Stunde.
	c.end()
	p.mu.Lock()
	if p.conns[c.runnerID] == c {
		delete(p.conns, c.runnerID)
	}
	p.mu.Unlock()
	p.Log.Info("runner disconnected", "runner", c.runnerID)
}

// AttachLocal wires the built-in runner: one connection, both ends in this
// process. It returns once the runner is registered — the caller may assign it
// something straight away.
//
// The node is remembered for the short path to the home: reading a file from
// the directory next door does not need a round trip through the protocol.
// Which of several built-in runners answers does not matter there, because a
// home is addressed by agent and not by organisation.
func (p *Pool) AttachLocal(ctx context.Context, node *Node) error {
	control, nodeEnd := NewInProc()
	go func() {
		// The built-in runner does not reconnect: this one call is its whole
		// life, so its watchers end with it.
		defer node.Close()
		if err := node.Run(ctx, nodeEnd); err != nil && ctx.Err() == nil {
			p.Log.Error("built-in runner ended", "err", err)
		}
	}()
	c, err := p.register(ctx, control, true)
	if err != nil {
		_ = control.Close()
		return err
	}
	p.mu.Lock()
	p.local = node
	p.mu.Unlock()
	go func() {
		defer p.detach(c)
		if err := c.readLoop(ctx); err != nil && ctx.Err() == nil {
			p.Log.Error("built-in runner: connection ended", "err", err)
		}
	}()
	return nil
}

func (c *conn) readLoop(ctx context.Context) error {
	watch, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go c.watchdog(watch)
	go c.capacityWatch(watch)

	for {
		msg, err := c.t.Receive(ctx)
		if err == nil {
			c.mu.Lock()
			c.lastHeard = time.Now()
			c.mu.Unlock()
			if c.pool.Heard != nil {
				c.pool.Heard(c.runnerID)
			}
		}
		if err != nil {
			if errors.Is(err, ErrTransportClosed) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		// An answer to something we asked goes to whoever is waiting; anything
		// else the runner says by itself is handled here.
		if msg.ID != "" {
			c.mu.Lock()
			ch, streaming := c.waiters[msg.ID], c.streams[msg.ID]
			if !streaming {
				delete(c.waiters, msg.ID)
			}
			c.mu.Unlock()
			if ch != nil {
				// A streaming answer (a download, an archive) arrives as several
				// messages under one ID. Blocking here is on purpose: it is what
				// keeps a slow reader from letting the runner fill our memory
				// with a 4 GB file.
				select {
				case ch <- msg:
				case <-ctx.Done():
					return nil
				}
				continue
			}
		}
		switch msg.Type {
		case TypeSandboxExited:
			ev, err := decode[SandboxExited](msg)
			if err != nil {
				continue
			}
			c.mu.Lock()
			if c.sandboxes > 0 {
				c.sandboxes--
			}
			c.mu.Unlock()
			c.pool.Log.Warn("sandbox ended without being asked to",
				"agent", ev.AgentID, "runner", c.runnerID, "reason", ev.Reason)
			if c.pool.SandboxDied != nil {
				c.pool.SandboxDied(ev.AgentID, ev.Reason)
			}
		case TypeProgress:
			// A line about a start that is under way. It answers nothing and
			// belongs to no correlation — it goes into the recording, which
			// keeps it, and into Phases, which is where "what is this agent
			// waiting on right now" is asked.
			ev, err := decode[Progress](msg)
			if err != nil {
				continue
			}
			c.pool.Phases.Note(c.runnerID, ev)
			if c.pool.Progress == nil {
				continue
			}
			c.pool.Progress(c.orgID, c.runnerID, ev)
		case TypeLog:
			// A batch of the runner's own log lines, so that the host can be
			// read where it is administered instead of only over SSH.
			if c.pool.Logs == nil {
				continue
			}
			batch, err := decode[LogBatch](msg)
			if err != nil {
				continue
			}
			c.pool.Logs(c.orgID, c.runnerID, batch)
		case TypeHeartbeat:
		case TypeHomeResult:
			// A chunk whose reader has gone: a download the browser cancelled.
			// Dropped silently — the alternative is a warning per chunk, which
			// turns one cancelled download into a page of log.
		default:
			c.pool.Log.Warn("runner: unexpected message", "type", msg.Type, "runner", c.runnerID)
		}
	}
}

// askStream sends a request whose answer arrives as several messages. The
// caller reads until a message carries EOF and must always call the returned
// stop — otherwise the correlation stays registered and the connection
// accumulates readers nobody serves.
func (c *conn) askStream(ctx context.Context, msgType string, payload any) (<-chan Message, func(), error) {
	id := uuid.NewString()
	msg, err := encode(msgType, id, payload)
	if err != nil {
		return nil, func() {}, err
	}
	ch := make(chan Message, 4)
	c.mu.Lock()
	c.waiters[id] = ch
	c.streams[id] = true
	c.mu.Unlock()
	stop := func() {
		c.mu.Lock()
		delete(c.waiters, id)
		delete(c.streams, id)
		c.mu.Unlock()
	}
	if err := c.t.Send(ctx, msg); err != nil {
		stop()
		return nil, func() {}, err
	}
	return ch, stop, nil
}

// watchdog closes a connection that has gone quiet. Receive would otherwise
// block for ever on a half-open link — the kernel has no reason to notice that
// the other end is gone, and the pool would keep offering a runner that hears
// nothing. Closing the transport is what makes the read loop return and the
// runner leave the pool.
func (c *conn) watchdog(ctx context.Context) {
	every, silence := c.pool.HeartbeatEvery, c.pool.SilenceAfter
	if every <= 0 {
		every = HeartbeatInterval
	}
	if silence <= 0 {
		silence = Silence
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.mu.Lock()
			quiet := time.Since(c.lastHeard)
			c.mu.Unlock()
			if quiet > silence {
				c.pool.Log.Warn("runner has gone quiet — connection closed",
					"runner", c.runnerID, "silent_for", quiet.Round(time.Second))
				_ = c.t.Close()
				return
			}
		}
	}
}

// answering reports whether this host still REACTS. That is not the same
// question as whether the line is open, and the difference cost covey.work a
// night: a runner sat there as connected — heartbeat every 30 seconds, last
// seen a minute ago — while it answered nothing at all. Its read loop was
// inside a `docker run` that was pulling an image, and the heartbeat goes out
// from a goroutine of its own, so the one thing still working was the one
// thing being measured.
//
// The scheduler nonetheless saw a candidate, sent it the start, and waited out
// the start timeout — an hour, since we made room for exactly such a pull. The
// built-in runner never stepped in, because stepping in requires there to be no
// candidate. Three wakes, three hours, nothing done.
//
// So the second signal: the connection asks this host about its disk every beat
// anyway (capacityWatch), and whether an answer comes back is a statement about
// the read loop rather than about the socket. Three missed answers — the same
// tolerance the heartbeat gets — and the host stops being a candidate. It is
// not thrown out: whatever it is doing may well be legitimate and long, and a
// start that is running has to be allowed to finish. It just gets no more work
// while it cannot say a word.
// full reports whether this host is carrying as much as it said it would. A
// host without a limit is never full — 0 means "no statement", not "none".
func (c *conn) full() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.maxSandboxes > 0 && c.sandboxes >= c.maxSandboxes
}

func (c *conn) answering() bool {
	c.mu.Lock()
	last := c.capacityAt
	if last.Before(c.openedAt) {
		last = c.openedAt
	}
	c.mu.Unlock()
	silence := c.pool.SilenceAfter
	if silence <= 0 {
		silence = Silence
	}
	return time.Since(last) <= silence
}

// capacityWatch keeps a host's disk figure current without anybody asking for
// it. It used to be fetched when somebody opened the runner page — one round
// trip per host, one after the other — and a host answers this out of its read
// loop, which is exactly what a sandbox start occupies while it pulls an image
// of several gigabytes. So the page waited out the whole start, per host.
//
// Here nobody is waiting: the answer arrives when it arrives, and until then
// the previous one stands with the moment it was taken beside it. That is the
// honest form of a remembered figure — a disk that is full says so a beat late
// rather than not at all, and how late is visible.
//
// The ticker's own tempo is the heartbeat's: a figure that is at most one beat
// old is fresh enough for the decision it serves, and the ask blocks the loop
// while it runs, so there is never more than one outstanding.
func (c *conn) capacityWatch(ctx context.Context) {
	every := c.pool.HeartbeatEvery
	if every <= 0 {
		every = HeartbeatInterval
	}
	// Once straight away: the first look at the page after a runner connects
	// should not show an empty column for a whole beat.
	c.refreshCapacity(ctx)
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.refreshCapacity(ctx)
		}
	}
}

// refreshCapacity asks once and keeps what comes back. A host that does not
// answer changes nothing: the older figure and its age are better than an
// empty column, and the age is what says which of the two this is.
func (c *conn) refreshCapacity(ctx context.Context) {
	answer, err := c.ask(ctx, TypeCapacity, nil, capacityAsk)
	if err != nil {
		return
	}
	report, err := decode[CapacityReport](answer)
	if err != nil {
		return
	}
	c.mu.Lock()
	c.capacity = report
	c.capacityAt = time.Now()
	c.mu.Unlock()

	// Die Lücke, auf die ein geplantes Update wartet. Der Kapazitätsbericht ist
	// dafür die verlässlichste Quelle: er zählt, was der Host WIRKLICH trägt,
	// und nicht, was die Steuerebene glaubt.
	//
	// Er zählt allerdings nur Sandboxen. Ein Host, der keine trägt, kann gerade
	// ein Home schreiben — genau das war der Fall, in dem ein eingeplantes
	// Update einen laufenden Sync abgeschnitten hat: die Sandbox war seit einer
	// Sekunde weg, der Sync lief noch elf Minuten. Was noch offen ist, weiß die
	// Verbindung selbst.
	if report.Sandboxes == 0 && c.pending() == 0 {
		c.runPlannedUpdate(ctx)
	}
}

// plannedRetry: so lange wird nach einem missglückten Versuch nicht erneut
// gefragt. Ohne diese Bremse liefe ein Update, das an einem kaputten Download
// scheitert, alle dreißig Sekunden wieder los.
const plannedRetry = 5 * time.Minute

// runPlannedUpdate führt aus, was für diesen Host vorgemerkt ist — jetzt, wo er
// nichts trägt.
//
// Der Plan bleibt stehen, solange er nicht erfüllt ist: ein Versuch, der an
// einem Download scheitert, ist kein Grund, den Wunsch zu vergessen. Erfüllt
// ist er, wenn der Host auf der gewünschten Fassung läuft — auch wenn ihn
// jemand von Hand dorthin gebracht hat.
func (c *conn) runPlannedUpdate(ctx context.Context) {
	p := c.pool
	if p.PlannedUpdate == nil || c.builtin {
		return
	}
	c.mu.Lock()
	if c.updating || time.Since(c.updateTried) < plannedRetry {
		c.mu.Unlock()
		return
	}
	c.updating, c.updateTried = true, time.Now()
	version := c.version
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		c.updating = false
		c.mu.Unlock()
	}()

	want, err := p.PlannedUpdate(ctx, c.runnerID)
	if err != nil || strings.TrimSpace(want) == "" {
		return
	}
	if want == version {
		// Schon da — dann war der Plan die Wirklichkeit, und der Wunsch ist
		// erfüllt, ohne dass jemand etwas ersetzen musste.
		if p.PlannedUpdateDone != nil {
			p.PlannedUpdateDone(ctx, c.runnerID, want)
		}
		return
	}
	p.Log.Info("carrying out the planned update", "runner", short(c.runnerID), "to", want)
	res, err := p.Update(ctx, c.runnerID, want, p.RunnerDownloadBase)
	switch {
	case err != nil:
		p.Log.Warn("planned update did not run", "runner", short(c.runnerID), "err", err)
	case res.Busy:
		// Zwischen Bericht und Auftrag ist ein Lauf gestartet. Der Plan bleibt.
		p.Log.Info("host became busy again — the update stays planned", "runner", short(c.runnerID))
	case res.Err != "":
		p.Log.Warn("planned update failed", "runner", short(c.runnerID), "err", res.Err)
	default:
		p.Log.Info("planned update under way", "runner", short(c.runnerID),
			"from", res.From, "to", res.To, "restarting", res.Restarting)
		if p.PlannedUpdateDone != nil {
			p.PlannedUpdateDone(ctx, c.runnerID, want)
		}
	}
}

// capacityAsk bounds one such question. Generous, because nobody is waiting on
// it — and pointless to make shorter: a host that has not answered within this
// is inside something long, and the next tick asks again.
const capacityAsk = 30 * time.Second

// askStart sends the start and gives up as soon as the host stops answering —
// long before the start timeout, which is an hour because a first start may be
// a multi-gigabyte pull.
//
// Those two facts fit together badly without this. The scheduler only picks
// hosts that are answering, but "answering" is a statement about the last 90
// seconds, and a host can go deaf in the second after it was picked: on
// covey.work a runner answered 19 seconds before the wake, took the start, went
// into the pull, and the agent then stood still for the full hour with no
// message at all. The signal that would have said so was arriving the whole
// time — the capacity question every beat — and nobody was listening to it
// while the start was outstanding.
//
// A host that IS reading stays unaffected, however long its pull takes: since
// the read loop hands off, capacity is answered beside it. Not answering while
// a start is outstanding therefore means the loop is stuck, which is exactly
// the case for a runner too old to hand off — and moving to the next host is
// better for it than an hour of nothing.
func (c *conn) askStart(ctx context.Context, spec StartSandbox, timeout time.Duration) (Message, error) {
	watch, stopWatch := context.WithCancel(ctx)
	defer stopWatch()
	go func() {
		every := c.pool.HeartbeatEvery
		if every <= 0 {
			every = HeartbeatInterval
		}
		ticker := time.NewTicker(every / 3)
		defer ticker.Stop()
		for {
			select {
			case <-watch.Done():
				return
			case <-ticker.C:
				if !c.answering() {
					stopWatch()
					return
				}
			}
		}
	}()

	answer, err := c.ask(watch, TypeStartSandbox, spec, timeout)
	if err != nil && ctx.Err() == nil && !c.answering() {
		// And the start is taken back. A host whose read loop is stuck has the
		// message lying in front of it, not lost: when the pull finishes it
		// would start the container for an agent that has long since woken
		// somewhere else. The stop goes into the same per-agent queue and is
		// therefore worked on AFTER the start it cancels.
		c.tell(TypeStopSandbox, StopSandbox{AgentID: spec.AgentID})
		return Message{}, fmt.Errorf("runner %s stopped answering while the sandbox was starting — "+
			"its start was taken back and the next host asked", short(c.runnerID))
	}
	return answer, err
}

// tell sends a message whose answer nobody waits for. For the case where
// waiting is pointless: the host is not reading right now, and what matters is
// that the message lies in front of it when it does.
func (c *conn) tell(msgType string, payload any) {
	msg, err := encode(msgType, uuid.NewString(), payload)
	if err != nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(context.Background()), 30*time.Second)
		defer cancel()
		if err := c.t.Send(ctx, msg); err != nil {
			c.pool.Log.Warn("runner: message could not be handed over", "type", msgType, "runner", c.runnerID, "err", err)
		}
	}()
}

// ask sends a request and waits for its answer.
func (c *conn) ask(ctx context.Context, msgType string, payload any, timeout time.Duration) (Message, error) {
	id := uuid.NewString()
	msg, err := encode(msgType, id, payload)
	if err != nil {
		return Message{}, err
	}
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.waiters[id] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.waiters, id)
		c.mu.Unlock()
	}()

	if err := c.t.Send(ctx, msg); err != nil {
		return Message{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	select {
	case answer := <-ch:
		return answer, nil
	case <-c.gone:
		// Die Verbindung ist weg. Ein Neuaufbau ist der Beweis, dass das, was
		// die alte tat, vorbei ist — hier auf den eigenen Zeitablauf zu warten
		// hieße, eine halbe Stunde lang auf niemanden zu warten.
		return Message{}, fmt.Errorf("%w: runner %s while waiting for %s",
			ErrRunnerGone, c.runnerID, msgType)
	case <-ctx.Done():
		return Message{}, fmt.Errorf("runner %s did not answer %s: %w", c.runnerID, msgType, ctx.Err())
	}
}

// ensureAndPick gives an organisation its built-in runner and tries again —
// whether it had none at all or none that fits this workplace. Serialised per
// organisation: two simultaneous wakes must not each start one.
func (p *Pool) ensureAndPick(ctx context.Context, want need) (*conn, error) {
	p.mu.Lock()
	lock := p.ensuring[want.orgID]
	if lock == nil {
		lock = &sync.Mutex{}
		p.ensuring[want.orgID] = lock
	}
	p.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	// Whoever waited on the lock may find the runner already there.
	if c, err := p.pick(want); err == nil {
		return c, nil
	}
	if err := p.EnsureLocal(ctx, want.orgID); err != nil {
		return nil, err
	}
	return p.pick(want)
}

// hasAll: every tag the agent asks for has to be on the runner. The other
// direction does not hold — a runner may carry more than is asked of it.
func hasAll(has, wanted []string) bool {
	for _, w := range wanted {
		found := false
		for _, h := range has {
			if strings.EqualFold(strings.TrimSpace(h), strings.TrimSpace(w)) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// holdsImage: a runner that names no images makes no claim and is therefore
// not excluded on that ground. That is the built-in runner's case — the
// control plane can look at its images itself, and a claim it would have to
// keep up to date would only be one more thing that can be wrong.
func holdsImage(has []string, wanted string) bool {
	if len(has) == 0 || wanted == "" {
		return true
	}
	for _, h := range has {
		if strings.TrimSpace(h) == wanted {
			return true
		}
	}
	return false
}

// imageFor resolves an agent's workplace. One rule, in this order: nothing
// named → the DEFAULT PROFILE (`base`), resolved like any other; a known
// profile → its image; anything else → taken literally.
//
// There used to be a second source for the empty case, a DefaultImage filled at
// process start — before the catalogue has ever been fetched. While the
// instance pinned its images through the environment the two agreed; the moment
// covey.work took its workplaces from the catalogue they parted, and every
// agent without a named workplace resolved to the compiled
// `covey-sandbox:latest`, which exists on no host. The self-check said so
// within a minute:
//
//	sandbox image "covey-sandbox:latest" is missing — build it once …
//
// The field is gone. "Nothing named" is a profile name, and `base` resolves
// like every other: environment over catalogue over compiled default
// (sandbox.Resolve) — one path, one answer.
func (p *Pool) imageFor(ctx context.Context, orgID uuid.UUID, want string) string {
	want = strings.TrimSpace(want)
	if want == "" {
		want = sandbox.DefaultName()
	}
	if img, ok := p.profiles(ctx)[want]; ok && img != "" {
		return img
	}
	// Ein Arbeitsplatz dieser Organisation. Nach dem Katalog gefragt, weil ein
	// veroeffentlichter Name nicht ueberschrieben werden kann — welcher
	// gemeint ist, darf nicht davon abhaengen, wer zuerst nachsieht.
	if orgID != uuid.Nil && p.OrgImages != nil {
		if img, ok := p.OrgImages(ctx, orgID)[want]; ok && img != "" {
			return img
		}
	}
	// Instanzweit gefragt (Bereitschaftspruefung): dann ohne Mandant, aber die
	// Namen sind dieselben.
	if orgID == uuid.Nil && p.AllOrgImages != nil {
		if img, ok := p.AllOrgImages(ctx)[want]; ok && img != "" {
			return img
		}
	}
	return want
}

// profiles is what the profile names resolve to on this installation:
// environment over catalogue over compiled default (sandbox.Resolve).
//
// Recomputed at most once a minute — the catalogue underneath keeps its own,
// much longer cache, so this is a cheap map assembly and not a request.
func (p *Pool) profiles(ctx context.Context) map[string]string {
	if p.Catalog == nil && p.EnvImages == nil {
		// Nothing to layer: whatever was wired in stands. This is also what
		// the tests build, and they should not need a catalogue.
		return p.Profiles
	}
	p.profMu.RLock()
	m, at := p.profMap, p.profAt
	p.profMu.RUnlock()
	if m != nil && time.Since(at) < time.Minute {
		return m
	}
	var fromCatalog map[string]string
	if p.Catalog != nil {
		fromCatalog = p.Catalog.Images(ctx)
	}
	eff := sandbox.Resolve(p.EnvImages, fromCatalog)
	p.profMu.Lock()
	p.profMap, p.profAt = eff, time.Now()
	p.profMu.Unlock()
	return eff
}

// startTimeout is the bound for one start: what the instance set, otherwise the
// default. One place, so the two cannot drift.
func (p *Pool) startTimeout() time.Duration {
	if p.StartTimeout > 0 {
		return p.StartTimeout
	}
	return defaultStartTimeout
}

// need describes what a sandbox requires of its runner.
type need struct {
	orgID uuid.UUID
	image string
	tags  []string
	// prefer is the host this agent last worked on. Not a requirement: a home
	// that is already lying there is worth a lot (materialising a 7 GB working
	// copy from the store costs minutes, pulling an image costs seconds), but
	// not worth waiting for a machine that is gone.
	prefer uuid.UUID
}

// candidates ranks the runners that may carry this sandbox. What decides is
// exactly one thing — the TAGS — and everything else is order:
//
//  1. the host the agent last worked on, while it is connected. Its working
//     copy of the home is there; every other host materialises it again.
//  2. a host that already holds the image. Cheaper, not required: docker run
//     fetches what is missing, and a claim that excluded a host instead cost
//     covey.work its data plane for an afternoon — the registered runner
//     claimed covey-sandbox:latest, the agents needed the deploy image, and
//     nobody was a candidate.
//  3. the fewest running sandboxes, and the runner id so that the choice does
//     not depend on map order.
//
// A start that then fails on the chosen host — no credentials for a private
// registry, a full disk — moves to the next in this list (see Start). That is
// what makes the image an ordering criterion rather than a wall.
func (p *Pool) candidates(want need) ([]*conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// tagged is what the tags let through, out what also still answers. Both,
	// because the two say different things and each deserves its own sentence
	// when nothing is left.
	var inOrg, tagged, out []*conn
	paused, full := 0, 0
	for _, c := range p.conns {
		// The organisation comes first and is not a filter among others: a
		// runner of a foreign tenant is not a worse candidate, it is none.
		if c.orgID != want.orgID {
			continue
		}
		inOrg = append(inOrg, c)
		// A tag says what a host IS — arm64, gpu, inside the target system's
		// network. That is the one thing no other machine can stand in for,
		// and therefore the only thing that excludes.
		if !hasAll(c.tags, want.tags) {
			continue
		}
		tagged = append(tagged, c)
		// A paused host is out of service on purpose. Named separately from
		// everything else that can exclude one, because it is the only reason
		// somebody CHOSE — and the answer to "why is nothing running" then has
		// to be the choice and not a guess about the network.
		if c.paused() {
			paused++
			continue
		}
		// And a host that says nothing gets nothing. See answering: connected
		// is not the same as reachable, and the difference is a start that
		// waits out its whole timeout on a machine that never read it.
		if !c.answering() {
			continue
		}
		// A host at its own limit is not a worse candidate, it is none — the
		// runner would refuse the start anyway, and choosing it would cost a
		// round trip and a failed wake to learn what the counter already knew.
		if c.full() {
			full++
			continue
		}
		out = append(out, c)
	}
	switch {
	case len(inOrg) == 0:
		return nil, errNoneInOrg
	case len(tagged) == 0:
		return nil, fmt.Errorf("%w: no runner of this organisation carries the tags %v",
			ErrNoRunner, want.tags)
	case len(out) == 0 && paused == len(tagged):
		return nil, fmt.Errorf("%w: every runner of this organisation is paused", ErrNoRunner)
	case len(out) == 0 && full == len(tagged):
		// Its own sentence, because it is the one state that passes: a full
		// host is working, and the wake belongs in the queue rather than in an
		// error somebody goes looking for a network fault behind.
		return nil, fmt.Errorf("%w: every runner of this organisation is at its sandbox limit", ErrNoRunner)
	case len(out) == 0:
		return nil, fmt.Errorf("%w: no runner of this organisation is available — %d paused, %d at their sandbox limit, %d connected but not answering",
			ErrNoRunner, paused, full, len(tagged)-paused-full)
	}
	rank := func(c *conn) (int, int) {
		c.mu.Lock()
		running := c.sandboxes
		c.mu.Unlock()
		score := 0
		// The built-in runner comes last of all. It no longer stands down when
		// an organisation registers a host of its own — that rule cost more
		// than it was worth — but the intention behind it was right: whoever
		// adds a machine wants the compute THERE, and a pool where half the
		// agents quietly run on the control plane is the surprise the old rule
		// was trying to prevent. As a last resort it keeps the intention
		// without the failure mode: while a registered host can take the work,
		// it does; when none can, the organisation still runs.
		if c.builtin {
			score += 10
		}
		if want.prefer != uuid.Nil && c.runnerID == want.prefer {
			score -= 2
		}
		if len(c.images) > 0 && holdsImage(c.images, want.image) {
			score--
		}
		return score, running
	}
	sort.Slice(out, func(i, j int) bool {
		si, ri := rank(out[i])
		sj, rj := rank(out[j])
		if si != sj {
			return si < sj
		}
		if ri != rj {
			return ri < rj
		}
		return out[i].runnerID.String() < out[j].runnerID.String()
	})
	return out, nil
}

// pick is the first candidate — the one the scheduler would take.
func (p *Pool) pick(want need) (*conn, error) {
	out, err := p.candidates(want)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// Start satisfies orchestrator.SandboxProvider.
func (p *Pool) Start(ctx context.Context, spec orchestrator.SandboxSpec) (orchestrator.Sandbox, error) {
	// Whatever was reported while starting stops being current the moment this
	// returns — either the sandbox is up, or the start failed. Both end the
	// wait the display was about.
	defer p.Phases.Clear(spec.AgentID)
	// Whatever the file browser wrote goes into the store BEFORE the home is
	// materialised over it. Otherwise a snapshot from before the upload would
	// win, and the file would be gone without anyone having deleted it.
	p.flushHome(ctx, spec.AgentID)

	want := need{orgID: spec.OrgID, image: p.imageFor(ctx, spec.OrgID, spec.Image), tags: spec.RunnerTags}
	if p.LastRunner != nil {
		if last, ok := p.LastRunner(ctx, spec.AgentID); ok {
			want.prefer = last
		}
	}
	candidates, err := p.candidates(want)
	// A pick fails for more reasons than "this organisation has no runner".
	// A registered remote runner that does not hold this image leaves the
	// organisation with a runner and without a candidate — and the built-in
	// one, which claims no image and is therefore always a candidate, would
	// never be started again. Measured on covey.work: one remote runner
	// joined, the control plane restarted, and from then on every wake failed
	// with "no runner holds the image" every 30 seconds while the machine that
	// could have run it stood idle.
	//
	// The fallback smuggles nothing past a requirement: pick runs again after
	// it, so tags still exclude the built-in runner where they matter.
	if err != nil && p.EnsureLocal != nil {
		if !errors.Is(err, errNoneInOrg) {
			// Worth a sentence: from here on this organisation runs sandboxes
			// on the control plane again, and whoever moved the data plane to
			// another host should read why rather than notice it in a load
			// graph.
			p.Log.Warn("no connected runner fits — the built-in one takes it",
				"org", want.orgID, "image", want.image, "tags", want.tags, "reason", err)
		}
		// The reason no registered host took it is the interesting half, and
		// the fallback used to overwrite it: whoever read "the built-in runner
		// is paused" learned nothing about the machine they had actually put
		// the work on. Measured on covey.work, and it cost an hour of looking
		// in the wrong place — the real reason was that no runner was connected
		// in that second, because the control plane had just restarted.
		why := err
		var c *conn
		if c, err = p.ensureAndPick(ctx, want); err == nil {
			candidates = []*conn{c}
		} else {
			err = fmt.Errorf("%w — and the built-in runner did not step in: %v", why, err)
		}
	}
	if err != nil {
		return nil, err
	}
	timeout := p.startTimeout()
	// Which state the home is brought to. A failure here is not fatal: the
	// working copy on the runner is then what applies — slower or older, but
	// an agent that cannot start at all because a lookup failed would be the
	// worse outcome.
	snapshot := ""
	var fallbacks []string
	if p.SnapshotChain != nil {
		// The whole chain in one question: which of them can be read is only
		// found out where the blocks are fetched, so the runner gets the list
		// and walks it (#138).
		if chain, err := p.SnapshotChain(ctx, spec.AgentID); err != nil {
			p.Log.Warn("snapshots not readable — the working copy applies",
				"agent", spec.AgentID, "err", err)
		} else if len(chain) > 0 {
			snapshot, fallbacks = chain[0], chain[1:]
		}
	} else if p.LatestSnapshot != nil {
		if hash, err := p.LatestSnapshot(ctx, spec.AgentID); err != nil {
			p.Log.Warn("last snapshot not readable — the working copy applies",
				"agent", spec.AgentID, "err", err)
		} else {
			snapshot = hash
		}
	}

	// A start can fail at the runner itself: no credentials for a private
	// registry, a full disk, a Docker that has just died. That is a reason to
	// ask the next host of this organisation, not to lose the sandbox — the
	// agent does not care which machine it wakes on.
	var last error
	for i, c := range candidates {
		answer, err := c.askStart(ctx, StartSandbox{
			AgentID:     spec.AgentID,
			OrgID:       spec.OrgID,
			Image:       want.image,
			HomeDir:     spec.HomeDir,
			Env:         spec.Env,
			EgressToken: spec.EgressToken,
			Snapshot:    snapshot,
			Fallbacks:   fallbacks,
			ImageHint:   p.imageHints(ctx)[want.image],
			Services:    spec.Services,
		}, timeout)
		switch {
		case err != nil:
			last = err
		default:
			res, decodeErr := decode[SandboxResult](answer)
			switch {
			case decodeErr != nil:
				last = decodeErr
			case res.Err != "":
				last = errors.New(res.Err)
			default:
				c.mu.Lock()
				c.sandboxes++
				c.mu.Unlock()
				return &poolSandbox{
					pool: p, conn: c, agentID: spec.AgentID, orgID: spec.OrgID,
					services: res.Services,
				}, nil
			}
		}
		if i+1 < len(candidates) {
			p.Log.Warn("runner could not start the sandbox — asking the next one",
				"runner", short(c.runnerID), "image", want.image, "err", last)
		}
	}
	return nil, last
}

// Runner satisfies orchestrator.Placed: which host this sandbox runs on.
func (s *poolSandbox) Runner() (uuid.UUID, string) {
	label := ""
	if s.pool.RunnerLabel != nil {
		label = s.pool.RunnerLabel(context.Background(), s.conn.runnerID)
	}
	if label == "" {
		if s.conn.builtin {
			label = "built-in"
		} else {
			label = short(s.conn.runnerID)
		}
	}
	return s.conn.runnerID, label
}

type poolSandbox struct {
	pool    *Pool
	conn    *conn
	agentID uuid.UUID
	orgID   uuid.UUID
	once    sync.Once
	// services is what the host reported it brought up, with the image each
	// one actually started from.
	services []sandbox.ServiceRun
}

// Services satisfies orchestrator.WithServices: what stands beside this
// sandbox. The control plane cannot work this out for itself — only the host
// that ran `docker run` knows which image the reference resolved to.
func (s *poolSandbox) Services() []sandbox.ServiceRun {
	// Under the lock, because StartServices appends to this while the sandbox
	// runs: the agent asking for its project's database is a second writer to
	// a slice the orchestrator reads whenever it records a job.
	s.pool.mu.Lock()
	defer s.pool.mu.Unlock()
	return slices.Clone(s.services)
}

// StartServices satisfies orchestrator.ServiceStarter: bring services up
// beside this sandbox while it runs.
//
// It goes to the host this sandbox is ON, not to any host of the organisation —
// a service is only useful on the network its sandbox hangs in, and the
// connection is already here. Whether they MAY run has been decided before this
// call: the allowlist is the organisation's question, and a runner would have
// to be a database client to ask it.
func (s *poolSandbox) StartServices(ctx context.Context, services []sandbox.Service) ([]sandbox.ServiceRun, error) {
	answer, err := s.conn.ask(ctx, TypeAddServices, AddServices{
		AgentID: s.agentID, Services: services,
	}, s.pool.startTimeout())
	if err != nil {
		return nil, err
	}
	res, err := decode[SandboxResult](answer)
	if err != nil {
		return nil, err
	}
	if res.Err != "" {
		return nil, errors.New(res.Err)
	}
	// What the agent gets told is what the HOST reported, not what was asked
	// for. The two differ exactly when something went wrong quietly, and that
	// is the case worth not papering over.
	s.pool.mu.Lock()
	s.services = append(s.services, res.Services...)
	s.pool.mu.Unlock()
	return res.Services, nil
}

// SyncHome writes the home into the store while the compute keeps running —
// satisfies orchestrator.HomeSyncer, and it is how a warm-parked sandbox gets
// its job into the store at all (see Stop below).
//
// The caller decides WHEN: the orchestrator syncs a parked sandbox, i.e. one
// nobody is writing into at that moment. The one thing that cannot be ruled
// out is the next wake arriving mid-scan; the snapshot then describes a home
// in motion. That is the same window the manual backup has always had, and it
// is the cheaper end of the trade — the alternative is a job that lives
// nowhere but in a container volume.
func (s *poolSandbox) SyncHome(ctx context.Context) error {
	// Whatever the file browser has changed and not yet carried out goes in
	// with it — two syncs in a row would only produce two snapshots of the
	// same state.
	s.pool.flushDirtyFlag(s.agentID)
	return s.pool.syncHomeReason(ctx, s.conn, s.agentID, s.orgID, "job")
}

// Discard takes the compute down and leaves the store alone — satisfies
// orchestrator.Discardable.
//
// For the start that never became a run: the home is byte for byte what was
// materialised into it, so the sync would scan gigabytes to produce the
// snapshot that is already there. The `once` is shared with Stop, so whichever
// of the two the caller reaches for, the sandbox goes down exactly once.
func (s *poolSandbox) Discard(ctx context.Context) error {
	var err error
	s.once.Do(func() {
		s.conn.mu.Lock()
		if s.conn.sandboxes > 0 {
			s.conn.sandboxes--
		}
		s.conn.mu.Unlock()
		_, err = s.conn.ask(context.WithoutCancel(ctx), TypeStopSandbox,
			StopSandbox{AgentID: s.agentID}, 60*time.Second)
	})
	return err
}

// Stop shuts the compute down and writes the home into the store. In that
// order: the scan runs on a home nothing is writing into any more.
//
// This is the real falling-asleep. A warm-parked sandbox does not get here —
// it goes through SyncHome above instead, on the parked sandbox and off the
// sleep path, so an agent that wakes every few minutes does not pay for a full
// scan every time it nods off.
func (s *poolSandbox) Stop(ctx context.Context) error {
	var err error
	s.once.Do(func() {
		s.conn.mu.Lock()
		if s.conn.sandboxes > 0 {
			s.conn.sandboxes--
		}
		s.conn.mu.Unlock()
		// Without cancel: the stop has to go through even when the run that
		// held this sandbox has just been cancelled — otherwise the container
		// stays behind and the name is taken at the next wake. The same for
		// the sync: a cancelled run is exactly when the work in the home is
		// worth keeping.
		ctx = context.WithoutCancel(ctx)
		_, err = s.conn.ask(ctx, TypeStopSandbox, StopSandbox{AgentID: s.agentID}, 60*time.Second)
		s.pool.syncHome(ctx, s.conn, s.agentID, s.orgID)
	})
	return err
}

// syncHome writes the home into the store after the job. A failure is logged
// and not passed on: the run is over, the sandbox is down, and there is
// nothing left for the caller to do about it. What it costs is the next start
// on another runner — which is time, not data loss, as long as the working
// copy is still there. And precisely because it might not be, no prune may
// follow a failed sync.
func (p *Pool) syncHome(ctx context.Context, c *conn, agentID, orgID uuid.UUID) {
	_ = p.syncHomeReason(ctx, c, agentID, orgID, "job")
}

// syncHomeReason is the same with a reason for the snapshot — what triggered
// it: the end of a job, the file browser, a maintenance window.
func (p *Pool) syncHomeReason(ctx context.Context, c *conn, agentID, orgID uuid.UUID, reason string) error {
	if p.SnapshotTaken == nil {
		return nil
	}
	// Same as with a start: when this returns, the writing back is over —
	// finished, failed, or the host gone.
	defer p.Phases.Clear(agentID)
	started := time.Now()
	answer, err := c.ask(ctx, TypeSyncHome, SyncHome{
		AgentID: agentID, OrgID: orgID, Excludes: p.HomeExcludes,
	}, 30*time.Minute) // the first sync of a grown home is a full pass
	if err != nil {
		p.Log.Warn("home not synced", "agent", agentID, "err", err)
		p.saySyncFailed(ctx, agentID, c.runnerID, reason, err.Error())
		return err
	}
	res, err := decode[HomeSynced](answer)
	if err != nil || res.Err != "" {
		grund := firstNonEmpty(errString(err), res.Err)
		p.Log.Warn("home not synced", "agent", agentID, "err", grund)
		p.saySyncFailed(ctx, agentID, c.runnerID, reason, grund)
		return errors.New(grund)
	}
	res.DurationMS = int(time.Since(started).Milliseconds())
	res.Reason = reason
	if err := p.SnapshotTaken(ctx, agentID, c.runnerID, res); err != nil {
		p.Log.Warn("snapshot not recorded", "agent", agentID, "err", err)
		return err
	}
	return nil
}

// saySyncFailed meldet einen Sync, der nicht stattgefunden hat — dorthin, wo
// ein Mensch hinsieht. Das Log allein hat wochenlang niemanden erreicht.
func (p *Pool) saySyncFailed(ctx context.Context, agentID, runnerID uuid.UUID, reason, msg string) {
	if p.SnapshotFailed == nil {
		return
	}
	p.SnapshotFailed(context.WithoutCancel(ctx), agentID, runnerID, reason, msg)
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// AgentFiles satisfies orchestrator.FileAccess: it opens an agent's home
// wherever it happens to lie.
//
// Three places, one interface. The runner that holds the working copy is the
// live truth and the only one that can be written to. When it is not
// connected, the last snapshot is still readable — read-only, because writing
// into a snapshot would produce a state nobody can reconcile with the working
// copy that is coming back. And when there is neither, the honest answer is
// that this provider has no reachable home rather than an empty listing that
// reads like an empty home.
func (p *Pool) AgentFiles(agentID uuid.UUID) (sandboxfs.Tree, error) {
	ctx := context.Background()
	if p.HomeInfo == nil {
		return nil, orchestrator.ErrNoFileAccess
	}
	orgID, lastRunner, snapshot, err := p.HomeInfo(ctx, agentID)
	if err != nil {
		return nil, err
	}

	if c := p.connFor(orgID, lastRunner); c != nil {
		return &remoteTree{pool: p, conn: c, agentID: agentID, orgID: orgID}, nil
	}
	if snapshot != "" && p.Blobs != nil {
		m, err := homestore.Load(ctx, p.Blobs, orgID, snapshot)
		if err != nil {
			return nil, err
		}
		return newSnapshotTree(p.Blobs, orgID, m), nil
	}
	return nil, orchestrator.ErrNoFileAccess
}

// connFor prefers the runner the home last lay on — its working copy is the
// one the snapshot was taken from. Any other runner of the organisation would
// answer about a home it may not even have.
func (p *Pool) connFor(orgID, preferred uuid.UUID) *conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	if c := p.conns[preferred]; c != nil && c.orgID == orgID {
		return c
	}
	// Without a preference (an agent that has never run) any runner of the
	// organisation will do: they all answer about the same, empty home.
	if preferred == uuid.Nil {
		for _, c := range p.conns {
			if c.orgID == orgID {
				return c
			}
		}
	}
	return nil
}

// Check satisfies orchestrator.DataPlaneChecker: it asks every connected
// runner what stands between it and a running sandbox. The question travels
// with the images the agents are actually configured for — a runner cannot
// know that, and asking about every configured profile instead would warn
// every fresh installation about a dev image nobody wants.
func (p *Pool) Check(ctx context.Context) []string {
	p.mu.Lock()
	conns := make([]*conn, 0, len(p.conns))
	for _, c := range p.conns {
		conns = append(conns, c)
	}
	p.mu.Unlock()

	if len(conns) == 0 {
		return []string{"no runner connected — no agent can be woken. " +
			"The built-in runner comes up with the control plane; if it is switched off, " +
			"this organisation needs a registered one."}
	}

	images := p.wantedImages(ctx)
	hints := p.imageHints(ctx)
	// All hosts at once. Serially, one host that does not answer held up the
	// answer for all the others — and the caller behind this is a view that is
	// polled, so its wait was everybody's wait. The order does not suffer: the
	// lines are sorted afterwards anyway.
	found := make([][]string, len(conns))
	var wg sync.WaitGroup
	for i, c := range conns {
		wg.Add(1)
		go func(i int, c *conn) {
			defer wg.Done()
			answer, err := c.ask(ctx, TypeCheck, Check{Images: images, Hints: hints}, 30*time.Second)
			if err != nil {
				found[i] = []string{fmt.Sprintf("runner %s does not answer: %v", short(c.runnerID), err)}
				return
			}
			res, err := decode[CheckResult](answer)
			if err != nil {
				found[i] = []string{fmt.Sprintf("runner %s: %v", short(c.runnerID), err)}
				return
			}
			for _, problem := range res.Problems {
				// Only name the runner once there is more than one — on the
				// normal installation the prefix would be noise in every line.
				if len(conns) > 1 {
					problem = "runner " + short(c.runnerID) + ": " + problem
				}
				found[i] = append(found[i], problem)
			}
		}(i, c)
	}
	wg.Wait()
	var problems []string
	for _, f := range found {
		problems = append(problems, f...)
	}
	sort.Strings(problems)
	return problems
}

// wantedImages resolves the agents' workplaces to images.
func (p *Pool) wantedImages(ctx context.Context) []string {
	if p.AgentImages == nil {
		return nil
	}
	inUse, err := p.AgentImages(ctx)
	if err != nil || len(inUse) == 0 {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for want := range inUse {
		image := p.imageFor(ctx, uuid.Nil, want)
		if image == "" || seen[image] {
			continue
		}
		seen[image] = true
		out = append(out, image)
	}
	sort.Strings(out)
	return out
}

// imageHints maps image → how one obtains it. It belongs on this side: the
// runner sees a reference, the catalogue and the instance's overrides are
// known here (spec/16).
func (p *Pool) imageHints(ctx context.Context) map[string]string {
	out := map[string]string{}
	for name, image := range p.profiles(ctx) {
		if image == "" {
			continue
		}
		if prof, ok := sandbox.Get(name); ok && prof.Build != "" {
			out[image] = prof.Build
		}
	}
	return out
}

// Workplaces answers, for the profiles of the catalogue, whether their image
// lies ready on a runner of this organisation — the list the interface offers
// as workplaces.
//
// Presence is a question to the runner and not to the control plane: the image
// lies where the sandbox starts. With several runners "there" means: on at
// least one that could take the agent — anything else would call a workplace
// unavailable that works.
func (p *Pool) Workplaces(ctx context.Context, orgID uuid.UUID) map[string]bool {
	var report []string
	for _, prof := range sandbox.All() {
		report = append(report, p.imageFor(ctx, orgID, prof.Name))
	}
	return p.WorkplaceImages(ctx, orgID, report)
}

// WorkplaceImages asks about exactly these images. The caller decides which —
// since an organisation may bring workplaces of its own (spec/16), the list is
// no longer derivable from the compiled catalogue alone.
func (p *Pool) WorkplaceImages(ctx context.Context, orgID uuid.UUID, images []string) map[string]bool {
	seen := map[string]bool{}
	var report []string
	for _, image := range images {
		if strings.TrimSpace(image) == "" || seen[image] {
			continue
		}
		seen[image] = true
		report = append(report, image)
	}
	sort.Strings(report)
	p.mu.Lock()
	var conns []*conn
	for _, c := range p.conns {
		if c.orgID == orgID {
			conns = append(conns, c)
		}
	}
	p.mu.Unlock()

	out := map[string]bool{}
	for _, c := range conns {
		answer, err := c.ask(ctx, TypeCheck, Check{Report: report}, 20*time.Second)
		if err != nil {
			continue
		}
		res, err := decode[CheckResult](answer)
		if err != nil {
			continue
		}
		for image, ok := range res.Present {
			out[image] = out[image] || ok
		}
	}
	return out
}

// PullOn fetches one image onto ONE host — the deliberate version of what the
// first wake there would do anyway, for the case the wake must not be the
// moment it is discovered that the pull does not work: a private registry
// without credentials answers in seconds here and in the middle of a run
// otherwise.
//
// nameOrImage is a workplace name or a reference; both resolve the same way an
// agent's workplace does.
func (p *Pool) PullOn(ctx context.Context, orgID, runnerID uuid.UUID, nameOrImage string) (string, error) {
	image := p.imageFor(ctx, orgID, nameOrImage)
	if strings.TrimSpace(image) == "" {
		return "", fmt.Errorf("no image for %q", nameOrImage)
	}
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil || c.orgID != orgID {
		return image, fmt.Errorf("%w: this runner is not connected", ErrNoRunner)
	}
	answer, err := c.ask(ctx, TypePullImage, PullImage{Image: image}, 30*time.Minute)
	if err != nil {
		return image, err
	}
	res, err := decode[PullResult](answer)
	if err != nil {
		return image, err
	}
	if res.Err != "" {
		return image, errors.New(res.Err)
	}
	return image, nil
}

// SetLogLevel asks one runner to report at this level from now on. It waits
// for the confirmation and answers with the level the runner APPLIED — a
// refused value must show up in the interface rather than look like it took.
//
// The caller stores it beside the runner as well: a runner that reconnects
// otherwise comes back quietly at info while the switch still reads "debug",
// and a switch that lies about the state is worse than no switch.
func (p *Pool) SetLogLevel(ctx context.Context, orgID, runnerID uuid.UUID, level string) (string, error) {
	if !ValidLogLevel(level) {
		return "", fmt.Errorf("unknown log level %q (debug or info)", level)
	}
	p.mu.Lock()
	c := p.conns[runnerID]
	p.mu.Unlock()
	if c == nil || c.orgID != orgID {
		return "", fmt.Errorf("%w: this runner is not connected", ErrNoRunner)
	}
	answer, err := c.ask(ctx, TypeSetLogLevel, SetLogLevel{Level: level}, 15*time.Second)
	if err != nil {
		return "", err
	}
	res, err := decode[SetLogLevel](answer)
	if err != nil {
		return "", err
	}
	return res.Level, nil
}

// PullWorkplace fetches a profile's image onto every runner of the
// organisation — the deliberate version of what the first wake would do anyway.
//
// It waits: a caller who asks for this wants to know whether it worked, and an
// answer that only says "started" would push the actual outcome into a place
// where nobody looks. The timeout is generous for the same reason a sandbox
// image is generous in size.
//
// The result names every runner that failed. Partial success is a real state
// with several runners and must not be rounded to "done" — an agent scheduled
// onto the one that failed would wait for a download nobody expects.
func (p *Pool) PullWorkplace(ctx context.Context, orgID uuid.UUID, profile string) (image string, problems []string, err error) {
	image = p.imageFor(ctx, orgID, profile)
	if strings.TrimSpace(image) == "" {
		return "", nil, fmt.Errorf("no image for workplace %q", profile)
	}

	p.mu.Lock()
	var conns []*conn
	for _, c := range p.conns {
		if c.orgID == orgID {
			conns = append(conns, c)
		}
	}
	p.mu.Unlock()
	if len(conns) == 0 {
		return image, nil, errNoneInOrg
	}

	for _, c := range conns {
		answer, err := c.ask(ctx, TypePullImage, PullImage{Image: image}, 30*time.Minute)
		if err != nil {
			problems = append(problems, fmt.Sprintf("runner %s: %v", short(c.runnerID), err))
			continue
		}
		res, err := decode[PullResult](answer)
		if err != nil {
			problems = append(problems, fmt.Sprintf("runner %s: %v", short(c.runnerID), err))
			continue
		}
		if res.Err != "" {
			problems = append(problems, fmt.Sprintf("runner %s: %s", short(c.runnerID), res.Err))
		}
	}
	return image, problems, nil
}

// StopStray stops a sandbox the control plane no longer holds a handle to —
// satisfies orchestrator.StrayStopper.
//
// The handle hangs off the session, and the session is what got lost: a run
// that hung and was given up on, or a restart of the control plane, whose
// sessions live in memory while the container lives on the host. The container
// then belongs to nobody. It does not block the next wake (a start removes a
// leftover of the same name first), and it does hold memory and disk — and it
// makes the host refuse its own update, because a runner carrying sandboxes
// must not replace the binary that watches them.
//
// Asked of the host the home last lay on. Any other runner of the organisation
// would be asked about a container it never started; that is not an error
// there, just a wasted question.
func (p *Pool) StopStray(ctx context.Context, agentID, orgID uuid.UUID) error {
	var last uuid.UUID
	if p.LastRunner != nil {
		if id, ok := p.LastRunner(ctx, agentID); ok {
			last = id
		}
	}
	c := p.connFor(orgID, last)
	if c == nil {
		// Nobody to ask. The container — if there is one — is on a host that
		// is not connected, and its own watcher will report the death when it
		// comes back.
		return nil
	}
	answer, err := c.ask(ctx, TypeStopSandbox, StopSandbox{AgentID: agentID}, 60*time.Second)
	if err != nil {
		return err
	}
	res, err := decode[SandboxResult](answer)
	if err != nil {
		return err
	}
	// "Was not there" is the normal case: usually nothing is left over, and
	// asking is cheaper than finding out afterwards that something was.
	if res.Err != "" {
		p.Log.Debug("stopping a stray sandbox", "agent", agentID, "runner", c.runnerID, "answer", res.Err)
	}
	p.Phases.Clear(agentID)
	return nil
}
