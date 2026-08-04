// Package orchestrator is the control plane side of the daemon protocol:
// dispatch loop (LISTEN/NOTIFY + tick), agent sessions (serial, one task at a
// time), secrets broker decisions, guard-rail enforcement, kill switch.
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/daemon"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/reqlog"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/secrets"
	"covey/internal/skills"
	"covey/internal/target"
	targetstore "covey/internal/target/store"
)

// defaultMaxTurns is the runaway guard per runtime run when the agent has not
// set a turn limit of its own (agents.max_turns = 0).
const defaultMaxTurns = 30

// DaemonLink abstracts the bidirectional connection to a sandbox daemon. The
// HTTP layer implements it over WebSocket; tests do it in-process.
type DaemonLink interface {
	Send(ctx context.Context, msg daemon.Message) error
	Receive(ctx context.Context) (daemon.Message, error)
	Close() error
}

type Options struct {
	Pool     *pgxpool.Pool
	Registry *agents.Registry
	Backlog  *backlog.Store
	Obs      *observability.Store
	Rails    *guardrails.Store
	Secrets  secrets.Store
	Identity identity.Provider
	Memory   *memory.Store
	// Skills are the agents' skills (library + agent-owned). nil = feature
	// switched off; runs then get no skills materialized.
	Skills  *skills.Store
	Targets *targetstore.Store
	Egress  *egress.Store
	// ReqLog records the HTTP requests of the target-system plugins (diagnosis,
	// spec/06). nil = request log switched off; the sandbox's events are then
	// discarded.
	ReqLog         *reqlogstore.Store
	Provider       SandboxProvider
	PublicWSURL    string // ws://…/api/daemon/ws — reachable from sandboxes
	DaemonTokenTTL time.Duration
	TickInterval   time.Duration
	// WikiMaintenanceInterval paces the wiki consolidation pass (spec/05:
	// task-independent, not in the hot path). 0 → default.
	WikiMaintenanceInterval time.Duration
	// BoardRetention is the age at which a terminal task is archived
	// automatically — that way the board cleans itself up instead of waiting for
	// a human at the "clean up" button. 0 → default.
	BoardRetention time.Duration
	ReadyTimeout   time.Duration
	Log            *slog.Logger
}

type Orchestrator struct {
	Options

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
	waiting  map[uuid.UUID]chan DaemonLink
	// baseCtx is the control plane's lifecycle, set by Run. Sessions hang off it
	// instead of context.Background(): on shutdown, running runs should be
	// aborted and not linger as orphaned goroutines with an open sandbox. Before
	// Run (or in tests that do not start Run) it is nil and the fallback kicks
	// in — see base().
	baseCtx context.Context
	// warm keeps parked sandbox sessions of warm-enabled agents alive while the
	// agent sleeps (link open, container keeps running). The next wake takes them
	// over instead of starting cold.
	warm map[uuid.UUID]*warmSession

	wikiSweepMu   sync.Mutex
	lastWikiSweep time.Time

	events *Broadcaster
}

func New(opts Options) *Orchestrator {
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 60 * time.Second
	}
	if opts.TickInterval == 0 {
		opts.TickInterval = 30 * time.Second
	}
	if opts.WikiMaintenanceInterval == 0 {
		opts.WikiMaintenanceInterval = 10 * time.Minute
	}
	if opts.BoardRetention == 0 {
		opts.BoardRetention = 24 * time.Hour
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Orchestrator{
		Options:  opts,
		sessions: map[uuid.UUID]*session{},
		waiting:  map[uuid.UUID]chan DaemonLink{},
		warm:     map[uuid.UUID]*warmSession{},
		events:   NewBroadcaster(),
	}
}

// warmIdleTTL: after this much idle time a parked warm sandbox is torn down
// after all, so it does not hold resources indefinitely.
const warmIdleTTL = 30 * time.Minute

// warmSession is a sandbox plus daemon link kept open between waking phases.
// While idle, a goroutine drains the daemon's heartbeats (otherwise the WS
// would be considered dead). The next wake cancels the drain and takes over.
type warmSession struct {
	link         DaemonLink
	sandbox      Sandbox
	lastUsed     time.Time
	cancel       context.CancelFunc // stops the drain
	done         chan struct{}      // closed once the drain has finished
	dead         atomic.Bool        // link died while idle
	teardownOnce sync.Once
}

func (ws *warmSession) teardown() {
	ws.teardownOnce.Do(func() {
		ws.link.Close()
		// Detached on purpose: cleanup often happens PRECISELY because the
		// associated context has expired. A derived one would already be dead
		// here and the container would stay up.
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = ws.sandbox.Stop(stopCtx)
	})
}

// warmLink wraps a daemon link for warm sessions. Reason: with coder/websocket
// a read whose context is cancelled closes the connection — so the idle drain
// (cancel for the handoff) would kill the warm link. A pump goroutine reads the
// inner link with a background context and distributes the messages over a
// channel; Receive(ctx) only reads from the channel, so a cancel never touches
// the actual connection.
type warmLink struct {
	inner     DaemonLink
	incoming  chan daemon.Message
	pumpErr   chan error
	stopPump  context.CancelFunc
	closeOnce sync.Once
}

func newWarmLink(inner DaemonLink) *warmLink {
	// Its own lifecycle instead of an inherited one: the link deliberately
	// outlives the run that created it (that is the point of "warm"). It is
	// terminated via stopPump in Close.
	ctx, cancel := context.WithCancel(context.Background())
	wl := &warmLink{
		inner:    inner,
		incoming: make(chan daemon.Message, 64),
		pumpErr:  make(chan error, 1),
		stopPump: cancel,
	}
	go func() {
		for {
			msg, err := inner.Receive(ctx)
			if err != nil {
				wl.pumpErr <- err
				close(wl.incoming)
				return
			}
			select {
			case wl.incoming <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return wl
}

func (wl *warmLink) Send(ctx context.Context, msg daemon.Message) error {
	return wl.inner.Send(ctx, msg)
}

func (wl *warmLink) Receive(ctx context.Context) (daemon.Message, error) {
	select {
	case msg, ok := <-wl.incoming:
		if !ok {
			select {
			case err := <-wl.pumpErr:
				return daemon.Message{}, err
			default:
				return daemon.Message{}, fmt.Errorf("daemon link closed")
			}
		}
		return msg, nil
	case <-ctx.Done():
		return daemon.Message{}, ctx.Err()
	}
}

func (wl *warmLink) Close() error {
	wl.closeOnce.Do(wl.stopPump)
	return wl.inner.Close()
}

// Events returns the SSE broadcaster for live updates of the admin UI.
func (o *Orchestrator) Events() *Broadcaster { return o.events }

type session struct {
	agent  agents.Agent
	cancel context.CancelFunc
	link   DaemonLink
	killed bool
}

// Run starts the dispatch loop: cheap, permanent, no LLM (spec/03).
// Wake sources: NOTIFY (event) and the periodic tick.
// base returns the lifecycle new sessions hang off. The caller holds o.mu.
// Without a running Run (tests, early call) it stays context.Background() —
// there is simply nothing to hang off then.
func (o *Orchestrator) base() context.Context {
	if o.baseCtx != nil {
		return o.baseCtx
	}
	return context.Background()
}

func (o *Orchestrator) Run(ctx context.Context) error {
	o.mu.Lock()
	o.baseCtx = ctx
	o.mu.Unlock()

	// Startup reconcile: orphaned in_progress tasks (sandbox gone with the last
	// process) back to open, otherwise they would hang forever after a
	// crash/deploy. Has to run before the first tick so they are picked up again
	// right away.
	if n, err := o.Backlog.RequeueOrphaned(ctx); err != nil {
		o.Log.Warn("startup reconcile failed", "err", err)
	} else if n > 0 {
		o.Log.Info("startup reconcile: orphaned tasks requeued", "count", n)
	}
	go o.listenLoop(ctx)
	go o.wikiMaintenanceLoop(ctx)
	go o.warmReaperLoop(ctx)
	go o.boardJanitorLoop(ctx)
	ticker := time.NewTicker(o.TickInterval)
	defer ticker.Stop()
	o.tick(ctx)
	for {
		select {
		case <-ctx.Done():
			o.shutdown()
			return ctx.Err()
		case <-ticker.C:
			o.tick(ctx)
		}
	}
}

// listenLoop listens for Postgres NOTIFY (wake events from the stores).
func (o *Orchestrator) listenLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := o.listenOnce(ctx); err != nil && ctx.Err() == nil {
			o.Log.Warn("listen/notify interrupted, reconnecting", "err", err)
			time.Sleep(2 * time.Second)
		}
	}
}

func (o *Orchestrator) listenOnce(ctx context.Context) error {
	conn, err := o.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN "+backlog.NotifyChannel); err != nil {
		return err
	}
	for {
		n, err := conn.Conn().WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if agentID, err := uuid.Parse(n.Payload); err == nil {
			o.EnsureRunning(agentID)
		}
	}
}

// tick is the periodic "what is due?" impulse: finds agents with open work and
// wakes them — a pure SQL decision, no model.
func (o *Orchestrator) tick(ctx context.Context) {
	o.fireHeartbeats(ctx)
	rows, err := o.Pool.Query(ctx, `SELECT DISTINCT a.id FROM agents a
		JOIN backlog_tasks t ON t.agent_id=a.id AND t.state='open'
		JOIN organizations org ON org.id=a.org_id
		WHERE NOT a.killed AND NOT org.fleet_killed`)
	if err != nil {
		o.Log.Warn("tick query", "err", err)
		return
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		o.EnsureRunning(id)
	}
}

// wikiMaintenanceLoop paces the wiki consolidation pass (spec/05): the
// lint/dedup pass is task-independent and does not run in the hot path of the
// done step, but bundled and throttled here.
func (o *Orchestrator) wikiMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(o.WikiMaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := o.ConsolidateWikis(ctx); err != nil {
				o.Log.Warn("wiki maintenance failed", "err", err)
			} else if n > 0 {
				o.Log.Info("wiki maintenance: duplicates merged", "count", n)
			}
		}
	}
}

// ConsolidateWikis consolidates the wikis of all agents whose pages have
// changed since the last pass (on the first run: all that have pages) and
// returns the total number of merges. Used by the maintenance ticker and for
// manual triggering (UI).
func (o *Orchestrator) ConsolidateWikis(ctx context.Context) (int, error) {
	o.wikiSweepMu.Lock()
	since := o.lastWikiSweep
	o.wikiSweepMu.Unlock()

	rows, err := o.Pool.Query(ctx, `SELECT DISTINCT agent_id FROM wiki_pages WHERE updated_at > $1`, since)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	total := 0
	for _, id := range ids {
		n, err := o.Memory.Consolidate(ctx, id)
		if err != nil {
			o.Log.Warn("wiki consolidation failed", "agent", id, "err", err)
			continue
		}
		total += n
	}
	o.wikiSweepMu.Lock()
	o.lastWikiSweep = time.Now()
	o.wikiSweepMu.Unlock()
	return total, nil
}

// fireHeartbeats creates a backlog task for due heartbeat entries
// (HEARTBEAT.md, materialized in agent_heartbeats). The interval form is due
// once the interval has elapsed since last_fired_at, the time-of-day form once
// per day from the configured time (server time). Kill switches apply as they
// do on wake. Dedup: as long as a non-terminal task with the same title from
// this heartbeat exists, no new one is created — the run still counts as
// "fired" so that no immediate second helping follows its completion and the
// regular schedule simply continues. The continuation of a run aborted at the
// turn limit (origin continuation:…) counts as well: it carries the same work
// forward and must not run alongside it.
func (o *Orchestrator) fireHeartbeats(ctx context.Context) {
	tx, err := o.Pool.Begin(ctx)
	if err != nil {
		o.Log.Warn("heartbeat tx", "err", err)
		return
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT h.agent_id, a.org_id, h.name, h.task_body, h.only_if, h.last_work_sig,
			EXISTS (SELECT 1 FROM backlog_tasks t
				WHERE t.agent_id=h.agent_id AND t.title=h.name
				  AND (t.origin='heartbeat' OR t.origin LIKE 'continuation:%')
				  AND t.state NOT IN ('done','failed','cancelled')) AS pending
		FROM agent_heartbeats h
		JOIN agents a ON a.id=h.agent_id
		JOIN organizations org ON org.id=a.org_id
		WHERE NOT a.killed AND NOT org.fleet_killed
		  AND ((h.every_seconds IS NOT NULL
		        AND h.last_fired_at + make_interval(secs => h.every_seconds) <= now())
		    OR (h.daily_at IS NOT NULL AND CURRENT_TIME >= h.daily_at
		        AND h.last_fired_at::date < CURRENT_DATE))
		FOR UPDATE OF h SKIP LOCKED`)
	if err != nil {
		o.Log.Warn("heartbeat query", "err", err)
		return
	}
	type due struct {
		agentID, orgID     uuid.UUID
		name, body, onlyIf string
		lastSig            string
		pending            bool
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.agentID, &d.orgID, &d.name, &d.body, &d.onlyIf, &d.lastSig, &d.pending); err != nil {
			rows.Close()
			o.Log.Warn("heartbeat scan", "err", err)
			return
		}
		dues = append(dues, d)
	}
	rows.Close()
	if rows.Err() != nil || len(dues) == 0 {
		return
	}
	for _, d := range dues {
		if _, err := tx.Exec(ctx,
			"UPDATE agent_heartbeats SET last_fired_at=now() WHERE agent_id=$1 AND name=$2",
			d.agentID, d.name); err != nil {
			o.Log.Warn("advance heartbeat", "agent", d.agentID, "name", d.name, "err", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		o.Log.Warn("heartbeat commit", "err", err)
		return
	}
	// Create tasks only after the commit — Create fires NOTIFY and wakes the
	// agent. If a Create fails, exactly one run is skipped (best effort).
	// The nur-wenn: check likewise runs after the commit (network I/O does not
	// belong in the transaction); last_fired_at has been advanced by then — a
	// skipped run counts as having run, the schedule keeps polling regularly.
	for _, d := range dues {
		if d.pending {
			o.Log.Info("heartbeat skipped: task still open", "agent", d.agentID, "name", d.name)
			continue
		}
		if d.onlyIf != "" {
			has, sig := o.heartbeatHasWork(ctx, d.agentID, d.orgID, d.onlyIf)
			if has && sig != "" && sig != d.lastSig {
				o.rememberWorkSignature(ctx, d.agentID, d.name, sig)
			} else if !has && d.lastSig != "" {
				// Backlog worked off — reset the signature so the same state may
				// wake the agent again later.
				o.rememberWorkSignature(ctx, d.agentID, d.name, "")
			}
			if !has {
				o.Log.Info("heartbeat skipped: no work", "agent", d.agentID, "name", d.name, "system", d.onlyIf)
				continue
			}
			// Unchanged backlog: the agent already knows this state and left it
			// that way deliberately. Only a CHANGE wakes it again — otherwise it
			// would have to comment just to switch off its own alarm clock.
			if sig != "" && sig == d.lastSig {
				o.Log.Info("heartbeat skipped: work backlog unchanged",
					"agent", d.agentID, "name", d.name, "system", d.onlyIf)
				continue
			}
		}
		if _, err := o.Backlog.Create(ctx, d.orgID, d.agentID, d.name, d.body, "heartbeat", 0); err != nil {
			o.Log.Warn("create heartbeat task", "agent", d.agentID, "name", d.name, "err", err)
		}
	}
}

// heartbeatHasWork checks the nur-wenn: condition of a due heartbeat: the
// target-system plugin reports via target.WorkChecker whether there is work.
// The condition value is "<system>" or "<system>:<kind>" — the latter gates a
// single kind of work (e.g. gitlab:mr for the review loop, gitlab:issues for
// issue triage) via target.KindWorkChecker, so that two heartbeats of the same
// system fire separately instead of through one shared boolean.
// Secrets and plugin lookup go by the system name (before the ":").
// The control plane resolves the secrets itself — the credential does not leave
// it. Fail-open: if the condition cannot be checked (plugin without
// WorkChecker, missing secrets, connection error), the heartbeat fires as
// usual — a broken condition must not leave work lying around.
// Returns: (work present, signature of the backlog). The signature is empty if
// the plugin does not supply one — the heartbeat then fires at every level, as
// it did before.
func (o *Orchestrator) heartbeatHasWork(ctx context.Context, agentID, orgID uuid.UUID, condition string) (bool, string) {
	system, kind, _ := strings.Cut(condition, ":")
	sys, ok := target.Get(system)
	if !ok {
		o.Log.Warn("nur-wenn: unknown target system — firing anyway", "system", system)
		return true, ""
	}
	checker, ok := sys.(target.WorkChecker)
	if !ok {
		o.Log.Warn("nur-wenn: target system cannot check for work up front — firing anyway", "system", system)
		return true, ""
	}
	var cred target.Credential
	if d, _ := target.Describe(system); !d.NoCredentials {
		token, err := o.Secrets.Resolve(ctx, orgID, agentID, system+"_token")
		if err != nil {
			o.Log.Warn("nur-wenn: secret missing — firing anyway", "system", system, "err", err)
			return true, ""
		}
		baseURL, err := o.Secrets.Resolve(ctx, orgID, agentID, system+"_url")
		if err != nil {
			o.Log.Warn("nur-wenn: secret missing — firing anyway", "system", system, "err", err)
			return true, ""
		}
		cred = target.Credential{BaseURL: baseURL, Token: token}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	// The work check is a target-system request made from the control plane —
	// with an agent sink so it is attributable in the request log instead of
	// anonymous.
	if o.ReqLog != nil {
		cctx = reqlog.WithSink(cctx, o.ReqLog.Sink(&orgID, &agentID, nil))
	}
	var (
		has bool
		sig string
		err error
	)
	switch c := checker.(type) {
	case target.SignedWorkChecker:
		// Additionally returns the signature of the backlog — with it the
		// dispatch suppresses a wake on a state that has already been seen.
		has, sig, err = c.HasWorkSigned(cctx, cred, kind)
	case target.KindWorkChecker:
		if kind == "" {
			has, err = c.HasWork(cctx, cred)
		} else {
			has, err = c.HasWorkKind(cctx, cred, kind)
		}
	default:
		if kind != "" {
			o.Log.Warn("nur-wenn: target system knows no sub-scope — checking everything", "system", system, "kind", kind)
		}
		has, err = checker.HasWork(cctx, cred)
	}
	if err != nil {
		o.Log.Warn("nur-wenn: check failed — firing anyway", "system", system, "kind", kind, "err", err)
		return true, ""
	}
	return has, sig
}

// rememberWorkSignature advances the signature that was last fired on. Best
// effort: if it fails, the next tick wakes the agent at most once too often —
// which is more harmless than a missed run.
func (o *Orchestrator) rememberWorkSignature(ctx context.Context, agentID uuid.UUID, name, sig string) {
	if _, err := o.Pool.Exec(ctx,
		"UPDATE agent_heartbeats SET last_work_sig=$3 WHERE agent_id=$1 AND name=$2",
		agentID, name, sig); err != nil {
		o.Log.Warn("advance heartbeat signature", "agent", agentID, "name", name, "err", err)
	}
}

// EnsureRunning starts an agent session if none is running (idempotent).
func (o *Orchestrator) EnsureRunning(agentID uuid.UUID) {
	o.mu.Lock()
	if _, active := o.sessions[agentID]; active {
		o.mu.Unlock()
		return
	}
	// Hung off the control plane's lifecycle, not off Background: a shutdown
	// thereby also terminates sessions that came into being this way.
	ctx, cancel := context.WithCancel(o.base())
	s := &session{cancel: cancel}
	o.sessions[agentID] = s
	o.mu.Unlock()

	go func() {
		defer func() {
			o.mu.Lock()
			delete(o.sessions, agentID)
			o.mu.Unlock()
			cancel()
		}()
		if err := o.runAgent(ctx, agentID, s); err != nil && !errors.Is(err, context.Canceled) {
			o.Log.Error("agent-session", "agent", agentID, "err", err)
		}
	}()
}

// AttachDaemon hands an authenticated daemon connection to the waiting session.
// Does not block: without a waiting session it is rejected.
func (o *Orchestrator) AttachDaemon(agentID uuid.UUID, link DaemonLink) error {
	o.mu.Lock()
	ch := o.waiting[agentID]
	o.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("no session is waiting for agent %s", agentID)
	}
	select {
	case ch <- link:
		return nil
	default:
		return fmt.Errorf("agent %s already has a connection", agentID)
	}
}

func (o *Orchestrator) setStatus(ctx context.Context, agent agents.Agent, taskID *uuid.UUID, status string) {
	if err := o.Registry.SetStatus(ctx, agent.ID, status); err != nil {
		o.Log.Warn("set status", "agent", agent.ID, "err", err)
		return
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, taskID, observability.KindLifecycle,
		map[string]string{"status": status})
	o.events.Publish(Event{Type: "agent_status", AgentID: agent.ID.String(), Data: map[string]string{"status": status}})
}

// runAgent is one complete waking phase: sandbox up, work through the tasks
// serially, sandbox down. sleeping → triggered → (triage → working)* → sleeping.
func (o *Orchestrator) runAgent(ctx context.Context, agentID uuid.UUID, s *session) error {
	agent, err := o.Registry.Get(ctx, agentID)
	if err != nil {
		return err
	}
	s.agent = agent
	if agent.Killed {
		return nil
	}
	if fleetKilled, err := o.Registry.FleetKilled(ctx, agent.OrgID); err != nil || fleetKilled {
		return err
	}
	hasOpen, err := o.Backlog.HasOpen(ctx, agentID)
	if err != nil || !hasOpen {
		return err
	}

	o.setStatus(ctx, agent, nil, agents.StatusTriggered)

	// Wake: take over a warm sandbox, otherwise start cold and wait for ready.
	link, sandbox, err := o.acquireSandbox(ctx, agent)
	if err != nil {
		o.setStatus(ctx, agent, nil, agents.StatusSleeping)
		return fmt.Errorf("wake: %w", err)
	}
	s.link = link
	defer func() {
		final := agents.StatusSleeping
		if s.killed {
			final = agents.StatusKilled
		}
		// Warm and cleanly asleep: keep sandbox + link alive (drained while
		// idle). Otherwise — or on kill/abort — tear the compute down.
		if agent.WarmSandbox && !s.killed && ctx.Err() == nil {
			o.parkWarm(agent.ID, link, sandbox)
		} else {
			link.Close()
			// Detached here too: on kill or abort ctx has already expired — the
			// sandbox still has to go.
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			sandbox.Stop(stopCtx)
			cancel()
		}
		o.setStatus(context.WithoutCancel(ctx), agent, nil, final)
	}()

	// Broker the runtime LLM key proactively (never permanently in the sandbox).
	o.pushAnthropicKey(ctx, agent, link)

	for ctx.Err() == nil {
		if killed, _ := o.isKilled(ctx, agent); killed {
			s.killed = true
			_ = o.sendMsg(ctx, link, daemon.TypeKill, map[string]string{})
			return nil
		}
		task, err := o.Backlog.ClaimNext(ctx, agentID)
		if errors.Is(err, backlog.ErrNotFound) {
			// Warm: NO sleep — otherwise coveyd would clear away dev servers and
			// browsers and exit. The daemon keeps idling in its receive loop.
			if !agent.WarmSandbox {
				_ = o.sendMsg(ctx, link, daemon.TypeSleep, map[string]string{})
			}
			return nil
		}
		if err != nil {
			return err
		}
		if err := o.processTask(ctx, agent, link, task, s); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, errBudgetExceeded) {
				return nil // task has already been reopened, agent is paused
			}
			_, _ = o.Backlog.Complete(context.WithoutCancel(ctx), task.ID, backlog.StateFailed, "", err.Error())
			o.publishTask(task.ID, agent.ID)
			return err
		}
	}
	return ctx.Err()
}

// wake starts the sandbox and waits for the daemon's ready message.
func (o *Orchestrator) wake(ctx context.Context, agent agents.Agent) (DaemonLink, Sandbox, error) {
	tok, err := o.Identity.IssueAgentToken(ctx, agent.ID,
		identity.Scope{Audience: "daemon"}, o.DaemonTokenTTL)
	if err != nil {
		return nil, nil, err
	}

	ch := make(chan DaemonLink, 1)
	o.mu.Lock()
	o.waiting[agent.ID] = ch
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.waiting, agent.ID)
		o.mu.Unlock()
	}()

	// Per-sandbox egress token: the proxy identifies the agent by it. Rotated on
	// every wake; only the hash is stored.
	egressToken := ""
	if o.Egress != nil {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err == nil {
			egressToken = hex.EncodeToString(buf)
			if err := o.Egress.SetAgentToken(ctx, agent.ID, egress.HashToken(egressToken)); err != nil {
				o.Log.Warn("setting the egress token failed", "agent", agent.ID, "err", err)
				egressToken = ""
			}
		}
	}

	sandbox, err := o.Provider.Start(ctx, SandboxSpec{
		AgentID:     agent.ID,
		EgressToken: egressToken,
		Env: map[string]string{
			"COVEY_WS_URL":       o.PublicWSURL,
			"COVEY_DAEMON_TOKEN": tok.Value,
			"COVEY_AGENT_ID":     agent.ID.String(),
		},
	})
	if err != nil {
		return nil, nil, err
	}

	select {
	case link := <-ch:
		// The first message has to be ready.
		readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		msg, err := link.Receive(readyCtx)
		if err != nil || msg.Type != daemon.TypeReady {
			link.Close()
			sandbox.Stop(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("daemon not ready: %v (%s)", err, msg.Type)
		}
		return link, sandbox, nil
	case <-time.After(o.ReadyTimeout):
		sandbox.Stop(context.WithoutCancel(ctx))
		// The address belongs in the message: the most common reason for this
		// timeout is that the sandbox cannot reach the control plane at exactly
		// this URL — wrong COVEY_PUBLIC_URL, missing egress allowance, a proxy in
		// between. Without it one is searching in the dark.
		return nil, nil, fmt.Errorf(
			"daemon did not connect (timeout %s) — the sandbox should reach %s; "+
				"check: COVEY_PUBLIC_URL has to be reachable from the sandbox",
			o.ReadyTimeout, o.PublicWSURL)
	case <-ctx.Done():
		sandbox.Stop(context.WithoutCancel(ctx))
		return nil, nil, ctx.Err()
	}
}

// acquireSandbox takes over a parked sandbox for warm-enabled agents (no cold
// start), otherwise it brings up a fresh one (wake).
func (o *Orchestrator) acquireSandbox(ctx context.Context, agent agents.Agent) (DaemonLink, Sandbox, error) {
	if agent.WarmSandbox {
		if ws := o.takeWarm(agent.ID); ws != nil {
			o.Log.Info("took over warm sandbox", "agent", agent.ID)
			return ws.link, ws.sandbox, nil
		}
	}
	link, sandbox, err := o.wake(ctx, agent)
	if err != nil {
		return nil, nil, err
	}
	// Warm: wrap the fresh link in the pump shell so the idle drain can take it
	// over safely later on.
	if agent.WarmSandbox {
		link = newWarmLink(link)
	}
	return link, sandbox, nil
}

// parkWarm keeps link + sandbox open after falling asleep and drains the
// daemon heartbeats in the background until the next wake takes over or the
// reaper clears it away. If the link dies while idle, the sandbox is torn down
// immediately.
func (o *Orchestrator) parkWarm(agentID uuid.UUID, link DaemonLink, sandbox Sandbox) {
	// The drain lives as long as the sandbox is parked — longer than the run
	// that hands it over. It is terminated by the reaper (idle TTL), by the next
	// wake, or on shutdown by teardownAllWarm.
	drainCtx, cancel := context.WithCancel(context.Background())
	ws := &warmSession{link: link, sandbox: sandbox, lastUsed: time.Now(), cancel: cancel, done: make(chan struct{})}
	o.mu.Lock()
	// Make way for a possibly still-registered predecessor session of the same agent.
	if old := o.warm[agentID]; old != nil {
		old.cancel()
	}
	o.warm[agentID] = ws
	o.mu.Unlock()

	go func() {
		defer close(ws.done)
		for {
			if _, err := link.Receive(drainCtx); err != nil {
				if drainCtx.Err() != nil {
					return // healthy handoff (takeWarm/reaper) — do not touch the link
				}
				// Link died while idle (container crash or similar): clean up.
				ws.dead.Store(true)
				o.mu.Lock()
				if o.warm[agentID] == ws {
					delete(o.warm, agentID)
				}
				o.mu.Unlock()
				ws.teardown()
				o.Log.Warn("lost warm sandbox while idle", "agent", agentID, "err", err)
				return
			}
			// Discard the heartbeat/idle message — no task is running.
		}
	}()
}

// takeWarm pulls a parked session out of the cache, stops its drain and returns
// it. If the link has died in the meantime it returns nil (→ cold start).
func (o *Orchestrator) takeWarm(agentID uuid.UUID) *warmSession {
	o.mu.Lock()
	ws := o.warm[agentID]
	if ws != nil {
		delete(o.warm, agentID)
	}
	o.mu.Unlock()
	if ws == nil {
		return nil
	}
	ws.cancel()
	<-ws.done
	if ws.dead.Load() {
		return nil
	}
	return ws
}

// evictWarm tears a parked warm sandbox down immediately (kill/disable).
// kill=true sends TypeKill beforehand (immediate end), otherwise TypeSleep
// (clean). No-op if the agent has no parked session.
func (o *Orchestrator) evictWarm(ctx context.Context, agentID uuid.UUID, kill bool) {
	ws := o.takeWarm(agentID)
	if ws == nil {
		return
	}
	msg := daemon.TypeSleep
	if kill {
		msg = daemon.TypeKill
	}
	_ = o.sendMsg(ctx, ws.link, msg, map[string]string{})
	ws.teardown()
}

// boardJanitorLoop keeps the backlog board on the working states that really
// exist: terminal tasks are archived after BoardRetention (not deleted — they
// stay in the archive), and the agent columns that thereby become empty
// disappear.
//
// Why the platform and not the agent: cleaning up is hygiene, not a decision.
// An agent told to do it in its prompt forgets it under load or does it at the
// wrong time; here it happens the same way for every installation, without
// anyone having to think about it. The "clean up" button in the UI remains for
// "and right now".
func (o *Orchestrator) boardJanitorLoop(ctx context.Context) {
	if o.BoardRetention < 0 {
		o.Log.Info("board cleanup is switched off (COVEY_BOARD_RETENTION is negative)")
		return
	}
	// Hourly is enough: the retention is measured in hours, not minutes.
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	o.sweepBoards(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.sweepBoards(ctx)
		}
	}
}

func (o *Orchestrator) sweepBoards(ctx context.Context) {
	n, err := o.Backlog.ArchiveTerminalOlderThan(ctx, o.BoardRetention)
	if err != nil {
		o.Log.Warn("board cleanup failed", "err", err)
		return
	}
	if n > 0 {
		o.Log.Info("board cleanup: terminal tasks archived", "count", n, "older_than", o.BoardRetention)
	}
}

// warmReaperLoop tears down parked warm sandboxes that have been idle too long.
func (o *Orchestrator) warmReaperLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			// A warm sandbox is a RUNNING container belonging to a sleeping
			// agent. Without this teardown it would be left behind on
			// shutdown: the next start only clears it away once the same agent
			// is woken again (the provider then deletes the container of the
			// same name) — an agent that never gets its turn again would hold
			// its container forever.
			o.teardownAllWarm()
			return
		case <-t.C:
			o.reapIdleWarm(ctx)
		}
	}
}

// teardownAllWarm tears down all parked sandboxes on shutdown.
func (o *Orchestrator) teardownAllWarm() {
	o.mu.Lock()
	all := make([]*warmSession, 0, len(o.warm))
	for id, ws := range o.warm {
		all = append(all, ws)
		delete(o.warm, id)
	}
	o.mu.Unlock()
	if len(all) == 0 {
		return
	}
	// Its own short context: the loop's has just expired — which is precisely
	// why we are cleaning up here. An expired context would make every sleep
	// and every Stop fail immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, ws := range all {
		ws.cancel()
		<-ws.done
		if !ws.dead.Load() {
			// Let it fall asleep cleanly so coveyd clears away its child processes.
			_ = o.sendMsg(ctx, ws.link, daemon.TypeSleep, map[string]string{})
		}
		ws.teardown()
	}
	o.Log.Info("tore down warm sandboxes on shutdown", "count", len(all))
}

func (o *Orchestrator) reapIdleWarm(ctx context.Context) {
	cutoff := time.Now().Add(-warmIdleTTL)
	o.mu.Lock()
	var expired []*warmSession
	for id, ws := range o.warm {
		if ws.lastUsed.Before(cutoff) {
			expired = append(expired, ws)
			delete(o.warm, id)
		}
	}
	o.mu.Unlock()
	for _, ws := range expired {
		ws.cancel()
		<-ws.done
		if ws.dead.Load() {
			continue
		}
		// Let it fall asleep cleanly (coveyd clears away dev servers and exits),
		// then tear the compute down.
		_ = o.sendMsg(ctx, ws.link, daemon.TypeSleep, map[string]string{})
		ws.teardown()
		o.Log.Info("tore down warm sandbox after idle", "ttl", warmIdleTTL)
	}
}

func (o *Orchestrator) sendMsg(ctx context.Context, link DaemonLink, msgType string, payload any) error {
	msg, err := daemon.Encode(msgType, payload)
	if err != nil {
		return err
	}
	return link.Send(ctx, msg)
}

func (o *Orchestrator) pushAnthropicKey(ctx context.Context, agent agents.Agent, link DaemonLink) {
	// The secret's name determines the credential type and hence the runtime
	// env: anthropic_api_key → ANTHROPIC_API_KEY, claude_code_oauth_token
	// (subscription, via `claude setup-token`) → CLAUDE_CODE_OAUTH_TOKEN. Do not
	// guess from the token prefix — the name is the binding intent.
	key, err := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, "anthropic_api_key")
	envVar := "ANTHROPIC_API_KEY"
	if err != nil {
		key, err = o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, "claude_code_oauth_token")
		envVar = "CLAUDE_CODE_OAUTH_TOKEN"
	}
	if err != nil {
		return // no credential stored — the runtime reports this as a task error
	}
	key = strings.TrimSpace(key) // catch copy-and-paste whitespace/newlines
	_ = o.sendMsg(ctx, link, daemon.TypeInjectCredentials, daemon.InjectCredentials{
		System: "anthropic", Granted: true, Token: key, EnvVar: envVar,
		TTLSecs: int(o.DaemonTokenTTL.Seconds()),
	})
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": "anthropic", "granted": true, "proactive": true})
}

func (o *Orchestrator) isKilled(ctx context.Context, agent agents.Agent) (bool, error) {
	var killed, fleet bool
	err := o.Pool.QueryRow(ctx, `SELECT a.killed, org.fleet_killed FROM agents a
		JOIN organizations org ON org.id=a.org_id WHERE a.id=$1`, agent.ID).Scan(&killed, &fleet)
	return killed || fleet, err
}

// teamSection loads the organization's employee profiles and builds the team
// section of the system prompt from them. Errors are not fatal — the agent then
// works without a staff directory. The platform identifiers are generic (map
// system → identifier); display labels come from the organization's
// target-system plugins, unknown systems keep their key. supervisorID
// (agents.supervisor_id, nil = none) marks the supervisor — the recipient of
// merge requests and escalations.
func (o *Orchestrator) teamSection(ctx context.Context, orgID uuid.UUID, supervisorID *uuid.UUID) string {
	labels := map[string]string{}
	if o.Targets != nil {
		if plugins, err := o.Targets.List(ctx, orgID); err == nil {
			for _, p := range plugins {
				if p.Label != "" {
					labels[p.Name] = p.Label
				}
			}
		}
	}
	// Org-wide configured profile fields: key → label, in definition order —
	// only defined fields appear in the directory.
	type fieldDef struct{ key, label string }
	var fieldDefs []fieldDef
	if fr, err := o.Pool.Query(ctx, `SELECT key, label FROM profile_fields
		WHERE org_id=$1 ORDER BY created_at`, orgID); err == nil {
		for fr.Next() {
			var d fieldDef
			if fr.Scan(&d.key, &d.label) == nil {
				fieldDefs = append(fieldDefs, d)
			}
		}
		fr.Close()
	}
	rows, err := o.Pool.Query(ctx, `SELECT id, display_name, job_title, email, identities, responsibilities, custom
		FROM humans WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var members []agents.TeamMember
	for rows.Next() {
		var m agents.TeamMember
		var id uuid.UUID
		var ids, custom map[string]string
		if err := rows.Scan(&id, &m.Name, &m.JobTitle, &m.Email, &ids, &m.Responsibilities, &custom); err != nil {
			return ""
		}
		m.Supervisor = supervisorID != nil && *supervisorID == id
		for _, system := range slices.Sorted(maps.Keys(ids)) {
			label := labels[system]
			if label == "" {
				label = system
			}
			m.Identities = append(m.Identities, agents.TeamIdentity{Label: label, Value: ids[system]})
		}
		for _, d := range fieldDefs {
			if v := custom[d.key]; v != "" {
				m.Fields = append(m.Fields, agents.TeamIdentity{Label: d.label, Value: v})
			}
		}
		members = append(members, m)
	}
	return agents.TeamSection(members)
}

// agentTeamSection builds the directory of AI colleagues for `self`'s prompt:
// all other (non-killed) agents of the organization, their GitLab/target-system
// identifiers, responsibilities and department. Agents from the same department
// as `self` are marked as its own team — that way a developer agent finds the
// QA agent of its team to hand the merge request over for review. Runs at
// dispatch time, like teamSection.
func (o *Orchestrator) agentTeamSection(ctx context.Context, self agents.Agent) string {
	labels := map[string]string{}
	if o.Targets != nil {
		if plugins, err := o.Targets.List(ctx, self.OrgID); err == nil {
			for _, p := range plugins {
				if p.Label != "" {
					labels[p.Name] = p.Label
				}
			}
		}
	}
	deptNames := map[uuid.UUID]string{}
	if dr, err := o.Pool.Query(ctx, `SELECT id, name FROM departments WHERE org_id=$1`, self.OrgID); err == nil {
		for dr.Next() {
			var id uuid.UUID
			var name string
			if dr.Scan(&id, &name) == nil {
				deptNames[id] = name
			}
		}
		dr.Close()
	}
	rows, err := o.Pool.Query(ctx, `SELECT id, display_name, job_title, identities, responsibilities, department_id
		FROM agents WHERE org_id=$1 AND id<>$2 AND NOT killed ORDER BY created_at`, self.OrgID, self.ID)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var colleagues []agents.AgentColleague
	for rows.Next() {
		var c agents.AgentColleague
		var id uuid.UUID
		var deptID *uuid.UUID
		var ids map[string]string
		if err := rows.Scan(&id, &c.Name, &c.JobTitle, &ids, &c.Responsibilities, &deptID); err != nil {
			return ""
		}
		if deptID != nil {
			c.Department = deptNames[*deptID]
			c.SameTeam = self.DepartmentID != nil && *self.DepartmentID == *deptID
		}
		c.Supervisor = self.SupervisorID != nil && *self.SupervisorID == id
		for _, system := range slices.Sorted(maps.Keys(ids)) {
			label := labels[system]
			if label == "" {
				label = system
			}
			c.Identities = append(c.Identities, agents.TeamIdentity{Label: label, Value: ids[system]})
		}
		colleagues = append(colleagues, c)
	}
	return agents.TeamAgentsSection(colleagues)
}

func (o *Orchestrator) publishTask(taskID, agentID uuid.UUID) {
	o.events.Publish(Event{Type: "task", AgentID: agentID.String(), Data: map[string]string{"task_id": taskID.String()}})
}

// processTask drives a task through triage → working → done/blocked/failed.
func (o *Orchestrator) processTask(ctx context.Context, agent agents.Agent, link DaemonLink, task backlog.Task, s *session) error {
	taskID := task.ID
	o.setStatus(ctx, agent, &taskID, agents.StatusTriage)
	o.publishTask(taskID, agent.ID)

	// Triage: check the wiki (spec/05) and compile the config (M2). Relevant
	// pages (vector hits) plus the compact index of the whole wiki.
	memCtx := ""
	if entries, err := o.Memory.Query(ctx, agent.ID, task.Title+" "+task.Body, 5); err == nil {
		memCtx = memory.FormatForPrompt(entries)
	}
	if idx, err := o.Memory.List(ctx, agent.ID, 40); err == nil {
		if section := memory.FormatIndexForPrompt(idx); section != "" {
			if memCtx != "" {
				memCtx += "\n"
			}
			memCtx += section
		}
	}
	cfg, err := o.Registry.CurrentConfig(ctx, agent.ID)
	if err != nil && !errors.Is(err, agents.ErrNotFound) {
		return err
	}
	// Recompile the prompt from the config files at dispatch time instead of
	// taking the compiled_prompt frozen at save time. The platform's share
	// (agents.ProtocolInstructions: completion protocol, meta actions, stage
	// rules) is code, not config — it belongs to the binary, not to the config
	// version. Otherwise every existing agent config would have to be saved
	// again by hand after each deploy for the agent to even learn about new
	// actions; a production agent would run for years with the platform contract
	// of its last config edit.
	//
	// The stored compiled_prompt is kept as a snapshot for audit and display —
	// the source of truth for the run are the files. Target-system docs and the
	// team directory below follow the same logic.
	compiled := agents.CompilePrompt(cfg.Files)
	// Append the target-system docs at dispatch time — they reflect the
	// organization's currently enabled plugins (including manifest uploads), not
	// the state at the time the config was compiled.
	if o.Targets != nil {
		if docs, err := o.Targets.EnabledDocsForAgent(ctx, agent.OrgID, agent.ID); err == nil {
			if section := agents.TargetDocs(docs); section != "" {
				compiled += "\n\n" + section
			}
		}
	}
	// The team directory likewise at dispatch time: the employee profiles
	// (responsibilities, GitLab usernames) tell the agent whom it hands things
	// over to in target systems — e.g. assigning an issue for testing.
	if section := o.teamSection(ctx, agent.OrgID, agent.SupervisorID); section != "" {
		compiled += "\n\n" + section
	}
	// Plus the AI colleagues: the organization's other agents, so an agent can
	// hand work over to the right colleague (e.g. the developer their MR to the
	// QA agent of their team) — the department marks its own team.
	if section := o.agentTeamSection(ctx, agent); section != "" {
		compiled += "\n\n" + section
	}

	maxTurns := agent.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	if err := o.sendMsg(ctx, link, daemon.TypeInjectConfig, daemon.InjectConfig{
		SystemPrompt: compiled,
		Runtime:      agent.Runtime,
		Model:        agent.Model,
		AllowedTools: daemon.DefaultAllowedTools,
		MaxTurns:     maxTurns,
	}); err != nil {
		return err
	}

	o.setStatus(ctx, agent, &taskID, agents.StatusWorking)

	assign := daemon.AssignTask{
		TaskID:        taskID.String(),
		Title:         task.Title,
		Body:          task.Body,
		Priority:      task.Priority,
		MemoryContext: memCtx,
	}
	if task.RuntimeSessionID != nil && task.ResumeInput != nil {
		// blocked→working: resumption through the native runtime session (spec/12).
		assign.ResumeSessionID = *task.RuntimeSessionID
		assign.ResumeInput = *task.ResumeInput
	}
	if err := o.sendMsg(ctx, link, daemon.TypeAssignTask, assign); err != nil {
		return err
	}

	// Message loop until blocked/task_done.
	for {
		msg, err := link.Receive(ctx)
		if err != nil {
			return fmt.Errorf("daemon connection: %w", err)
		}
		done, err := o.handleDaemonMessage(ctx, agent, link, taskID, msg, s)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
}

// storeActionArtifact pulls a base64 image out of an action event, stores it as
// a blob and replaces it in the payload with the reference (screenshot=<blob-id>)
// — that keeps the bytes out of the recording timeline's JSONB (spec/06). On any
// error the payload stays unchanged (fail-open for the recording).
func (o *Orchestrator) storeActionArtifact(ctx context.Context, agent agents.Agent, taskID uuid.UUID, payload json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return payload
	}
	b64, ok := m["image_b64"].(string)
	if !ok || b64 == "" {
		return payload
	}
	// Recording depth (spec/06): store screenshots only at 'full'. Otherwise
	// discard the image — the action itself stays in the recording. Enforcement
	// on the control plane side is the last instance (fail-closed: when in
	// doubt, do not store).
	if lvl, err := o.Obs.EffectiveRecordingLevel(ctx, agent.ID); err != nil || lvl != observability.LevelFull {
		delete(m, "image_b64")
		delete(m, "image_mime")
		if out, mErr := json.Marshal(m); mErr == nil {
			return out
		}
		return payload
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return payload
	}
	mime, _ := m["image_mime"].(string)
	if mime == "" {
		mime = "image/png"
	}
	id, err := o.Obs.PutBlob(ctx, agent.OrgID, agent.ID, &taskID, mime, data)
	if err != nil {
		return payload
	}
	delete(m, "image_b64")
	delete(m, "image_mime")
	m["screenshot"] = id.String()
	if out, err := json.Marshal(m); err == nil {
		return out
	}
	return payload
}

// recordHTTP puts an HTTP request reported from the sandbox into the request
// log — with the context only the control plane has (org, agent, task). Without
// a configured log it is a no-op.
func (o *Orchestrator) recordHTTP(ctx context.Context, agent agents.Agent, taskID uuid.UUID, payload []byte) {
	if o.ReqLog == nil {
		return
	}
	var e reqlog.Entry
	if err := json.Unmarshal(payload, &e); err != nil {
		return
	}
	orgID, agentID, tid := agent.OrgID, agent.ID, taskID
	o.ReqLog.Enqueue(reqlogstore.Record{Entry: e, OrgID: &orgID, AgentID: &agentID, TaskID: &tid})
}

// handleDaemonMessage processes one daemon message; true = task finished.
func (o *Orchestrator) handleDaemonMessage(ctx context.Context, agent agents.Agent, link DaemonLink, taskID uuid.UUID, msg daemon.Message, s *session) (bool, error) {
	switch msg.Type {
	case daemon.TypeHeartbeat:
		return false, nil

	case daemon.TypeEvent:
		ev, err := daemon.DecodePayload[daemon.Event](msg)
		if err != nil {
			return false, nil
		}
		// HTTP requests of the plugins belong in the request log, not in the
		// recording timeline — own retention, own view (spec/06).
		if ev.Kind == daemon.EventKindHTTP {
			o.recordHTTP(ctx, agent, taskID, ev.Payload)
			return false, nil
		}
		kind := observability.KindRuntime
		payload := json.RawMessage(ev.Payload)
		if ev.Kind == daemon.EventKindAction {
			kind = observability.KindAction
			payload = o.storeActionArtifact(ctx, agent, taskID, payload)
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, kind, payload)
		o.events.Publish(Event{Type: "recording", AgentID: agent.ID.String(),
			Data: map[string]any{"task_id": taskID.String(), "kind": kind}})
		return false, nil

	case daemon.TypeCost:
		c, err := daemon.DecodePayload[daemon.Cost](msg)
		if err != nil {
			return false, nil
		}
		_ = o.Obs.AddCost(ctx, agent.ID, &taskID, c.USD, c.InputTokens, c.OutputTokens, c.Model)
		return false, o.enforceBudget(ctx, agent, link, taskID, s)

	case daemon.TypeRequestCredential:
		req, err := daemon.DecodePayload[daemon.RequestCredential](msg)
		if err != nil {
			return false, nil
		}
		resp := o.brokerCredential(ctx, agent, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectCredentials, resp)

	case daemon.TypeRequestApproval:
		req, err := daemon.DecodePayload[daemon.RequestApproval](msg)
		if err != nil {
			return false, nil
		}
		resp := o.decideAction(ctx, agent, taskID, req)
		return false, o.sendMsg(ctx, link, daemon.TypeApprovalDecision, resp)

	case daemon.TypeRequestTarget:
		req, err := daemon.DecodePayload[daemon.RequestTarget](msg)
		if err != nil {
			return false, nil
		}
		resp := o.brokerTarget(ctx, agent, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectTarget, resp)

	case daemon.TypeRequestOrgChart:
		req, err := daemon.DecodePayload[daemon.RequestOrgChart](msg)
		if err != nil {
			return false, nil
		}
		chart := o.orgChartPayload(ctx, agent.OrgID, agent.ID)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectOrgChart,
			daemon.InjectOrgChart{RequestID: req.RequestID, Chart: chart})

	case daemon.TypeRequestWiki:
		req, err := daemon.DecodePayload[daemon.RequestWiki](msg)
		if err != nil {
			return false, nil
		}
		resp := o.brokerWiki(ctx, agent, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectWiki, resp)

	case daemon.TypeRequestSkills:
		req, err := daemon.DecodePayload[daemon.RequestSkills](msg)
		if err != nil {
			return false, nil
		}
		return false, o.sendMsg(ctx, link, daemon.TypeInjectSkills, o.skillsFor(ctx, agent, req))

	case daemon.TypeRequestCreateTask:
		req, err := daemon.DecodePayload[daemon.RequestCreateTask](msg)
		if err != nil {
			return false, nil
		}
		resp := o.createAgentTask(ctx, agent, taskID, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectCreateTask, resp)

	case daemon.TypeBlocked:
		b, err := daemon.DecodePayload[daemon.Blocked](msg)
		if err != nil {
			return true, err
		}
		if _, err := o.Backlog.Block(ctx, taskID, b.CorrelationKey, b.SessionID, b.Question); err != nil {
			return true, err
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
			map[string]string{"status": "blocked", "correlation_key": b.CorrelationKey, "question": b.Question})
		o.publishTask(taskID, agent.ID)
		return true, nil

	case daemon.TypeTaskDone:
		d, err := daemon.DecodePayload[daemon.TaskDone](msg)
		if err != nil {
			return true, err
		}
		if d.Status == statusIncomplete {
			return true, o.handleIncomplete(ctx, agent, taskID, d)
		}
		state := backlog.StateDone
		if d.Status == "failed" {
			state = backlog.StateFailed
		}
		result := d.Result
		if d.Status == "escalated" {
			result = "ESCALATED: " + result
			if name, err := o.Registry.SupervisorName(ctx, agent.ID); err == nil && name != "" {
				result += " (to " + name + ")"
			}
		}
		if _, err := o.Backlog.Complete(ctx, taskID, state, result, d.Error); err != nil {
			return true, err
		}
		// done step: feed what was learned into the wiki (spec/05). The
		// consolidation (merging near-duplicates) runs task-independently in the
		// paced maintenance job, not here in the hot path.
		if d.Memory != "" {
			// Do not swallow errors: since the embedding can run through a
			// service, a failure means an insight is lost. Completing the task
			// should not fail because of it, but it has to be in the log.
			if err := o.Memory.Ingest(ctx, agent.ID, d.Memory,
				map[string]string{"task_id": taskID.String()}); err != nil {
				o.Log.Warn("wiki: insight could not be stored",
					"agent", agent.Slug, "task", taskID, "err", err)
			}
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
			map[string]string{"status": "task_" + d.Status})
		o.publishTask(taskID, agent.ID)
		return true, nil

	case daemon.TypeSetStage:
		ss, err := daemon.DecodePayload[daemon.SetStage](msg)
		if err != nil {
			return false, nil
		}
		target := taskID
		if ss.TaskID != "" {
			if tid, err := uuid.Parse(ss.TaskID); err == nil {
				target = tid
			}
		}
		// Ensure + move + cleanup of empty agent columns in one transaction.
		stage, err := o.Backlog.SetTaskStageByName(ctx, agent.ID, target, ss.Stage)
		if err != nil {
			o.Log.Warn("set_stage: task could not be moved", "err", err)
			return false, nil
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &target, observability.KindLifecycle,
			map[string]string{"status": "stage", "stage": stage.Name})
		o.publishTask(target, agent.ID)
		return false, nil

	case daemon.TypeNote:
		n, err := daemon.DecodePayload[daemon.Note](msg)
		if err != nil || strings.TrimSpace(n.Content) == "" {
			return false, nil
		}
		target := taskID
		if n.TaskID != "" {
			if tid, err := uuid.Parse(n.TaskID); err == nil {
				target = tid
			}
		}
		if n.Scope == "memory" {
			// Generally applicable insight: straight into memory, not only via
			// the memory field at completion time (M7).
			if err := o.Memory.Ingest(ctx, agent.ID, n.Content, map[string]string{
				"task_id": target.String(), "origin": "proactive"}); err != nil {
				o.Log.Warn("wiki: insight (remember) could not be stored",
					"agent", agent.Slug, "err", err)
			}
		} else {
			// Task-related note: onto the task itself.
			if _, err := o.Backlog.AddNote(ctx, target, "agent", n.Content); err != nil {
				o.Log.Warn("note: could not be stored", "err", err)
				return false, nil
			}
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &target, observability.KindLifecycle,
			map[string]string{"status": "note", "scope": n.Scope})
		o.publishTask(target, agent.ID)
		return false, nil

	default:
		o.Log.Warn("unexpected daemon message", "type", msg.Type)
		return false, nil
	}
}

const (
	// statusIncomplete is what a runtime adapter reports when a run ended at the
	// turn limit: work happened, a result does not exist. Not a backlog state —
	// the control plane translates it into failed + follow-up task.
	statusIncomplete = "incomplete"

	// originContinuation marks the follow-up task of an aborted run. The loop
	// protection counts the length of the chain by this prefix.
	originContinuation = "continuation"

	// maxContinuations limits how often a task is continued at the turn limit
	// before it escalates. Whoever has no result after that many full runs does
	// not need another run but a human: either the assignment is cut too large
	// or max_turns is too small.
	maxContinuations = 3
)

// handleIncomplete processes a run aborted at the turn limit (spec/03).
// Previously it ended as failed without an error text and without a result —
// the next heartbeat started the same work from scratch, in an endless loop.
//
// Now: the interim state the daemon obtained from the aborted session is
// attached to the task as a note (visible in the ticket) and stored as the
// result; from it a **follow-up task** is created that resumes the runtime
// session and carries on there. After maxContinuations consecutive
// continuations the task escalates instead.
func (o *Orchestrator) handleIncomplete(ctx context.Context, agent agents.Agent, taskID uuid.UUID, d daemon.TaskDone) error {
	handover := strings.TrimSpace(d.Result)
	if handover != "" {
		if _, err := o.Backlog.AddNote(ctx, taskID, "agent", "Interim state (run aborted at the turn limit):\n\n"+handover); err != nil {
			o.Log.Warn("interim state could not be stored as a note", "task", taskID, "err", err)
		}
	}

	task, err := o.Backlog.Get(ctx, taskID)
	if err != nil {
		return err
	}
	depth, err := o.Backlog.AncestorsWithOrigin(ctx, taskID, originContinuation)
	if err != nil {
		o.Log.Warn("continuation depth not determinable — treating it as the last one", "task", taskID, "err", err)
		depth = maxContinuations
	}

	// End of the chain: do not keep it running, hand it over instead.
	if depth >= maxContinuations || d.SessionID == "" {
		reason := fmt.Sprintf("%s. Handed over after %d continuations without a result — the assignment is cut too large or max_turns is too small.", d.Error, depth)
		if d.SessionID == "" {
			reason = d.Error + ". Without a runtime session no continuation is possible."
		}
		result := "ESCALATED: " + handover
		if name, err := o.Registry.SupervisorName(ctx, agent.ID); err == nil && name != "" {
			result += " (to " + name + ")"
		}
		if _, err := o.Backlog.Complete(ctx, taskID, backlog.StateFailed, result, reason); err != nil {
			return err
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
			map[string]string{"status": "task_escalated", "reason": "max_turns", "continuations": strconv.Itoa(depth)})
		o.publishTask(taskID, agent.ID)
		return nil
	}

	// Continuation: same title and same priority as the original task. The title
	// deliberately stays unchanged — the heartbeat dedup recognizes by it that
	// this work is still running and does not fire alongside it.
	child, err := o.Backlog.CreateChild(ctx, taskID, backlog.ChildSpec{
		Title:       task.Title,
		Body:        task.Body,
		Origin:      originContinuation + ":" + taskID.String(),
		Priority:    task.Priority,
		SessionID:   d.SessionID,
		ResumeInput: continuationInput(handover),
	})
	if err != nil {
		return err
	}
	if _, err := o.Backlog.Complete(ctx, taskID, backlog.StateFailed, handover,
		fmt.Sprintf("%s. Continuation scheduled as follow-up task %s (%d/%d).", d.Error, child.ID, depth+1, maxContinuations)); err != nil {
		return err
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "task_continued", "reason": "max_turns",
			"continuation_task": child.ID.String(), "continuations": strconv.Itoa(depth + 1)})
	o.publishTask(taskID, agent.ID)
	return nil
}

// continuationInput is the input the resumed session carries on with. The
// handover state is included because, while the session holds the context, the
// agent has to know that it was interrupted at the turn limit — otherwise it
// answers its own summary instead of working.
func continuationInput(handover string) string {
	in := "Your previous run was aborted at the turn limit. Carry on where you left off."
	if handover != "" {
		in += "\n\nYour own handover state:\n\n" + handover
	}
	in += "\n\nStart with the open point you noted yourself as the next step. " +
		"Keep the run short enough to reach a result this time — better to finish a partial " +
		"result cleanly and create the rest as its own task than to run into the limit again."
	return in
}

// enforceBudget checks the budget caps (agent field + budget_limit guard
// rails). Exceeding them pauses the agent (fail-closed) and stops the run.
func (o *Orchestrator) enforceBudget(ctx context.Context, agent agents.Agent, link DaemonLink, taskID uuid.UUID, s *session) error {
	summary, err := o.Obs.CostByAgent(ctx, agent.ID)
	if err != nil {
		return nil
	}
	limit := agent.BudgetUSD
	if rules, err := o.Rails.List(ctx, agent.OrgID); err == nil {
		if rl := guardrails.BudgetLimit(rules, agent.ID); rl > 0 && (limit == 0 || rl < limit) {
			limit = rl
		}
	}
	if limit <= 0 || summary.TotalUSD < limit {
		return nil
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindGuardrail,
		map[string]any{"rule": "budget_limit", "limit_usd": limit, "spent_usd": summary.TotalUSD, "action": "agent paused"})
	_ = o.Registry.SetKilled(ctx, agent.ID, true)
	s.killed = true
	_, _ = o.Backlog.Reopen(ctx, taskID, "budget exceeded — agent paused")
	_ = o.sendMsg(ctx, link, daemon.TypeKill, map[string]string{"reason": "budget"})
	return fmt.Errorf("%w (%.4f ≥ %.4f USD)", errBudgetExceeded, summary.TotalUSD, limit)
}

// errBudgetExceeded marks the budget stop: the task is open again, the agent is
// paused — not a failed completion.
var errBudgetExceeded = errors.New("budget exceeded")

const (
	// originAgentTask marks a task an agent created itself (covey/create_task) —
	// as a subtask or by delegation. The full form is "agent:<slug>" so the
	// audit trail records who created it.
	originAgentTask = "agent"

	// maxAgentTaskDepth limits the chain of self-created tasks: a subtask may
	// have subtasks, but not to arbitrary depth. Without this limit an agent
	// decomposes its work recursively until the budget is empty.
	maxAgentTaskDepth = 3

	// maxAgentTasksPerRun limits the width: this many tasks a single run may
	// spin off. An agent that needs more has not decomposed its work but copied
	// it.
	maxAgentTasksPerRun = 10
)

// createAgentTask serves covey/create_task: the agent creates a subtask for
// itself or delegates to a colleague. The new task hangs as a child off the
// running one — that carries both the audit trail (who created it) and the loop
// protection.
//
// Fail-closed in three directions, because an agent that can create tasks can
// keep itself busy until the budget is empty:
//
//   - Depth (maxAgentTaskDepth) — no infinite decomposition.
//   - Width (maxAgentTasksPerRun) — a run spins off a limited amount.
//   - Duplicates — if an open task with the same title already exists at the
//     target agent, no second one is created. That is exactly where loops fail
//     in which every run creates the same task again.
//
// The delegation stays within the organization: the target agent is resolved by
// its slug **within the sender's org**.
func (o *Orchestrator) createAgentTask(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestCreateTask) daemon.InjectCreateTask {
	fail := func(m string) daemon.InjectCreateTask {
		return daemon.InjectCreateTask{RequestID: req.RequestID, OK: false, Error: m}
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return fail("title is missing")
	}

	// Target agent: empty = myself, otherwise a colleague from the same org.
	targetAgent := agent
	if slug := strings.TrimSpace(req.Agent); slug != "" && slug != agent.Slug {
		found, err := o.Registry.GetBySlug(ctx, agent.OrgID, slug)
		if err != nil {
			return fail(fmt.Sprintf("no agent %q in this organization", slug))
		}
		if found.Killed {
			return fail(fmt.Sprintf("agent %q is paused — no delegation", slug))
		}
		targetAgent = found
	}

	depth, err := o.Backlog.AncestorsWithOrigin(ctx, taskID, originAgentTask+":")
	if err != nil {
		return fail("origin chain cannot be checked")
	}
	if depth >= maxAgentTaskDepth {
		return fail(fmt.Sprintf("task chain too deep (%d) — do not decompose further, finish or escalate instead", depth))
	}
	children, err := o.Backlog.CountChildren(ctx, taskID)
	if err != nil {
		return fail("subtasks cannot be counted")
	}
	if children >= maxAgentTasksPerRun {
		return fail(fmt.Sprintf("this run has already created %d tasks — that is the limit", children))
	}
	dup, err := o.Backlog.OpenWithTitle(ctx, targetAgent.ID, title)
	if err != nil {
		return fail("duplicate check failed")
	}
	if dup {
		return fail(fmt.Sprintf("there is already an open task %q at %s — no second one created", title, targetAgent.Slug))
	}

	created, err := o.Backlog.CreateChild(ctx, taskID, backlog.ChildSpec{
		AgentID:  targetAgent.ID,
		Title:    title,
		Body:     req.Body,
		Origin:   originAgentTask + ":" + agent.Slug,
		Priority: req.Priority,
	})
	if err != nil {
		return fail(err.Error())
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
		map[string]string{"status": "task_created", "created_task": created.ID.String(),
			"target_agent": targetAgent.Slug, "title": title})
	o.publishTask(created.ID, targetAgent.ID)
	if targetAgent.ID != agent.ID {
		o.EnsureRunning(targetAgent.ID) // delegation wakes the colleague
	}
	return daemon.InjectCreateTask{RequestID: req.RequestID, OK: true,
		TaskID: created.ID.String(), Agent: targetAgent.Slug}
}

// brokerWiki serves the agent's wiki tools (covey/wiki_*, spec/05) against the
// control plane's memory store.
func (o *Orchestrator) brokerWiki(ctx context.Context, agent agents.Agent, req daemon.RequestWiki) daemon.InjectWiki {
	fail := func(m string) daemon.InjectWiki {
		return daemon.InjectWiki{RequestID: req.RequestID, OK: false, Error: m}
	}
	ok := func(v any) daemon.InjectWiki {
		data, _ := json.Marshal(v)
		return daemon.InjectWiki{RequestID: req.RequestID, OK: true, Data: data}
	}
	switch req.Op {
	case "list":
		// All pages for the working copy in the home (spec/05).
		entries, err := o.Memory.List(ctx, agent.ID, 1000)
		if err != nil {
			return fail(err.Error())
		}
		type page struct {
			Slug  string   `json:"slug"`
			Title string   `json:"title"`
			Body  string   `json:"body"`
			Links []string `json:"links"`
			Type  string   `json:"type"`
			Tags  []string `json:"tags"`
		}
		pages := make([]page, 0, len(entries))
		for _, e := range entries {
			pages = append(pages, page{e.Slug, e.Title, e.Content, e.Links, e.Type, e.Tags})
		}
		return ok(pages)
	case "search":
		entries, err := o.Memory.Search(ctx, agent.ID, req.Query, 8)
		if err != nil {
			return fail(err.Error())
		}
		type hit struct {
			Slug    string  `json:"slug"`
			Title   string  `json:"title"`
			Score   float64 `json:"score"`
			Excerpt string  `json:"excerpt"`
		}
		hits := make([]hit, 0, len(entries))
		for _, e := range entries {
			hits = append(hits, hit{e.Slug, e.Title, e.Score, truncateRunes(e.Content, 200)})
		}
		return ok(map[string]any{"results": hits})
	case "read":
		e, err := o.Memory.Read(ctx, agent.ID, req.Slug)
		if err != nil {
			return fail("page not found")
		}
		return ok(e)
	case "write":
		e, err := o.Memory.Write(ctx, agent.ID, memory.PageInput{
			Slug: req.Slug, Title: req.Title, Body: req.Body,
			Source: "agent", Type: req.Type, Tags: req.Tags,
		})
		if err != nil {
			return fail(err.Error())
		}
		return ok(map[string]string{"slug": e.Slug, "title": e.Title, "type": e.Type})
	case "append":
		// Append without rewriting the page (spec/05).
		e, err := o.Memory.Append(ctx, agent.ID, req.Slug, req.Text)
		if err != nil {
			return fail(err.Error())
		}
		return ok(map[string]string{"slug": e.Slug, "title": e.Title})
	case "delete":
		// Wiki upkeep: remove a superfluous/merged page. Agent-scoped in the
		// store, so the agent can only delete within its own wiki (spec/05).
		if err := o.Memory.DeleteBySlug(ctx, agent.ID, req.Slug); err != nil {
			return fail("page not found")
		}
		return ok(map[string]string{"slug": req.Slug, "deleted": "true"})
	default:
		return fail("unknown wiki operation: " + req.Op)
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + " …"
}

// brokerCredential is the broker decision (spec/04): authorization per
// ACCESS.md, guard rails, then short-lived pass-through from the SecretStore.
func (o *Orchestrator) brokerCredential(ctx context.Context, agent agents.Agent, req daemon.RequestCredential) daemon.InjectCredentials {
	deny := func(reason string) daemon.InjectCredentials {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": false, "reason": reason})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System, Granted: false, Reason: reason}
	}
	ok, err := o.Registry.HasAccess(ctx, agent.ID, req.System, "")
	if err != nil || !ok {
		return deny("no access per ACCESS.md")
	}
	// Plugin activation is a central enforcement point (fail-closed): a disabled
	// or unknown target system gets no credentials.
	var kind string
	if o.Targets != nil {
		if _, err := o.Targets.System(ctx, agent.OrgID, req.System); err != nil {
			return deny("target system not enabled: " + req.System)
		}
		kind, _ = o.Targets.Kind(ctx, agent.OrgID, req.System)
	}
	rules, err := o.Rails.List(ctx, agent.OrgID)
	if err != nil {
		return deny("guard rails not readable (fail-closed)")
	}
	if v := guardrails.Evaluate(rules, agent.ID, req.System); v.Decision == guardrails.Deny {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindGuardrail,
			map[string]any{"rule": "deny_system", "system": req.System, "pattern": v.Rule.Pattern})
		return deny("forbidden by guard rail")
	}
	// Local systems (NoCredentials, e.g. the dev plugin) need no secrets — the
	// actions run entirely inside the sandbox. The checks above (ACCESS.md,
	// activation, guard rails) still apply; what is granted is an empty
	// credential.
	if d, ok := target.Describe(req.System); ok && d.NoCredentials {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": true, "local": true})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
			Granted: true, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
	}
	// MCP servers carry their endpoint in the config; auth is optional. A
	// missing token therefore does NOT deny — the server may be reachable
	// without auth. The URL secret remains an optional override.
	if kind == "mcp" {
		token, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_token")
		baseURL, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_url")
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": true, "kind": "mcp",
				"ttl_secs": int(o.DaemonTokenTTL.Seconds())})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
			Granted: true, Token: token, BaseURL: baseURL, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
	}
	token, err := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_token")
	if err != nil {
		return deny("no secret stored or assigned: " + req.System + "_token")
	}
	// If the plugin brings the endpoint along itself (BaseURLOptional),
	// <name>_url is only an override — a missing secret then does not deny,
	// otherwise a properly set up agent would fail on a value the control plane
	// does not need at all.
	baseURL, err := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_url")
	if err != nil {
		if d, ok := target.Describe(req.System); !ok || !d.BaseURLOptional {
			return deny("no secret stored or assigned: " + req.System + "_url")
		}
		baseURL = ""
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": req.System, "granted": true, "ttl_secs": int(o.DaemonTokenTTL.Seconds())})
	return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
		Granted: true, Token: token, BaseURL: baseURL, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
}

// brokerTarget passes the definition of a manifest plugin into the sandbox —
// only for enabled custom systems of the organization (fail-closed). Compiled
// plugins are known to the daemon itself, which does not even ask here.
func (o *Orchestrator) brokerTarget(ctx context.Context, agent agents.Agent, req daemon.RequestTarget) daemon.InjectTarget {
	deny := func(reason string) daemon.InjectTarget {
		return daemon.InjectTarget{RequestID: req.RequestID, System: req.System, Granted: false, Reason: reason}
	}
	if o.Targets == nil {
		return deny("no target-system store configured")
	}
	kind, raw, err := o.Targets.BrokeredDefinition(ctx, agent.OrgID, req.System)
	if err != nil {
		return deny("target system not available: " + err.Error())
	}
	return daemon.InjectTarget{RequestID: req.RequestID, System: req.System, Granted: true,
		Kind: kind, Manifest: raw}
}

// decideAction is the central policy decision for an action: allow (auto), deny
// (guard rail) or pending (approval gate, spec/06). An already granted, unused
// approval is consumed.
func (o *Orchestrator) decideAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestApproval) daemon.ApprovalDecision {
	// Per-agent tool assignment (fail-closed as soon as an allowlist exists):
	// the subject is system:tool. Without an assignment for the system this does
	// not apply — built-ins/manifests stay untouched.
	if o.Targets != nil {
		if system, tool, ok := strings.Cut(req.Action, ":"); ok {
			allowed, err := o.Targets.AgentToolAllowed(ctx, agent.ID, system, tool)
			if err != nil {
				return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
					Reason: "tool assignment not readable (fail-closed)"}
			}
			if !allowed {
				_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindGuardrail,
					map[string]any{"rule": "tool_not_assigned", "action": req.Action, "decision": "denied"})
				o.events.Publish(Event{Type: "guardrail", AgentID: agent.ID.String(),
					Data: map[string]string{"action": req.Action, "decision": "denied"}})
				return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
					Reason: "tool " + tool + " is not assigned to this agent"}
			}
		}
	}
	rules, err := o.Rails.List(ctx, agent.OrgID)
	if err != nil {
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
			Reason: "guard rails not readable (fail-closed)"}
	}
	verdict := guardrails.Evaluate(rules, agent.ID, req.Action)
	switch verdict.Decision {
	case guardrails.Deny:
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindGuardrail,
			map[string]any{"rule": verdict.Rule.RuleType, "pattern": verdict.Rule.Pattern,
				"action": req.Action, "decision": "denied"})
		o.events.Publish(Event{Type: "guardrail", AgentID: agent.ID.String(),
			Data: map[string]string{"action": req.Action, "decision": "denied"}})
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
			Reason: "forbidden by guard rail " + verdict.Rule.Pattern}

	case guardrails.RequireApproval:
		// An unused approval for exactly this action? Then consume it.
		var approvalID uuid.UUID
		err := o.Pool.QueryRow(ctx, `UPDATE approvals SET used=TRUE
			WHERE id = (SELECT id FROM approvals
				WHERE agent_id=$1 AND action=$2 AND status='approved' AND NOT used
				ORDER BY decided_at DESC LIMIT 1)
			RETURNING id`, agent.ID, req.Action).Scan(&approvalID)
		if err == nil {
			_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindApproval,
				map[string]any{"action": req.Action, "decision": "approved", "approval_id": approvalID.String()})
			return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "approved"}
		}
		appr, err := o.Obs.CreateApproval(ctx, agent.OrgID, agent.ID, &taskID, req.Action, json.RawMessage(req.Params))
		if err != nil {
			return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied", Reason: err.Error()}
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindApproval,
			map[string]any{"action": req.Action, "decision": "pending", "approval_id": appr.ID.String()})
		o.events.Publish(Event{Type: "approval", AgentID: agent.ID.String(),
			Data: map[string]string{"approval_id": appr.ID.String(), "action": req.Action}})
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "pending",
			ApprovalID: appr.ID.String(), CorrelationKey: "approval:" + appr.ID.String()}

	default:
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindApproval,
			map[string]any{"action": req.Action, "decision": "auto-allow"})
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "approved"}
	}
}

// OnApprovalDecided closes the loop of the approval gate: the decision wakes
// the task blocked on "approval:<id>" (wake-on-correlation).
func (o *Orchestrator) OnApprovalDecided(ctx context.Context, appr observability.Approval) {
	text := fmt.Sprintf("The approval for %q was granted. Perform the action again through the action proxy now and finish the task.", appr.Action)
	if appr.Status == "denied" {
		text = fmt.Sprintf("The approval for %q was DENIED. Do not perform the action; choose another way or escalate.", appr.Action)
	}
	if _, err := o.Backlog.CorrelateWake(ctx, "approval:"+appr.ID.String(), text); err != nil &&
		!errors.Is(err, backlog.ErrNotFound) {
		o.Log.Warn("approval correlation", "approval", appr.ID, "err", err)
	}
}

// Kill is the kill switch for one agent: set the flag, abort the running
// session immediately, put the started task back into the backlog.
func (o *Orchestrator) Kill(ctx context.Context, agentID uuid.UUID) error {
	if err := o.Registry.SetKilled(ctx, agentID, true); err != nil {
		return err
	}
	o.mu.Lock()
	s := o.sessions[agentID]
	o.mu.Unlock()
	if s != nil {
		s.killed = true
		if s.link != nil {
			_ = o.sendMsg(ctx, s.link, daemon.TypeKill, map[string]string{"reason": "kill-switch"})
		}
		s.cancel()
	}
	// Tear down a parked warm sandbox (no active run) immediately — a killed
	// agent must not keep a running container.
	o.evictWarm(ctx, agentID, true)
	// Reopen the started task so nothing gets lost.
	_, _ = o.Pool.Exec(ctx, `UPDATE backlog_tasks SET state='open', updated_at=now()
		WHERE agent_id=$1 AND state='in_progress'`, agentID)
	if agent, err := o.Registry.Get(ctx, agentID); err == nil {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindLifecycle,
			map[string]string{"status": "killed"})
		o.events.Publish(Event{Type: "agent_status", AgentID: agentID.String(), Data: map[string]string{"status": "killed"}})
	}
	return nil
}

// KillFleet is the fleet-wide emergency stop (spec/06).
// ResumeFleet takes the emergency stop back — the counterpart to KillFleet, and
// a complete one at that.
//
// Previously, taking it back only released the org flag, while KillFleet had
// stopped EVERY agent individually. The workforce stayed put afterwards even
// though the UI reported "no emergency stop" — one would have had to switch
// every agent back on by hand, without anything hinting at it. An emergency
// stop has to be releasable the way it was triggered.
func (o *Orchestrator) ResumeFleet(ctx context.Context, orgID uuid.UUID) error {
	if err := o.Registry.SetFleetKilled(ctx, orgID, false); err != nil {
		return err
	}
	ids, err := o.agentIDs(ctx, orgID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		if err := o.Registry.SetKilled(ctx, id, false); err != nil && firstErr == nil {
			firstErr = err
			continue
		}
		// Whoever has open work should pick it up right away instead of waiting
		// for the next tick.
		o.EnsureRunning(id)
	}
	return firstErr
}

// agentIDs lists the agents of an organization.
func (o *Orchestrator) agentIDs(ctx context.Context, orgID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := o.Pool.Query(ctx, "SELECT id FROM agents WHERE org_id=$1", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (o *Orchestrator) KillFleet(ctx context.Context, orgID uuid.UUID) error {
	if err := o.Registry.SetFleetKilled(ctx, orgID, true); err != nil {
		return err
	}
	ids, err := o.agentIDs(ctx, orgID)
	if err != nil {
		return err
	}
	var firstErr error
	for _, id := range ids {
		if err := o.Kill(ctx, id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (o *Orchestrator) shutdown() {
	o.mu.Lock()
	for _, s := range o.sessions {
		s.cancel()
	}
	// Collect and tear down parked warm sandboxes (no active run).
	parked := make([]*warmSession, 0, len(o.warm))
	for id, ws := range o.warm {
		parked = append(parked, ws)
		delete(o.warm, id)
	}
	o.mu.Unlock()
	for _, ws := range parked {
		ws.cancel()
		<-ws.done
		ws.teardown()
	}
}

// HandleWebhook processes an incoming target-system event (M3/M4): idempotent;
// first correlates against blocked tasks (ResumeInput is then the continuation
// input), otherwise a new backlog task (TaskBody).
func (o *Orchestrator) HandleWebhook(ctx context.Context, agent agents.Agent, source string, ev target.WebhookEvent) (string, error) {
	tag, err := o.Pool.Exec(ctx, `INSERT INTO webhook_events (dedup_key, source)
		VALUES ($1,$2) ON CONFLICT (dedup_key) DO NOTHING`, ev.DedupKey, source)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "duplicate", nil // retry from the target system — already processed
	}
	if !ev.Wake {
		return "ignored", nil // e.g. the echo of the agent's own reply
	}
	if task, err := o.Backlog.CorrelateWake(ctx, ev.CorrelationKey, ev.ResumeInput); err == nil {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &task.ID, observability.KindLifecycle,
			map[string]string{"status": "wake_on_correlation", "correlation_key": ev.CorrelationKey})
		o.publishTask(task.ID, agent.ID)
		return "correlated", nil
	} else if !errors.Is(err, backlog.ErrNotFound) {
		return "", err
	}
	if ev.CorrelateOnly {
		return "ignored", nil // if nobody waits for the event, no work is created
	}
	task, err := o.Backlog.Create(ctx, agent.OrgID, agent.ID, ev.Title, ev.TaskBody, "webhook:"+source, 3)
	if err != nil {
		return "", err
	}
	o.publishTask(task.ID, agent.ID)
	return "created", nil
}

// HandleAgentTrigger processes an agent's generic, token-authenticated webhook
// trigger (spec/03, wake source event): optionally idempotent via a dedup_key,
// creates a backlog task and kicks off the dispatch immediately. Unlike
// HandleWebhook there is no target-system plugin and no correlation — the
// sender is an arbitrary foreign system.
func (o *Orchestrator) HandleAgentTrigger(ctx context.Context, agent agents.Agent, title, body string, priority int, dedupKey string) (string, error) {
	if dedupKey != "" {
		// Globally unique table — scope it per agent so that foreign systems of
		// different agents do not deduplicate each other.
		tag, err := o.Pool.Exec(ctx, `INSERT INTO webhook_events (dedup_key, source)
			VALUES ($1,'agent-trigger') ON CONFLICT (dedup_key) DO NOTHING`,
			"trigger:"+agent.ID.String()+":"+dedupKey)
		if err != nil {
			return "", err
		}
		if tag.RowsAffected() == 0 {
			return "duplicate", nil
		}
	}
	task, err := o.Backlog.Create(ctx, agent.OrgID, agent.ID, title, body, "webhook:trigger", priority)
	if err != nil {
		return "", err
	}
	o.publishTask(task.ID, agent.ID)
	return "created", nil
}
