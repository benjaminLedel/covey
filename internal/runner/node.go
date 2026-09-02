package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"covey/internal/buildinfo"
	"covey/internal/homestore"
	"covey/internal/sandbox"
)

// Node is the runner side of the protocol: it starts and stops sandboxes and
// reports what becomes of them. The same implementation serves the built-in
// runner and the one on a foreign host — what differs is the Transport it is
// handed.
type Node struct {
	RunnerID uuid.UUID
	OrgID    uuid.UUID
	Docker   *Docker
	Log      *slog.Logger
	Tags     []string
	// Images this host holds. On a runner the image is a statement of
	// capacity — it gets only agents whose workplace it can provide. Empty =
	// it makes no claim and is not excluded on that ground.
	Images []string
	// MaxSandboxes caps how many sandboxes run here at once, 0 = no limit.
	// Set from the runner's own configuration file: how much this machine can
	// carry is a fact about the machine, and the control plane has no way of
	// knowing it.
	MaxSandboxes int
	// Blobs is the home store. The built-in runner reaches it directly — it
	// sits in the process that owns it; that is a transport detail, and the
	// sync logic above does not know the difference. nil = no store, and the
	// home stays what lies in the working copy.
	Blobs homestore.BlobStore

	mu      sync.Mutex
	running map[uuid.UUID]*sandboxProc
	// closed marks the node as finished. Without it Close would only EMPTY the
	// map, and a start already on its way would enter its sandbox behind it —
	// with a watcher nothing cancels any more, which is exactly the orphaned
	// `docker wait` Close exists to prevent. The production callers cannot hit
	// that window (Run returns synchronously before its defer fires), the
	// tests can: t.Cleanup(node.Close) runs while node.Run is still live in its
	// goroutine.
	closed bool
	// watchers counts the sandbox watchers this node has started. Cancelling
	// one is a signal, not a join: `docker wait` dies when its context ends,
	// but the goroutine around it is still on its way past that context and
	// still holds a container, a working copy and a temp directory. Close used
	// to return in between, and whoever cleaned up afterwards cleaned up under
	// running work — in a test that was a directory removed while the docker
	// double was still writing into it (#114), in production it is a teardown
	// cut off by the process exiting. Same defect one layer up: #98.
	watchers sync.WaitGroup
	// turn holds, per agent, the end of the line: the channel the message
	// currently being worked on closes when it is done. See inOrder.
	turn map[uuid.UUID]chan struct{}
	// streams are the chunked answers under way, by correlation id, for the
	// credits that let them continue (see pump).
	streams map[string]*stream
	// adopted marks that this process has looked for the containers a
	// predecessor left running — once, at the first connection.
	adopted sync.Once
	// updateMu serialises updateSelf: two orders arriving at once must not
	// both write the new binary and rename over each other (#161). updating is
	// the flag the rest of the node reads: a start that arrives while the
	// binary is being fetched is refused rather than accepted into a process
	// that is about to exec.
	updateMu sync.Mutex
	updating bool
	// ReadSilence is how long this node tolerates hearing NOTHING from the
	// control plane before it closes the connection and lets RunNode dial
	// again. The control plane asks every connected host for its capacity once
	// per beat, so silence for three beats is a dead link, not a quiet one —
	// and without this the runner learned of a NAT-dropped connection only
	// when the kernel gave up on its own writes, a quarter of an hour later,
	// offline for the pool the whole time (#158). 0 = off, which is what the
	// built-in runner runs with: its link is a channel pair, and closing it
	// would end the one connection it gets.
	ReadSilence time.Duration
	// Restart replaces this process with the binary that now lies at its path.
	// A field so that a test can watch instead of disappearing; nil = execSelf,
	// which is what a runner does.
	Restart func() error
	// executable answers where this process's binary lies — the file an update
	// replaces. A seam of the same kind as Restart: a test must be able to say
	// which file, or it would overwrite the one running the test.
	executable func() (string, error)
	// logs is the buffer between what this runner writes and what the control
	// plane gets to read. See logship.go.
	logs *logRing

	// link is the connection this node speaks over RIGHT NOW. Everything that
	// outlives a connection — a sandbox watcher, a start or sync that was under
	// way when the link dropped — answers over this rather than over the
	// transport it was handed at the time (#154). Before, every such answer went
	// to the socket of the connection that had asked, which after a reconnect is
	// a closed one: a crash was "reported" into the void and the control plane
	// went back to inferring it from the ReadyTimeout.
	linkMu sync.Mutex
	link   Transport
	// outbox holds the messages that could not be sent and must not be lost —
	// a sandbox's death, a completed sync. They go out first on the next
	// connection. Bounded: a host that is offline for a day does not need to
	// deliver a day of progress lines, and those are not kept here anyway.
	outbox []Message
}

// outboxLimit bounds what a node keeps for the next connection. Exits and sync
// results are rare — a handful per sandbox per day — so the bound is generous
// against those and tight against anything that would leak.
const outboxLimit = 256

// sandboxProc is a running sandbox as this node sees it.
type sandboxProc struct {
	container string
	// stopping marks a shutdown somebody asked for. Without it, every ordinary
	// stop would be reported as an exit, and "the sandbox died" would become
	// the most frequent and least informative message the platform produces.
	stopping bool
	cancel   context.CancelFunc
}

func NewNode(runnerID, orgID uuid.UUID, docker *Docker, log *slog.Logger) *Node {
	if log == nil {
		log = slog.Default()
	}
	// Every line this node writes goes two ways from here on: to the host's
	// own stderr as before, and into the ring the control plane reads. Wrapping
	// in the constructor rather than at the call sites is what makes that true
	// for the whole package — a logger passed on with WithAttrs keeps shipping.
	ring := newLogRing(slog.LevelInfo)
	return &Node{
		RunnerID: runnerID,
		OrgID:    orgID,
		Docker:   docker,
		Log:      shippingLogger(log, ring),
		logs:     ring,
		running:  map[uuid.UUID]*sandboxProc{},
		turn:     map[uuid.UUID]chan struct{}{},
		streams:  map[string]*stream{},
	}
}

// Run speaks the protocol until the transport ends or ctx is cancelled. It
// registers first: the control plane may not assign anything to a runner whose
// identity and protocol version it does not know.
func (n *Node) Run(ctx context.Context, t Transport) error {
	// What a predecessor left running is taken under watch BEFORE the
	// handshake names it — the control plane reconciles against the list, and
	// a sandbox the list did not carry would be one nobody ever stops.
	n.adopt(ctx)
	hello, err := encode(TypeRegistered, "", Registered{
		RunnerID: n.RunnerID,
		OrgID:    n.OrgID,
		Protocol: Protocol,
		Version:  buildinfo.String(),
		Arch:     runtime.GOARCH,
		Tags:     n.Tags,
		Images:   n.Images,
		Features: []string{FeatureSelfUpdate, FeatureLogShipping, FeatureStreamCredit},
		Running:  n.runningAgents(),

		MaxSandboxes: n.MaxSandboxes,
	})
	if err != nil {
		return err
	}
	if err := t.Send(ctx, hello); err != nil {
		return err
	}
	// Said once, and only on success: whoever starts `covey-runner run` and
	// sees nothing cannot tell a working connection from a silent failure.
	n.Log.Info("connected to the control plane", "runner", n.RunnerID, "organisation", n.OrgID)
	n.setLink(t)
	// What the last connection could not deliver goes first. A sandbox that
	// died while the link was down is still a death the control plane has to
	// hear of, and a sync that finished into a dead socket is still the state
	// the next wake must build on (#153).
	n.flushOutbox(ctx, t)

	beat, stopBeat := context.WithCancel(ctx)
	defer stopBeat()
	go n.heartbeat(beat, t)
	var heard atomic.Int64
	heard.Store(time.Now().UnixNano())
	if n.ReadSilence > 0 {
		go n.readWatchdog(beat, t, &heard)
	}
	// The log goes up the same link. It shares the heartbeat's lifetime: what
	// this runner said is worth having exactly as long as there is somebody to
	// say it to.
	go n.shipLogs(beat, t)

	for {
		msg, err := t.Receive(ctx)
		if err != nil {
			if errors.Is(err, ErrTransportClosed) || errors.Is(err, context.Canceled) {
				return nil
			}
			return err
		}
		heard.Store(time.Now().UnixNano())
		n.handle(ctx, t, msg)
	}
}

// readWatchdog closes the transport when the control plane has said nothing
// for ReadSilence — the mirror of the pool's watchdog, for the same half-open
// link seen from the other end.
func (n *Node) readWatchdog(ctx context.Context, t Transport, heard *atomic.Int64) {
	ticker := time.NewTicker(n.ReadSilence / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			quiet := time.Since(time.Unix(0, heard.Load()))
			if quiet > n.ReadSilence {
				n.Log.Warn("nothing heard from the control plane — closing the connection to dial again",
					"silent_for", quiet.Round(time.Second))
				_ = t.Close()
				return
			}
		}
	}
}

// heartbeat reports in while the connection stands. Without it a half-open
// connection is invisible to the control plane: it would keep assigning
// sandboxes to a runner that no longer hears anything.
func (n *Node) heartbeat(ctx context.Context, t Transport) {
	ticker := time.NewTicker(HeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			msg, err := encode(TypeHeartbeat, "", nil)
			if err != nil {
				return
			}
			if err := t.Send(ctx, msg); err != nil {
				// The connection is gone; the read loop notices it too and ends
				// the run. Nothing to report twice.
				return
			}
		}
	}
}

func (n *Node) handle(ctx context.Context, t Transport, msg Message) {
	// One line per incoming message, at debug. It is the spine of the trace:
	// with it, "this host received nothing for twenty minutes" and "it received
	// a start and sat on it" are different pictures instead of the same silence.
	n.Log.Debug("runner: message received", "type", msg.Type, "id", msg.ID)
	switch msg.Type {
	case TypeStartSandbox:
		spec, err := decode[StartSandbox](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeSandboxFailed, SandboxResult{Err: err.Error()})
			return
		}
		n.inOrder(spec.AgentID, func() { n.start(ctx, t, msg.ID, spec) })
	case TypeStopSandbox:
		spec, err := decode[StopSandbox](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeSandboxStopped, SandboxResult{Err: err.Error()})
			return
		}
		n.inOrder(spec.AgentID, func() { n.stop(ctx, t, msg.ID, spec.AgentID) })
	case TypeSyncHome:
		req, err := decode[SyncHome](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeHomeSynced, HomeSynced{Err: err.Error()})
			return
		}
		n.inOrder(req.AgentID, func() { n.sync(ctx, t, msg.ID, req) })
	case TypeHomeOp:
		op, err := decode[HomeOp](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeHomeResult, HomeResult{Err: err.Error()})
			return
		}
		// In its own goroutine: a download of a few gigabytes must not stop the
		// runner from starting a sandbox in the meantime.
		go n.homeOp(context.WithoutCancel(ctx), t, msg.ID, op)
	case TypeAddServices:
		req, err := decode[AddServices](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeServicesAdded, SandboxResult{Err: err.Error()})
			return
		}
		// In order with the starts and stops of the same agent: bringing a
		// service up while its sandbox is being torn down would leave a
		// container that belongs to nothing.
		n.inOrder(req.AgentID, func() { n.addServices(ctx, t, msg.ID, req) })
	case TypeStreamCredit:
		credit, err := decode[StreamCredit](msg)
		if err != nil {
			return
		}
		n.grant(msg.ID, credit.Chunks)
	case TypeCapacity:
		// Beside the loop, like everything that can take time. Free space is a
		// statfs and costs nothing — until the file system it asks about hangs,
		// and then this cheap question would block every other one.
		go n.reply(ctx, t, msg.ID, TypeCapacityReport, n.capacity())
	case TypePullImage:
		req, err := decode[PullImage](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypePullResult, PullResult{Err: err.Error()})
			return
		}
		// In seinem eigenen Goroutine und ohne das Abbruch-Signal der
		// Verbindung: Ein Image sind mehrere Gigabyte, und ein Runner, der
		// währenddessen keine Sandbox mehr startet, hätte das Warten nur
		// verschoben.
		go func(req PullImage) {
			out, err := n.Docker.Pull(context.WithoutCancel(ctx), req.Image)
			res := PullResult{Image: req.Image}
			if err != nil {
				res.Err = firstLine(out, err)
			}
			n.reply(context.WithoutCancel(ctx), t, msg.ID, TypePullResult, res)
		}(req)
	case TypeUpdate:
		req, err := decode[Update](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeUpdateResult, UpdateResult{Err: err.Error()})
			return
		}
		// Beside the loop and without the connection's cancel signal: this
		// fetches tens of megabytes, and the answer has to get out before the
		// process is replaced.
		go n.update(context.WithoutCancel(ctx), t, msg.ID, req)
	case TypeSetLogLevel:
		in, err := decode[SetLogLevel](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeLogLevelResult, SetLogLevel{Level: levelName(n.logs.level())})
			return
		}
		if !n.logs.setLevel(in.Level) {
			n.Log.Warn("runner: unknown log level, keeping the current one", "asked", in.Level)
		} else {
			// At the level it was just set to, so that switching to debug
			// leaves a mark in the log it switched on.
			n.Log.Info("log level set", "level", in.Level)
		}
		n.reply(ctx, t, msg.ID, TypeLogLevelResult, SetLogLevel{Level: levelName(n.logs.level())})
	case TypeCheck:
		req, err := decode[Check](msg)
		if err != nil {
			req = Check{}
		}
		// The check spawns docker processes. Usually milliseconds, and a
		// diagnosis that blocks the host it is diagnosing is the last thing
		// anybody needs from it.
		go func() {
			problems, present := n.Docker.Check(ctx, req)
			n.reply(ctx, t, msg.ID, TypeCheckResult, CheckResult{Problems: problems, Present: present})
		}()
	default:
		n.Log.Warn("runner: unknown message", "type", msg.Type)
	}
}

// update replaces the binary and, if that worked, this process with it. The
// answer goes out first: the connection ends with the restart, and a control
// plane that only saw it drop could not tell a successful update from a host
// that fell over.
func (n *Node) update(ctx context.Context, t Transport, id string, req Update) {
	res := n.updateSelf(ctx, req)
	n.reply(ctx, t, id, TypeUpdateResult, res)
	if !res.Restarting {
		return
	}
	n.Log.Info("runner: binary replaced — restarting", "from", res.From, "to", res.To)
	// A moment for the answer to leave the wire. Send has handed it to the
	// kernel, but the exec closes the socket, and a message still sitting in a
	// buffer would be the one message an operator needs.
	time.Sleep(500 * time.Millisecond)
	restart := n.Restart
	if restart == nil {
		restart = execSelf
	}
	if err := restart(); err != nil {
		// exec does not return when it works. Whatever arrives here is a
		// failure, and the new binary is already in place — saying so is the
		// most useful thing left: a service manager brings the runner back,
		// and whoever started it by hand reads this line.
		n.Log.Error("runner: restart failed — the new binary is installed, start it again", "err", err)
	}
}

// adopt takes the sandbox containers a previous runner process left running
// back under watch: a proc and a watcher each, as if this process had started
// them. The containers belong to Docker and survived; the watcher and the
// count did not, and until #155 nothing brought them back (#155).
//
// The watchers get no transport of their own: the link is set once the
// handshake is out, and a death reported before `registered` would be the
// first message on the connection — which the pool refuses. Until then a
// report goes into the outbox and follows the handshake.
func (n *Node) adopt(ctx context.Context) {
	n.adopted.Do(func() {
		if n.Docker == nil {
			return
		}
		found, err := n.Docker.Running(ctx)
		if err != nil {
			n.Log.Warn("could not look for sandboxes a predecessor left running", "err", err)
			return
		}
		for _, sb := range found {
			watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
			proc := &sandboxProc{container: sb.Container, cancel: cancel}
			n.mu.Lock()
			if n.closed || n.running[sb.AgentID] != nil {
				n.mu.Unlock()
				cancel()
				continue
			}
			n.running[sb.AgentID] = proc
			n.mu.Unlock()
			n.watchers.Add(1)
			go func(agentID uuid.UUID, proc *sandboxProc) {
				defer n.watchers.Done()
				n.watch(watchCtx, nil, agentID, proc)
			}(sb.AgentID, proc)
			n.Log.Info("sandbox found running — taken under watch", "agent", sb.AgentID, "container", sb.Container)
		}
	})
}

// runningAgents lists whose sandboxes this host holds right now.
func (n *Node) runningAgents() []uuid.UUID {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]uuid.UUID, 0, len(n.running))
	for id := range n.running {
		out = append(out, id)
	}
	return out
}

// inOrder runs the work of a message beside the read loop, but keeps the
// messages of ONE agent in the order they arrived.
//
// Beside the loop, because a start is not a quick job: `docker run` fetches a
// missing workplace image, and those are gigabytes — the bound for it is an
// hour. Handled inside the loop, as it was, that hour was an hour in which this
// runner read nothing at all: not the start for the next agent, not the
// capacity question the runner page asks, not the check behind the warning
// banner. On covey.work one image pull thus stopped every agent of the
// organisation, and the host reported itself as connected the whole time,
// because the heartbeat has a goroutine of its own.
//
// In order, because start, stop and sync of one agent describe one working
// copy. A mutex would not do it: it hands the turn to whoever happens to be
// waiting, so a stop could overtake the start it belongs to. So each message
// takes the previous one's channel as its cue and leaves its own behind for the
// next — the handover is arranged here in the read loop, which is what makes
// the order the arrival order.
//
// Not for home_op: that is the file browser, and browsing an agent's home has
// no business waiting an hour for its sandbox to start.
func (n *Node) inOrder(agentID uuid.UUID, work func()) {
	n.mu.Lock()
	prev := n.turn[agentID]
	mine := make(chan struct{})
	n.turn[agentID] = mine
	n.mu.Unlock()

	go func() {
		defer close(mine)
		if prev != nil {
			<-prev
		}
		work()
		// Whoever is last in line takes the agent out of the map again —
		// otherwise every agent that ever ran here would leave an entry behind.
		n.mu.Lock()
		if n.turn[agentID] == mine {
			delete(n.turn, agentID)
		}
		n.mu.Unlock()
	}()
}

// sync writes the home into the store. Only after home_synced may anything be
// cleaned up locally — that is the one hard rule of the working copy.
func (n *Node) sync(ctx context.Context, t Transport, id string, req SyncHome) {
	if n.Blobs == nil {
		n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{AgentID: req.AgentID, Err: "no home store configured"})
		return
	}
	home, _, _ := n.Docker.AgentHome(req.AgentID)
	began := time.Now()
	n.Log.Debug("home sync started", "agent", req.AgentID, "path", home)
	// Gesagt, bevor es passiert: das Zurückschreiben eines gewachsenen Homes
	// dauert Minuten, und wer in dieser Zeit auf die Aufzeichnung sieht, soll
	// den Vorgang finden statt eine Lücke.
	n.say(ctx, t, Progress{AgentID: req.AgentID, Phase: PhaseHomeSync})
	res, err := homestore.SyncWatched(ctx, n.Blobs, req.OrgID, home, req.Excludes,
		n.ticker(ctx, t, req.AgentID, PhaseHomeSync, began, 0))
	if err != nil {
		n.Log.Error("home sync failed", "agent", req.AgentID, "path", home, "err", err)
		n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{AgentID: req.AgentID, Err: err.Error()})
		return
	}
	// Ab hier steht die Arbeitskopie auf diesem Schnappschuss — das ist die
	// Auskunft, die der nächste Weckruf braucht, um räumen zu dürfen.
	homestore.MarkSynced(home, res.ManifestHash)
	// Die Schlussmeldung der Phase: ab hier sind die Zahlen ein Ergebnis und
	// kein Zwischenstand mehr.
	n.say(ctx, t, Progress{
		AgentID: req.AgentID, Phase: PhaseHomeSync, Bytes: res.BytesUp,
		MS: time.Since(began).Milliseconds(), Done: true,
	})
	n.Log.Info("home synced", "agent", req.AgentID, "blocks", res.Blocks,
		"bytes_up", res.BytesUp, "total", res.TotalSize, "ms", time.Since(began).Milliseconds())
	// Kept if it cannot be delivered: the control plane records the snapshot
	// only when it hears this, and a snapshot it never hears of is one the next
	// wake materialises the previous state over (#153).
	n.replyKept(ctx, t, id, TypeHomeSynced, HomeSynced{
		AgentID: req.AgentID, ManifestHash: res.ManifestHash,
		TotalSize: res.TotalSize, Blocks: res.Blocks, BytesUp: res.BytesUp,
	})
}

func (n *Node) start(ctx context.Context, t Transport, id string, spec StartSandbox) {
	startedAt := time.Now()
	if n.isUpdating() {
		// The binary is being fetched and this process is about to be replaced.
		// A sandbox accepted now would be watched by nobody in a minute — the
		// case the busy check in updateSelf exists for, seen from the other
		// side (#161). Refused, so the pool asks the next host.
		n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{
			AgentID: spec.AgentID, Err: "this host is installing an update — not taking sandboxes until it is back",
		})
		return
	}
	// The limit is enforced here as well as in the scheduler. Not belt and
	// braces: the scheduler works from a count it keeps itself, and between its
	// decision and this start a second one can arrive. The host is the only
	// place that knows what is actually running on it — and it is the one that
	// falls over if the number is wrong.
	if n.MaxSandboxes > 0 {
		n.mu.Lock()
		running := len(n.running)
		_, replacing := n.running[spec.AgentID]
		n.mu.Unlock()
		// A restart of an agent that already runs here replaces its sandbox
		// rather than adding one — refusing that would make a full host unable
		// to restart its own agents.
		if !replacing && running >= n.MaxSandboxes {
			n.Log.Warn("sandbox refused — this host is at its limit",
				"agent", spec.AgentID, "running", running, "max", n.MaxSandboxes)
			n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{
				AgentID: spec.AgentID,
				Err: fmt.Sprintf("runner at its limit: %d of %d sandboxes running",
					running, n.MaxSandboxes),
			})
			return
		}
	}
	n.Log.Info("sandbox start requested", "agent", spec.AgentID, "image", spec.Image,
		"snapshot", spec.Snapshot != "")
	// The home comes out of the store before the sandbox goes in. Only what
	// differs is written — on the runner an agent last ran on that is the
	// normal case, and then this costs nothing at all.
	if why := n.copyPrevails(spec); why != "" && n.Blobs != nil {
		// The copy on this host is a LATER state than the snapshot the control
		// plane asked for — a sync whose answer never arrived, or a run whose
		// sync failed. Materialising the snapshot over it would rewrite every
		// file that run changed and bring back every file it deleted; prune=false
		// only spares the files it added (#153). So the copy is secured first and
		// the sandbox starts on it, and the control plane learns the state it
		// did not know.
		n.secureCopyBeforeStart(ctx, t, spec, why)
	} else if spec.Snapshot != "" && n.Blobs != nil {
		home, uid, gid := n.Docker.AgentHome(spec.AgentID)
		// Whose the restored files are. The runner is root on a runner host,
		// so without this every file it writes belongs to root and the agent
		// inside — uid 1001 — can read its home and change nothing in it
		// (#120). It was asked to tidy up caches it had no permission to
		// delete, and reported exactly that.
		besitzer := &homestore.Owner{UID: uid, GID: gid}
		// Said before it happens, not after: on a host the agent has not
		// worked on, this is minutes of copying, and afterwards is exactly
		// when nobody needs to be told any more.
		n.say(ctx, t, Progress{AgentID: spec.AgentID, Phase: PhaseHome})
		began := time.Now()

		// The newest state first, then the ones behind it. A snapshot whose
		// manifest cannot be read is not a reason to give up on the agent:
		// until #138 exactly that ended one for good, because the control
		// plane offered the same unreadable state every thirty seconds while
		// nine readable ones lay in the same table.
		//
		// Falling back does not delete from the working copy: pruning happens
		// only when the copy IS the snapshot being restored, and against an
		// older one it never is. Files the older snapshot knows are still
		// brought to its state — which is why copyPrevails runs before this,
		// and a copy that is the later state never gets here (#153).
		var err error
		for i, hash := range append([]string{spec.Snapshot}, spec.Fallbacks...) {
			var m homestore.Manifest
			m, err = homestore.Load(ctx, n.Blobs, spec.OrgID, hash)
			if err != nil {
				n.Log.Warn("snapshot unreadable", "agent", spec.AgentID,
					"snapshot", short8(hash), "err", err)
				continue
			}
			if i > 0 {
				// Not a log line: whoever looks at this agent has to see that
				// it is not running on the state it last saved.
				n.say(ctx, t, Progress{
					AgentID: spec.AgentID, Phase: PhaseHome,
					Detail: fmt.Sprintf("the last snapshot could not be read — going back %d state(s), to %s", i, short8(hash)),
				})
				n.Log.Warn("falling back to an older snapshot", "agent", spec.AgentID,
					"wanted", short8(spec.Snapshot), "using", short8(hash), "back", i)
			}

			// Räumen darf nur, wer weiß, dass diese Kopie unverändert der
			// Schnappschuss ist — also seit dem letzten gelungenen Sync keine
			// Sandbox darin gearbeitet hat. Sonst trägt sie Arbeit, die der
			// Schnappschuss nicht kennt, und die zu löschen hieße, das
			// Gedächtnis eines unfertigen Laufs wegzuwerfen: die
			// Sitzungstranskripte liegen im Home, und die Fortsetzung eines am
			// Turn-Limit abgebrochenen Laufs will genau sie fortsetzen.
			// Eine Datei zu viel kostet Platz, eine gelöschte kostet Arbeit,
			// die niemand zurückholt.
			stand := homestore.SyncedHash(home)
			raeumen := stand != "" && stand == hash
			var res homestore.MaterializeResult
			res, err = homestore.MaterializeOwned(ctx, n.Blobs, spec.OrgID, home, m, raeumen,
				n.ticker(ctx, t, spec.AgentID, PhaseHome, began, int64(len(m.Entries))), besitzer)
			if err != nil {
				break
			}
			if !raeumen {
				// Precisely what that means: files the snapshot does not know
				// stay; files it does know are brought to ITS state, changed
				// ones included. That is right here, because copyPrevails has
				// already ruled out that the copy is the later state — an
				// older snapshot is only ever written over a copy whose run it
				// supersedes, or one whose age nothing can tell (#153).
				n.Log.Warn("working copy carries files this snapshot does not know — keeping those, the rest is brought to the snapshot",
					"agent", spec.AgentID, "snapshot", short8(hash), "working_copy", short8(stand))
			}
			// Files the store no longer holds are named rather than fatal
			// (#138) — but they are named. A gap in a home that nobody
			// mentions is the one outcome worse than a refused start.
			if len(res.Missing) > 0 {
				n.Log.Warn("files missing from the store — restored without them",
					"agent", spec.AgentID, "snapshot", short8(hash), "files", len(res.Missing),
					"first", strings.Join(cut(res.Missing, 5), ", "))
				n.say(ctx, t, Progress{
					AgentID: spec.AgentID, Phase: PhaseHome,
					Detail: fmt.Sprintf("%d file(s) are no longer in the store and could not be restored: %s",
						len(res.Missing), strings.Join(cut(res.Missing, 5), ", ")),
				})
			}
			// Homes that predate #120 belong to root all the way down. Handing
			// one over walks it once and then leaves a marker — a home nobody
			// has to repair twice.
			if anzahl, repariert := homestore.Adopt(home, *besitzer); repariert {
				n.Log.Info("home handed to its agent", "agent", spec.AgentID, "entries", anzahl)
			}
			// Ab hier läuft gleich eine Sandbox darin: die Kopie gilt als
			// verändert, bis ein Sync das Gegenteil festhält.
			homestore.MarkInUse(home)
			n.Log.Info("home materialised", "agent", spec.AgentID,
				"bytes_in", res.BytesIn, "ms", time.Since(began).Milliseconds())
			n.say(ctx, t, Progress{
				AgentID: spec.AgentID, Phase: PhaseHome, Bytes: res.BytesIn,
				MS: time.Since(began).Milliseconds(), Done: true,
				Count: int64(len(m.Entries)), CountTotal: int64(len(m.Entries)),
			})
			err = nil
			break
		}
		if err != nil {
			// Nothing in the store could be read. There is one case left in
			// which starting is not a half state but the RIGHT one: the copy
			// on this host says it is exactly the snapshot that was asked for.
			//
			// SyncedHash is written after a successful sync and names the
			// state the copy was brought to. If it equals the snapshot the
			// control plane wants, then the files here ARE that state — the
			// store lost its copy of something this disk still holds. Refusing
			// then is not caution, it is throwing away the last intact copy:
			// on covey.work exactly this kept an agent down for six and a half
			// hours while its home lay complete on the runner (#138).
			//
			// Any other case still refuses. A copy that says nothing, or says
			// a different state, is not the snapshot — and an agent working on
			// one of those produces work nobody can place afterwards.
			if stand := homestore.SyncedHash(home); stand != "" && stand == spec.Snapshot {
				n.Log.Warn("the store lost this state — the working copy on this host is it",
					"agent", spec.AgentID, "snapshot", short8(spec.Snapshot), "err", err)
				n.say(ctx, t, Progress{
					AgentID: spec.AgentID, Phase: PhaseHome, Done: true,
					Detail: "the store could not produce this state; the working copy on this host is it, unchanged since the last sync",
				})
				homestore.MarkInUse(home)
				err = nil
			}
		}
		if err != nil {
			// Refused rather than started on a home that is not the one the
			// snapshot describes: an agent that silently works on a half state
			// produces work nobody can place afterwards. What changed with
			// #138 is only WHEN this is reached — after every state has been
			// tried, and after the working copy has been ruled out.
			n.Log.Error("materialising the home failed — sandbox refused",
				"agent", spec.AgentID, "snapshot", spec.Snapshot,
				"fallbacks", len(spec.Fallbacks), "err", err)
			n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{
				AgentID: spec.AgentID,
				Err:     "materialising the home failed: " + err.Error(),
			})
			return
		}
	}

	// `docker run` fetches a missing image by itself — silently, and for
	// several gigabytes. That silence is what makes a start look like a hang,
	// and it is the sentence this whole message type exists for.
	n.ensureImage(ctx, t, spec.AgentID, spec.Image)
	// The services' images are fetched the same way and for the same reason. A
	// project's database is a few hundred megabytes rather than a few gigabytes
	// — but it is fetched on the FIRST wake after somebody declared it, which
	// is exactly the moment they are watching to see whether the declaration
	// worked.
	for _, image := range sandbox.ServiceImages(spec.Services) {
		n.ensureImage(ctx, t, spec.AgentID, image)
	}

	container, services, err := n.Docker.Start(ctx, spec)
	if err != nil {
		// Docker's own words. A wrapped "start failed" hides which of "no such
		// image", "port in use" and "no space left" it was — and those call for
		// three different people.
		n.Log.Error("docker start failed", "agent", spec.AgentID, "image", spec.Image, "err", err)
		n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{AgentID: spec.AgentID, Err: err.Error()})
		return
	}
	n.watchSandbox(ctx, t, id, spec, container, services, startedAt)
}

// copyPrevails decides whether the working copy on this host is a later state
// than the snapshot the control plane asked for. Empty = it is not, or nothing
// can be said; then the snapshot is materialised as before.
//
// Two ways the copy is the later one, and both are ordinary rather than exotic:
//
//   - It is synced to a state the control plane does not hold, and that sync is
//     younger than the control plane's snapshot. That is a `home_synced` whose
//     answer was lost — the sync ran to the end while the connection dropped.
//   - It has been in use since AFTER the control plane's snapshot was taken and
//     never synced since: the post-job sync failed, or the runner was restarted
//     mid-run and the control plane never asked for one.
//
// The other direction is just as ordinary and must materialise: the agent ran
// on this host, its sync failed, and it then ran on ANOTHER host whose sync
// succeeded. Then the snapshot is the later state and the dirty copy here is
// superseded. Only the times tell the two apart, which is why they are kept.
func (n *Node) copyPrevails(spec StartSandbox) string {
	if spec.SnapshotAt.IsZero() || spec.Snapshot == "" {
		// An older control plane, or a first wake: nothing to compare against.
		return ""
	}
	home, _, _ := n.Docker.AgentHome(spec.AgentID)
	if stand := homestore.SyncedHash(home); stand != "" {
		if stand == spec.Snapshot {
			return ""
		}
		if at := homestore.SyncedAt(home); !at.IsZero() && at.After(spec.SnapshotAt) {
			return fmt.Sprintf("the working copy was synced to %s at %s, after the snapshot the control plane holds (%s, %s)",
				short8(stand), at.Format(time.RFC3339), short8(spec.Snapshot), spec.SnapshotAt.Format(time.RFC3339))
		}
		return ""
	}
	if since := homestore.InUseSince(home); !since.IsZero() && since.After(spec.SnapshotAt) {
		return fmt.Sprintf("the working copy has been in use since %s and was not synced since, the snapshot the control plane holds is from %s",
			since.Format(time.RFC3339), spec.SnapshotAt.Format(time.RFC3339))
	}
	return ""
}

// secureCopyBeforeStart syncs a working copy that prevails over the snapshot
// (see copyPrevails) and tells the control plane the state it produced. The
// start then proceeds on the copy, untouched.
//
// A sync that fails here does not refuse the start: the copy is the later
// state whether or not the store took it, and an agent working on it loses
// nothing that a refusal would save. What it costs is that the state exists on
// this host alone until the next sync goes through — said out loud, in the
// recording, as every unsecured state is.
func (n *Node) secureCopyBeforeStart(ctx context.Context, t Transport, spec StartSandbox, why string) {
	home, _, _ := n.Docker.AgentHome(spec.AgentID)
	n.Log.Warn("working copy is newer than the snapshot — securing it before the start", "agent", spec.AgentID, "why", why)
	n.say(ctx, t, Progress{AgentID: spec.AgentID, Phase: PhaseHomeSync,
		Detail: "the working copy on this host is newer than the snapshot — securing it first; " + why})
	began := time.Now()
	res, err := homestore.SyncWatched(ctx, n.Blobs, spec.OrgID, home, spec.Excludes,
		n.ticker(ctx, t, spec.AgentID, PhaseHomeSync, began, 0))
	if err != nil {
		n.Log.Error("securing the working copy failed — starting on it anyway, unsecured",
			"agent", spec.AgentID, "err", err)
		n.say(ctx, t, Progress{AgentID: spec.AgentID, Phase: PhaseHomeSync, Done: true,
			Detail: "securing the working copy failed — the sandbox starts on it, and the state exists on this host alone until the next sync: " + err.Error()})
		homestore.MarkInUse(home)
		return
	}
	homestore.MarkSynced(home, res.ManifestHash)
	n.say(ctx, t, Progress{AgentID: spec.AgentID, Phase: PhaseHomeSync, Bytes: res.BytesUp,
		MS: time.Since(began).Milliseconds(), Done: true})
	n.Log.Info("working copy secured before the start", "agent", spec.AgentID, "snapshot", short8(res.ManifestHash),
		"blocks", res.Blocks, "bytes_up", res.BytesUp, "ms", time.Since(began).Milliseconds())
	// Unasked, and kept until delivered: the control plane has to record this
	// state, or the next wake asks for the old one again.
	n.replyKept(ctx, t, "", TypeHomeSynced, HomeSynced{
		AgentID: spec.AgentID, ManifestHash: res.ManifestHash,
		TotalSize: res.TotalSize, Blocks: res.Blocks, BytesUp: res.BytesUp,
	})
	homestore.MarkInUse(home)
}

// ensureImage fetches an image if this host does not have it, and says how far
// it has got while it does.
func (n *Node) ensureImage(ctx context.Context, t Transport, agentID uuid.UUID, image string) {
	if image == "" {
		return
	}
	if !n.Docker.HasImage(ctx, image) {
		// The one line that explains an hour: this host does not have the
		// image and is about to fetch several gigabytes of it.
		n.Log.Info("image not present — fetching it", "agent", agentID, "image", image)
		n.say(ctx, t, Progress{AgentID: agentID, Phase: PhaseImage, Detail: image})
		// Deliberately fetched here instead of leaving it to `docker run`:
		// docker does the same thing either way, but only this way does
		// anybody find out how far it has got.
		pullBegan := time.Now()
		melden := n.gate(ctx, t)
		var letzte Progress
		out, err := n.Docker.PullWatched(ctx, image, func(pr PullProgress) {
			letzte = Progress{
				AgentID: agentID, Phase: PhaseImage, Detail: image,
				Bytes: pr.Bytes, BytesTotal: pr.Total,
				MS: time.Since(pullBegan).Milliseconds(),
			}
			melden(letzte)
		})
		if err != nil {
			// Not a reason to refuse: `docker run` fetches it too, and its
			// error message is the one worth reporting. What is worth keeping
			// is that the attempt was made and what it said.
			n.Log.Warn("fetching the image failed — leaving it to docker run",
				"agent", agentID, "image", image, "err", err, "out", tailOf(out))
		} else {
			n.Log.Info("image fetched", "agent", agentID, "image", image,
				"bytes", letzte.BytesTotal, "ms", time.Since(pullBegan).Milliseconds())
		}
		n.say(ctx, t, Progress{
			AgentID: agentID, Phase: PhaseImage, Detail: image,
			Bytes: letzte.BytesTotal, BytesTotal: letzte.BytesTotal, Done: true,
			MS: time.Since(pullBegan).Milliseconds(),
		})
	}
}

// watchSandbox registers the started container and hands it to a watcher that
// outlives this call. Split off from the start only so that fetching an image
// could become a step of its own — the sandbox has more than one image to wait
// for now that services stand beside it.
func (n *Node) watchSandbox(ctx context.Context, t Transport, id string, spec StartSandbox, container string, services []sandbox.ServiceRun, startedAt time.Time) {
	// The watcher outlives this call — it has to survive the run that started
	// it, because what it waits for is precisely the end nobody asked for.
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	proc := &sandboxProc{container: container, cancel: cancel}
	n.mu.Lock()
	if n.closed {
		// The node ended while this start was on its way. Take the watcher back
		// before it exists rather than leaving it to nobody, and say so — a
		// sandbox that was started but is watched by no one is worse than one
		// that was refused.
		n.mu.Unlock()
		cancel()
		n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{
			AgentID: spec.AgentID,
			Err:     "the runner node ended while the sandbox was starting",
		})
		return
	}
	if old := n.running[spec.AgentID]; old != nil {
		old.cancel()
	}
	n.running[spec.AgentID] = proc
	n.mu.Unlock()

	n.watchers.Add(1)
	go func() {
		defer n.watchers.Done()
		n.watch(watchCtx, t, spec.AgentID, proc)
	}()
	n.Log.Info("sandbox started", "agent", spec.AgentID, "container", container,
		"services", len(services), "ms", time.Since(startedAt).Milliseconds())
	// The services travel back with the answer: only this host knows which
	// image each one actually started from, and that is what a recording has to
	// be able to say six months later.
	n.reply(ctx, t, id, TypeSandboxStarted, SandboxResult{AgentID: spec.AgentID, Services: services})
}

// addServices brings services up beside a running sandbox.
//
// It refuses when there is no sandbox, and that refusal is the useful half: an
// agent asking for a database is asking from INSIDE its sandbox, so "there is
// none" means this runner is not the one holding it. Saying so is better than
// starting containers that would then stand beside nothing.
func (n *Node) addServices(ctx context.Context, t Transport, id string, req AddServices) {
	n.mu.Lock()
	proc := n.running[req.AgentID]
	n.mu.Unlock()
	if proc == nil {
		n.reply(ctx, t, id, TypeServicesAdded, SandboxResult{
			AgentID: req.AgentID,
			Err:     "this runner is not holding a sandbox for this agent",
		})
		return
	}
	for _, image := range sandbox.ServiceImages(req.Services) {
		n.ensureImage(ctx, t, req.AgentID, image)
	}
	started, err := n.Docker.AddServices(ctx, req.AgentID, proc.container, req.Services)
	if err != nil {
		n.Log.Warn("adding services failed", "agent", req.AgentID, "err", err)
		n.reply(ctx, t, id, TypeServicesAdded, SandboxResult{AgentID: req.AgentID, Err: err.Error()})
		return
	}
	n.Log.Info("services added", "agent", req.AgentID, "count", len(started))
	n.reply(ctx, t, id, TypeServicesAdded, SandboxResult{AgentID: req.AgentID, Services: started})
}

// watch waits for the container to end and reports it — unless somebody asked
// for the end. This is what turns a crash from a guess into a fact: without it
// the control plane notices only at the ReadyTimeout, and by then the reason
// is gone with the container.
func (n *Node) watch(ctx context.Context, t Transport, agentID uuid.UUID, proc *sandboxProc) {
	reason := n.Docker.Wait(ctx, proc.container)
	if ctx.Err() != nil {
		return
	}

	n.mu.Lock()
	asked := proc.stopping
	if n.running[agentID] == proc {
		delete(n.running, agentID)
	}
	n.mu.Unlock()
	if asked {
		n.Log.Info("sandbox stopped", "agent", agentID, "container", proc.container)
		return
	}
	if reason == "" {
		return
	}
	// Nobody asked for this end. It is the most useful line this runner
	// writes: the control plane learns it as a fact, and the reason is here
	// while the container's own words still exist.
	n.Log.Warn("sandbox ended on its own", "agent", agentID,
		"container", proc.container, "reason", reason)

	msg, err := encode(TypeSandboxExited, "", SandboxExited{AgentID: agentID, Reason: reason})
	if err != nil {
		return
	}
	// Over the connection that stands NOW, not the one that started the
	// sandbox — and kept for the next one if none does. This is the message
	// the whole watcher exists for, and until #154 a single reconnect between
	// start and death sent it into a closed socket.
	if err := n.send(ctx, t, msg); err != nil {
		n.Log.Warn("runner: the end of a sandbox could not be reported yet — kept for the next connection",
			"agent", agentID, "err", err)
		n.keep(msg)
	}
}

func (n *Node) stop(ctx context.Context, t Transport, id string, agentID uuid.UUID) {
	n.mu.Lock()
	proc := n.running[agentID]
	if proc != nil {
		proc.stopping = true
		delete(n.running, agentID)
	}
	n.mu.Unlock()

	if proc == nil {
		// Nothing running under this agent. Not an error: a control plane that
		// has restarted cleans up sandboxes it never started itself, and a
		// complaint here would only be noise.
		n.reply(ctx, t, id, TypeSandboxStopped, SandboxResult{AgentID: agentID})
		return
	}
	err := n.Docker.Stop(ctx, proc.container)
	proc.cancel()
	res := SandboxResult{AgentID: agentID}
	if err != nil {
		n.Log.Error("stopping the sandbox failed", "agent", agentID,
			"container", proc.container, "err", err)
		res.Err = err.Error()
	}
	n.reply(ctx, t, id, TypeSandboxStopped, res)
}

// Close ends every watcher this node still holds. A watcher deliberately
// outlives the call that started it — that is what turns a crash into a
// reported fact rather than a guess — but it must not outlive the node
// itself: what it blocks in is a `docker wait` child process, and a node that
// disappears without cancelling it leaves that process behind for good. It
// then polls a container that will never exist, forever, and nothing on the
// host still knows what it belongs to.
//
// Not called when a connection drops: RunNode reconnects, and a sandbox that
// dies in between is precisely the death worth reporting. Only the owner of
// the node's lifetime closes it.
//
// It is final, not just a drain: a start that arrives afterwards is refused
// rather than served, because serving it would create the very watcher this
// method just took away.
func (n *Node) Close() {
	n.mu.Lock()
	n.closed = true
	procs := make([]*sandboxProc, 0, len(n.running))
	for agentID, proc := range n.running {
		procs = append(procs, proc)
		delete(n.running, agentID)
	}
	n.mu.Unlock()
	// Outside the lock: cancel wakes the watcher, which takes the lock itself.
	for _, proc := range procs {
		proc.cancel()
	}

	// And now wait until the cancelled work has actually stopped. Otherwise
	// Close means "the signal is set", the caller reads it as "the node is
	// finished", and the difference between the two is a watcher still writing
	// where nobody expects one any more.
	//
	// With a deadline, because the waiting must not become the new hanging. The
	// bound is generous on purpose: a watcher whose container ended by itself
	// is inside `Docker.Wait`, whose removal carries its own 20-second limit
	// (docker.go), and cutting that off would leave exactly the orphaned
	// container this node exists to avoid. Whoever misses the deadline is
	// named in the log — that is the note somebody needs to find it next time.
	done := make(chan struct{})
	go func() {
		n.watchers.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(closeGrace):
		n.Log.Warn("runner node: a sandbox watcher is still running after the grace period",
			"grace", closeGrace)
	}
}

// closeGrace is how long Close waits for the watchers it has just cancelled.
const closeGrace = 30 * time.Second

// say sends a line about work in progress. Nobody waits for it and nothing
// depends on it: a failure to send it must not fail the start it describes.
func (n *Node) say(ctx context.Context, t Transport, p Progress) {
	msg, err := encode(TypeProgress, "", p)
	if err != nil {
		return
	}
	_ = n.send(ctx, t, msg)
}

func (n *Node) reply(ctx context.Context, t Transport, id, msgType string, payload any) {
	msg, err := encode(msgType, id, payload)
	if err != nil {
		n.Log.Error("runner: encoding the answer failed", "type", msgType, "err", err)
		return
	}
	if err := n.send(ctx, t, msg); err != nil && !errors.Is(err, ErrTransportClosed) {
		n.Log.Warn("runner: sending the answer failed", "type", msgType, "err", err)
	}
}

// replyKept is reply for an answer that must arrive even if this connection
// does not survive to carry it: it is kept and sent first on the next one. The
// correlation id travels along — the control plane's waiter will be gone by
// then, and it handles the message as what it is rather than as an answer.
func (n *Node) replyKept(ctx context.Context, t Transport, id, msgType string, payload any) {
	msg, err := encode(msgType, id, payload)
	if err != nil {
		n.Log.Error("runner: encoding the answer failed", "type", msgType, "err", err)
		return
	}
	if err := n.send(ctx, t, msg); err != nil {
		n.Log.Warn("runner: answer could not be delivered — kept for the next connection",
			"type", msgType, "err", err)
		n.keep(msg)
	}
}

// send puts a message on the connection that stands now. fallback is the
// transport the work was handed when it began; it is used only while Run has
// never set a link — which is the tests' case, not production's.
func (n *Node) send(ctx context.Context, fallback Transport, msg Message) error {
	n.linkMu.Lock()
	t := n.link
	n.linkMu.Unlock()
	if t == nil {
		t = fallback
	}
	if t == nil {
		return ErrTransportClosed
	}
	return t.Send(ctx, msg)
}

func (n *Node) setLink(t Transport) {
	n.linkMu.Lock()
	n.link = t
	n.linkMu.Unlock()
}

// keep holds a message for the next connection. Oldest out when full — what is
// dropped is named, once, because a silent drop here is a silent data loss one
// layer up.
func (n *Node) keep(msg Message) {
	n.linkMu.Lock()
	defer n.linkMu.Unlock()
	if len(n.outbox) >= outboxLimit {
		n.Log.Warn("runner: outbox full — dropping the oldest undelivered message", "type", n.outbox[0].Type)
		n.outbox = n.outbox[1:]
	}
	n.outbox = append(n.outbox, msg)
}

// flushOutbox delivers what earlier connections could not. Whatever fails
// again stays kept, in order.
func (n *Node) flushOutbox(ctx context.Context, t Transport) {
	n.linkMu.Lock()
	pending := n.outbox
	n.outbox = nil
	n.linkMu.Unlock()
	for i, msg := range pending {
		if err := t.Send(ctx, msg); err != nil {
			n.linkMu.Lock()
			n.outbox = append(pending[i:], n.outbox...)
			n.linkMu.Unlock()
			return
		}
		n.Log.Info("runner: delivered a message the last connection could not", "type", msg.Type)
	}
}

// capacity is what this runner is carrying. The free space comes from the file
// system the working copies lie on — exactly the figure that decides whether
// the next home still fits.
func (n *Node) capacity() CapacityReport {
	n.mu.Lock()
	running := len(n.running)
	n.mu.Unlock()

	work := n.Docker.DataDir
	total, free := diskSpace(work)
	return CapacityReport{Sandboxes: running, MaxSandboxes: n.MaxSandboxes,
		TotalBytes: total, FreeBytes: free, WorkDir: work}
}

// short8 kürzt einen Hash auf das, was in eine Logzeile gehört.
func short8(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	if h == "" {
		return "(unbekannt)"
	}
	return h
}

// progressEvery: how often a phase that is still running says so. Every report
// is a row in the agent's recording and a line in the runner's log, so it has
// to be rare enough that a quarter of an hour of syncing stays readable
// afterwards — and frequent enough that somebody watching now sees movement.
const progressEvery = 15 * time.Second

// gate hands back a way to report progress that passes at most one report every
// progressEvery, however often it is called. The caller reports whenever it
// knows something new; how much of that is worth keeping is decided here.
func (n *Node) gate(ctx context.Context, t Transport) func(Progress) {
	var mu sync.Mutex
	last := time.Now()
	return func(p Progress) {
		mu.Lock()
		if time.Since(last) < progressEvery {
			mu.Unlock()
			return
		}
		last = time.Now()
		mu.Unlock()
		n.say(ctx, t, p)
	}
}

// ticker is that gate as the sign of life a home operation gives: how many
// entries it has dealt with, of how many, and how many bytes went over the wire
// doing it. total 0 means the operation does not know its own end.
func (n *Node) ticker(ctx context.Context, t Transport, agentID uuid.UUID, phase string, began time.Time, total int64) homestore.Watch {
	melden := n.gate(ctx, t)
	return func(seen int, bytes int64) {
		melden(Progress{
			AgentID: agentID, Phase: phase,
			Count: int64(seen), CountTotal: total, Bytes: bytes,
			MS: time.Since(began).Milliseconds(),
		})
	}
}

// tailOf keeps the end of a command's output: that is where the reason stands,
// and the beginning is a list of layers nobody reads.
func tailOf(out string) string {
	const keep = 500
	if len(out) <= keep {
		return out
	}
	return "…" + out[len(out)-keep:]
}

// cut is the first n of a list — for a message that names examples rather than
// a list nobody reads. A home has six figures of files; five of them say what
// kind of thing is missing, and the count says how much.
func cut(list []string, n int) []string {
	if len(list) <= n {
		return list
	}
	return append(append([]string{}, list[:n]...), "…")
}
