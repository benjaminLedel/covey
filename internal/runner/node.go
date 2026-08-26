package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/buildinfo"
	"covey/internal/homestore"
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
	// turn holds, per agent, the end of the line: the channel the message
	// currently being worked on closes when it is done. See inOrder.
	turn map[uuid.UUID]chan struct{}
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
}

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
	}
}

// Run speaks the protocol until the transport ends or ctx is cancelled. It
// registers first: the control plane may not assign anything to a runner whose
// identity and protocol version it does not know.
func (n *Node) Run(ctx context.Context, t Transport) error {
	hello, err := encode(TypeRegistered, "", Registered{
		RunnerID: n.RunnerID,
		OrgID:    n.OrgID,
		Protocol: Protocol,
		Version:  buildinfo.String(),
		Arch:     runtime.GOARCH,
		Tags:     n.Tags,
		Images:   n.Images,
		Features: []string{FeatureSelfUpdate},

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

	beat, stopBeat := context.WithCancel(ctx)
	defer stopBeat()
	go n.heartbeat(beat, t)
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
		n.handle(ctx, t, msg)
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
	res, err := homestore.Sync(ctx, n.Blobs, req.OrgID, home, req.Excludes)
	if err != nil {
		n.Log.Error("home sync failed", "agent", req.AgentID, "path", home, "err", err)
		n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{AgentID: req.AgentID, Err: err.Error()})
		return
	}
	n.Log.Info("home synced", "agent", req.AgentID, "blocks", res.Blocks,
		"bytes_up", res.BytesUp, "total", res.TotalSize, "ms", time.Since(began).Milliseconds())
	n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{
		AgentID: req.AgentID, ManifestHash: res.ManifestHash,
		TotalSize: res.TotalSize, Blocks: res.Blocks, BytesUp: res.BytesUp,
	})
}

func (n *Node) start(ctx context.Context, t Transport, id string, spec StartSandbox) {
	startedAt := time.Now()
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
	if spec.Snapshot != "" && n.Blobs != nil {
		home, _, _ := n.Docker.AgentHome(spec.AgentID)
		// Said before it happens, not after: on a host the agent has not
		// worked on, this is minutes of copying, and afterwards is exactly
		// when nobody needs to be told any more.
		n.say(ctx, t, Progress{AgentID: spec.AgentID, Phase: PhaseHome})
		began := time.Now()
		m, err := homestore.Load(ctx, n.Blobs, spec.OrgID, spec.Snapshot)
		if err == nil {
			var res homestore.MaterializeResult
			res, err = homestore.Materialize(ctx, n.Blobs, spec.OrgID, home, m)
			if err == nil {
				n.Log.Info("home materialised", "agent", spec.AgentID,
					"bytes_in", res.BytesIn, "ms", time.Since(began).Milliseconds())
				n.say(ctx, t, Progress{
					AgentID: spec.AgentID, Phase: PhaseHome, Bytes: res.BytesIn,
					MS: time.Since(began).Milliseconds(),
				})
			}
		}
		if err != nil {
			// Refused rather than started on a home that is not the one the
			// snapshot describes: an agent that silently works on a half state
			// produces work nobody can place afterwards.
			n.Log.Error("materialising the home failed — sandbox refused",
				"agent", spec.AgentID, "snapshot", spec.Snapshot, "err", err)
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
	if !n.Docker.HasImage(ctx, spec.Image) {
		// The one line that explains an hour: this host does not have the
		// image and is about to fetch several gigabytes of it.
		n.Log.Info("image not present — docker will fetch it", "agent", spec.AgentID, "image", spec.Image)
		n.say(ctx, t, Progress{AgentID: spec.AgentID, Phase: PhaseImage, Detail: spec.Image})
	}
	container, err := n.Docker.Start(ctx, spec)
	if err != nil {
		// Docker's own words. A wrapped "start failed" hides which of "no such
		// image", "port in use" and "no space left" it was — and those call for
		// three different people.
		n.Log.Error("docker start failed", "agent", spec.AgentID, "image", spec.Image, "err", err)
		n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{AgentID: spec.AgentID, Err: err.Error()})
		return
	}

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

	go n.watch(watchCtx, t, spec.AgentID, proc)
	n.Log.Info("sandbox started", "agent", spec.AgentID, "container", container,
		"ms", time.Since(startedAt).Milliseconds())
	n.reply(ctx, t, id, TypeSandboxStarted, SandboxResult{AgentID: spec.AgentID})
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
	if err := t.Send(ctx, msg); err != nil {
		n.Log.Warn("runner: reporting the end of a sandbox failed", "agent", agentID, "err", err)
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
}

// say sends a line about work in progress. Nobody waits for it and nothing
// depends on it: a failure to send it must not fail the start it describes.
func (n *Node) say(ctx context.Context, t Transport, p Progress) {
	msg, err := encode(TypeProgress, "", p)
	if err != nil {
		return
	}
	_ = t.Send(ctx, msg)
}

func (n *Node) reply(ctx context.Context, t Transport, id, msgType string, payload any) {
	msg, err := encode(msgType, id, payload)
	if err != nil {
		n.Log.Error("runner: encoding the answer failed", "type", msgType, "err", err)
		return
	}
	if err := t.Send(ctx, msg); err != nil && !errors.Is(err, ErrTransportClosed) {
		n.Log.Warn("runner: sending the answer failed", "type", msgType, "err", err)
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
