package runner

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"covey/internal/orchestrator"
)

// Pool is the control plane's side of the runner protocol and at the same time
// the orchestrator's SandboxProvider: from up there, "start a sandbox" looks
// exactly as it did when the local Docker CLI was called directly.
//
// It knows agents and profiles; a runner knows images and containers. That cut
// is why the runner needs no access to anything the platform holds.
type Pool struct {
	Log *slog.Logger
	// DefaultImage applies to agents that name no workplace, Profiles maps a
	// profile name to its image (spec/16). A value that is neither is taken as
	// an image reference — the "org-owned: anything" row of the profile table.
	DefaultImage string
	Profiles     map[string]string
	// AgentImages reports which workplaces the agents are configured for
	// (value → number of agents); the self-check asks the runners about
	// exactly those. nil = unknown, then only the default is asked about.
	AgentImages func(ctx context.Context) (map[string]int, error)
	// SandboxDied is called when a runner reports the end of a sandbox nobody
	// asked for. nil = the report is only logged.
	SandboxDied func(agentID uuid.UUID, reason string)
	// EnsureLocal is asked when an organisation has no connected runner: the
	// built-in one is created on first use, because organisations come into
	// being while the process runs. nil = nothing is created, and the
	// organisation simply has no runner.
	EnsureLocal func(ctx context.Context, orgID uuid.UUID) error
	// LatestSnapshot is the state an agent's home is materialised to on wake.
	// nil or an empty hash = the working copy on the runner applies, which is
	// what the very first wake looks like.
	LatestSnapshot func(ctx context.Context, agentID uuid.UUID) (string, error)
	// SnapshotTaken files a completed sync. Only afterwards may anything be
	// cleaned up locally — no prune before a successful sync (spec/16).
	SnapshotTaken func(ctx context.Context, agentID, runnerID uuid.UUID, res HomeSynced) error
	// HomeExcludes are the paths left out of the sync. Empty = everything is
	// synced, which is the default on purpose: the list is a cost question, not
	// a prerequisite for correctness.
	HomeExcludes []string
	// StartTimeout bounds how long a start may take before it counts as
	// failed. Without it a runner that has gone quiet would hold the wake
	// until the orchestrator's ReadyTimeout — and the message would then blame
	// the daemon for something the runner never did.
	StartTimeout time.Duration

	mu    sync.Mutex
	conns map[uuid.UUID]*conn // runner ID → connection
	// ensuring serialises EnsureLocal per organisation: without it, two
	// simultaneous wakes in a fresh organisation would each start a built-in
	// runner, and the second would take the first one's place.
	ensuring map[uuid.UUID]*sync.Mutex
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
	tags     []string
	t        Transport
	pool     *Pool

	mu      sync.Mutex
	waiters map[string]chan Message
	// sandboxes counts what is running here — the whole of the scheduling
	// weight for now: no bin packing, no resource modelling.
	sandboxes int
}

// ErrNoRunner: this organisation has nothing that could carry the sandbox.
var ErrNoRunner = errors.New("no runner available")

const defaultStartTimeout = 2 * time.Minute

func NewPool(log *slog.Logger) *Pool {
	if log == nil {
		log = slog.Default()
	}
	return &Pool{Log: log, conns: map[uuid.UUID]*conn{}, ensuring: map[uuid.UUID]*sync.Mutex{}}
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
		protocol: reg.Protocol, version: reg.Version, tags: reg.Tags,
		t: t, pool: p, waiters: map[string]chan Message{},
	}
	p.mu.Lock()
	p.conns[reg.RunnerID] = c
	p.mu.Unlock()
	p.Log.Info("runner connected", "runner", reg.RunnerID, "org", reg.OrgID,
		"builtin", builtin, "version", reg.Version)
	return c, nil
}

func (p *Pool) detach(c *conn) {
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
	for {
		msg, err := c.t.Receive(ctx)
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
			ch := c.waiters[msg.ID]
			delete(c.waiters, msg.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- msg
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
		case TypeHeartbeat:
		default:
			c.pool.Log.Warn("runner: unexpected message", "type", msg.Type, "runner", c.runnerID)
		}
	}
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
	case <-ctx.Done():
		return Message{}, fmt.Errorf("runner %s did not answer %s: %w", c.runnerID, msgType, ctx.Err())
	}
}

// ensureAndPick gives an organisation without a runner its built-in one and
// tries again. Serialised per organisation: two simultaneous wakes must not
// each start one.
func (p *Pool) ensureAndPick(ctx context.Context, orgID uuid.UUID) (*conn, error) {
	if p.EnsureLocal == nil {
		return nil, fmt.Errorf("%w: this organisation has no connected runner", ErrNoRunner)
	}
	p.mu.Lock()
	lock := p.ensuring[orgID]
	if lock == nil {
		lock = &sync.Mutex{}
		p.ensuring[orgID] = lock
	}
	p.mu.Unlock()

	lock.Lock()
	defer lock.Unlock()
	// Whoever waited on the lock may find the runner already there.
	if c, err := p.pick(orgID); err == nil {
		return c, nil
	}
	if err := p.EnsureLocal(ctx, orgID); err != nil {
		return nil, err
	}
	return p.pick(orgID)
}

// imageFor resolves an agent's workplace. One rule, in this order: nothing
// named → the instance default; a known profile → its image; anything else →
// taken literally.
func (p *Pool) imageFor(want string) string {
	want = strings.TrimSpace(want)
	if want == "" {
		return p.DefaultImage
	}
	if img, ok := p.Profiles[want]; ok && img != "" {
		return img
	}
	return want
}

// pick chooses the runner for an agent. Deliberately simple, because no runner
// has to be "the right one" — only the cheapest.
func (p *Pool) pick(orgID uuid.UUID) (*conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var candidates []*conn
	for _, c := range p.conns {
		// The organisation comes first and is not a filter among others: a
		// runner of a foreign tenant is not a worse candidate, it is none.
		if c.orgID == orgID {
			candidates = append(candidates, c)
		}
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("%w: this organisation has no connected runner", ErrNoRunner)
	}
	// The fewest running sandboxes wins; the runner ID breaks ties so that the
	// choice does not depend on map order.
	sort.Slice(candidates, func(i, j int) bool {
		ci, cj := candidates[i], candidates[j]
		ci.mu.Lock()
		ni := ci.sandboxes
		ci.mu.Unlock()
		cj.mu.Lock()
		nj := cj.sandboxes
		cj.mu.Unlock()
		if ni != nj {
			return ni < nj
		}
		return ci.runnerID.String() < cj.runnerID.String()
	})
	return candidates[0], nil
}

// Start satisfies orchestrator.SandboxProvider.
func (p *Pool) Start(ctx context.Context, spec orchestrator.SandboxSpec) (orchestrator.Sandbox, error) {
	c, err := p.pick(spec.OrgID)
	if errors.Is(err, ErrNoRunner) {
		if c, err = p.ensureAndPick(ctx, spec.OrgID); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	timeout := p.StartTimeout
	if timeout <= 0 {
		timeout = defaultStartTimeout
	}
	// Which state the home is brought to. A failure here is not fatal: the
	// working copy on the runner is then what applies — slower or older, but
	// an agent that cannot start at all because a lookup failed would be the
	// worse outcome.
	snapshot := ""
	if p.LatestSnapshot != nil {
		if hash, err := p.LatestSnapshot(ctx, spec.AgentID); err != nil {
			p.Log.Warn("last snapshot not readable — the working copy applies",
				"agent", spec.AgentID, "err", err)
		} else {
			snapshot = hash
		}
	}

	answer, err := c.ask(ctx, TypeStartSandbox, StartSandbox{
		AgentID:     spec.AgentID,
		OrgID:       spec.OrgID,
		Image:       p.imageFor(spec.Image),
		HomeDir:     spec.HomeDir,
		Env:         spec.Env,
		EgressToken: spec.EgressToken,
		Snapshot:    snapshot,
	}, timeout)
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
	c.mu.Lock()
	c.sandboxes++
	c.mu.Unlock()
	return &poolSandbox{pool: p, conn: c, agentID: spec.AgentID, orgID: spec.OrgID}, nil
}

type poolSandbox struct {
	pool    *Pool
	conn    *conn
	agentID uuid.UUID
	orgID   uuid.UUID
	once    sync.Once
}

// Stop shuts the compute down and writes the home into the store. In that
// order: the scan runs on a home nothing is writing into any more.
//
// This is the real falling-asleep. A warm-parked sandbox never gets here — the
// warm session (spec/03) stays untouched by the sync, which is what keeps the
// sleep path from becoming the slow path for agents that wake every few
// minutes.
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
	if p.SnapshotTaken == nil {
		return
	}
	answer, err := c.ask(ctx, TypeSyncHome, SyncHome{
		AgentID: agentID, OrgID: orgID, Excludes: p.HomeExcludes,
	}, 30*time.Minute) // the first sync of a grown home is a full pass
	if err != nil {
		p.Log.Warn("home not synced", "agent", agentID, "err", err)
		return
	}
	res, err := decode[HomeSynced](answer)
	if err != nil || res.Err != "" {
		p.Log.Warn("home not synced", "agent", agentID, "err", firstNonEmpty(errString(err), res.Err))
		return
	}
	if err := p.SnapshotTaken(ctx, agentID, c.runnerID, res); err != nil {
		p.Log.Warn("snapshot not recorded", "agent", agentID, "err", err)
	}
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

// AgentHome satisfies orchestrator.FileAccess. The built-in runner answers
// from the file system next door; a remote one needs home_op over the runner
// link, which is not built yet — and until it is, the honest answer is that
// this provider has no reachable home rather than a path that is not there.
func (p *Pool) AgentHome(agentID uuid.UUID) (orchestrator.Home, error) {
	p.mu.Lock()
	local := p.local
	p.mu.Unlock()
	if local == nil {
		return orchestrator.Home{}, orchestrator.ErrNoFileAccess
	}
	path, uid, gid := local.AgentHome(agentID)
	return orchestrator.Home{Path: path, UID: uid, GID: gid}, nil
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
	var problems []string
	for _, c := range conns {
		answer, err := c.ask(ctx, TypeCheck, Check{Images: images}, 30*time.Second)
		if err != nil {
			problems = append(problems, fmt.Sprintf("runner %s does not answer: %v", short(c.runnerID), err))
			continue
		}
		res, err := decode[CheckResult](answer)
		if err != nil {
			problems = append(problems, fmt.Sprintf("runner %s: %v", short(c.runnerID), err))
			continue
		}
		for _, problem := range res.Problems {
			// Only name the runner once there is more than one — on the normal
			// installation the prefix would be noise in every line.
			if len(conns) > 1 {
				problem = "runner " + short(c.runnerID) + ": " + problem
			}
			problems = append(problems, problem)
		}
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
		image := p.imageFor(want)
		if image == "" || seen[image] {
			continue
		}
		seen[image] = true
		out = append(out, image)
	}
	sort.Strings(out)
	return out
}
