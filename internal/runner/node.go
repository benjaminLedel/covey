package runner

import (
	"context"
	"errors"
	"log/slog"
	"sync"

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
	// Blobs is the home store. The built-in runner reaches it directly — it
	// sits in the process that owns it; that is a transport detail, and the
	// sync logic above does not know the difference. nil = no store, and the
	// home stays what lies in the working copy.
	Blobs homestore.BlobStore

	mu      sync.Mutex
	running map[uuid.UUID]*sandboxProc
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
	return &Node{
		RunnerID: runnerID,
		OrgID:    orgID,
		Docker:   docker,
		Log:      log,
		running:  map[uuid.UUID]*sandboxProc{},
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
		Tags:     n.Tags,
	})
	if err != nil {
		return err
	}
	if err := t.Send(ctx, hello); err != nil {
		return err
	}

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

func (n *Node) handle(ctx context.Context, t Transport, msg Message) {
	switch msg.Type {
	case TypeStartSandbox:
		spec, err := decode[StartSandbox](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeSandboxFailed, SandboxResult{Err: err.Error()})
			return
		}
		n.start(ctx, t, msg.ID, spec)
	case TypeStopSandbox:
		spec, err := decode[StopSandbox](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeSandboxStopped, SandboxResult{Err: err.Error()})
			return
		}
		n.stop(ctx, t, msg.ID, spec.AgentID)
	case TypeSyncHome:
		req, err := decode[SyncHome](msg)
		if err != nil {
			n.reply(ctx, t, msg.ID, TypeHomeSynced, HomeSynced{Err: err.Error()})
			return
		}
		n.sync(ctx, t, msg.ID, req)
	case TypeCheck:
		req, err := decode[Check](msg)
		if err != nil {
			req = Check{}
		}
		n.reply(ctx, t, msg.ID, TypeCheckResult, CheckResult{Problems: n.Docker.Check(ctx, req.Images)})
	default:
		n.Log.Warn("runner: unknown message", "type", msg.Type)
	}
}

// sync writes the home into the store. Only after home_synced may anything be
// cleaned up locally — that is the one hard rule of the working copy.
func (n *Node) sync(ctx context.Context, t Transport, id string, req SyncHome) {
	if n.Blobs == nil {
		n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{AgentID: req.AgentID, Err: "no home store configured"})
		return
	}
	home, _, _ := n.Docker.AgentHome(req.AgentID)
	res, err := homestore.Sync(ctx, n.Blobs, req.OrgID, home, req.Excludes)
	if err != nil {
		n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{AgentID: req.AgentID, Err: err.Error()})
		return
	}
	n.reply(ctx, t, id, TypeHomeSynced, HomeSynced{
		AgentID: req.AgentID, ManifestHash: res.ManifestHash,
		TotalSize: res.TotalSize, Blocks: res.Blocks, BytesUp: res.BytesUp,
	})
}

func (n *Node) start(ctx context.Context, t Transport, id string, spec StartSandbox) {
	// The home comes out of the store before the sandbox goes in. Only what
	// differs is written — on the runner an agent last ran on that is the
	// normal case, and then this costs nothing at all.
	if spec.Snapshot != "" && n.Blobs != nil {
		home, _, _ := n.Docker.AgentHome(spec.AgentID)
		m, err := homestore.Load(ctx, n.Blobs, spec.OrgID, spec.Snapshot)
		if err == nil {
			_, err = homestore.Materialize(ctx, n.Blobs, spec.OrgID, home, m)
		}
		if err != nil {
			// Refused rather than started on a home that is not the one the
			// snapshot describes: an agent that silently works on a half state
			// produces work nobody can place afterwards.
			n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{
				AgentID: spec.AgentID,
				Err:     "materialising the home failed: " + err.Error(),
			})
			return
		}
	}

	container, err := n.Docker.Start(ctx, spec)
	if err != nil {
		n.reply(ctx, t, id, TypeSandboxFailed, SandboxResult{AgentID: spec.AgentID, Err: err.Error()})
		return
	}

	// The watcher outlives this call — it has to survive the run that started
	// it, because what it waits for is precisely the end nobody asked for.
	watchCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	proc := &sandboxProc{container: container, cancel: cancel}
	n.mu.Lock()
	if old := n.running[spec.AgentID]; old != nil {
		old.cancel()
	}
	n.running[spec.AgentID] = proc
	n.mu.Unlock()

	go n.watch(watchCtx, t, spec.AgentID, proc)
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
	if asked || reason == "" {
		return
	}

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
		res.Err = err.Error()
	}
	n.reply(ctx, t, id, TypeSandboxStopped, res)
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

// AgentHome is the local route to an agent's home. Only the built-in runner
// answers this directly — for a remote one the way there is home_op over the
// runner link.
func (n *Node) AgentHome(agentID uuid.UUID) (string, int, int) {
	return n.Docker.AgentHome(agentID)
}
