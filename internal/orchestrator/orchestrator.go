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
	"reflect"
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
	"covey/internal/buildinfo"
	"covey/internal/daemon"
	"covey/internal/egress"
	"covey/internal/guardrails"
	"covey/internal/identity"
	"covey/internal/memory"
	"covey/internal/observability"
	"covey/internal/reqlog"
	reqlogstore "covey/internal/reqlog/store"
	"covey/internal/runtimes"
	"covey/internal/sandbox"
	"covey/internal/secrets"
	"covey/internal/skills"
	targetstore "covey/internal/target/store"
	"covey/internal/workplaces"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

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
	// Runtimes is the capacity layer: which contract an agent works on and
	// which of its credentials this waking phase runs on (spec/18).
	Runtimes *runtimes.Store
	Identity identity.Provider
	Memory   *memory.Store
	// Skills are the agents' skills (library + agent-owned). nil = feature
	// switched off; runs then get no skills materialized.
	Skills  *skills.Store
	Targets *targetstore.Store
	Egress  *egress.Store
	// Workplaces holds what an organisation brings along itself — its own
	// workplace images, and the allowlist of images that may run BESIDE a
	// sandbox as services (spec/16). nil = no allowlist is enforced, which is
	// the state of a stack wired without it (the integration harness); the
	// production wiring always sets it.
	Workplaces *workplaces.Store
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
	// TidyHomeAbove: ab dieser Größe wird ein Agent gebeten, sein Home
	// aufzuräumen (siehe tidy.go). 0 oder kleiner = gar nicht.
	TidyHomeAbove int64
	// StaleAfter: so lange darf ein Agent einen beschäftigten Zustand tragen,
	// ohne dass eine Sitzung dahintersteht, bevor die Plattform ihn auflöst.
	// 0 → Voreinstellung. Ein Knopf für Tests, kein Bedienelement.
	StaleAfter time.Duration
	// RuntimeTools is the runs' built-in tool scope (COVEY_RUNTIME_TOOLS).
	// Empty → daemon.DefaultAllowedTools. The list decides not only what a run
	// may use but what exists for it at all — see daemon.DefaultAllowedTools.
	RuntimeTools []string
	Log          *slog.Logger
}

type Orchestrator struct {
	Options

	mu sync.Mutex
	// laufend zählt, was der Orchestrator an eigenen Nebenläufigkeiten gestartet
	// hat: die Sitzungen der Agenten und seine Dauerschleifen. Ohne sie heißt
	// „abgebrochen" nur, dass das Signal gesetzt ist — und wer danach aufräumt,
	// räumt unter noch laufender Arbeit weg. Im Test war das ein Verzeichnis,
	// das gelöscht wurde, während eine Sitzung hineinschrieb; im Betrieb ist es
	// ein Sandbox-Abbau, der beim Beenden des Prozesses abgeschnitten wird.
	laufend  sync.WaitGroup
	sessions map[uuid.UUID]*session
	waiting  map[uuid.UUID]chan DaemonLink
	// dying receives the reason when the sandbox of an agent currently being
	// woken has ended before its daemon ever connected. Without it, a crashed
	// or OOM-killed container costs the full ReadyTimeout and produces a
	// message about a daemon that never had a chance to connect.
	dying map[uuid.UUID]chan string
	// wakeFehler hält fest, welcher Agent gerade NICHT aufwachen kann, und
	// verzögert den nächsten Versuch.
	//
	// Ohne das versucht der Scheduler es alle dreißig Sekunden weiter, für
	// immer. Auf covey.work waren das rund 900 Fehlversuche in sechseinhalb
	// Stunden — jeder mit einem Runner-Platz, vier Zeilen Aufzeichnung und
	// keiner Aussicht auf ein anderes Ergebnis: Was einen Weckversuch
	// scheitern lässt (ein verlorener Block, ein fehlendes Image, ein Host,
	// der nicht antwortet), ändert sich nicht dadurch, dass man dreißig
	// Sekunden wartet.
	//
	// Im Speicher und nicht in der Datenbank: Ein Neustart der Control Plane
	// ist genau der Moment, in dem sich etwas geändert haben KANN — ein
	// Deploy, eine neue Fassung, eine reparierte Einstellung. Dann soll sofort
	// wieder versucht werden, nicht erst nach Ablauf einer gespeicherten
	// Sperre.
	wakeFehler map[uuid.UUID]*WakeTrouble
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
	// verwaist merkt sich, seit wann ein beschäftigter Zustand ohne Sitzung
	// dasteht. Im Speicher und nicht in der Datenbank: nach einem Neustart der
	// Steuerebene ist ohnehin JEDE Sitzung weg, und dann soll die Frist neu
	// laufen statt sofort abzulaufen.
	verwaist map[uuid.UUID]time.Time
	// lastWarmSync: when this agent's parked home last went into the store.
	// Guarded by mu, like warm itself.
	lastWarmSync map[uuid.UUID]time.Time

	wikiSweepMu   sync.Mutex
	lastWikiSweep time.Time

	// noCredNoted: when this agent's missing credential was last put on the
	// record. The tick comes back every thirty seconds and finds the same open
	// task, and a state that changes only when a human acts does not need to be
	// stated twice a minute — that would bury the recording of the run people
	// are actually looking for.
	noCredMu    sync.Mutex
	noCredNoted map[uuid.UUID]time.Time

	events *Broadcaster

	// usage holds the engines' own utilisation figures per credential — asked
	// centrally and briefly cached, because the provider's endpoint has a rate
	// limit of its own (usage.go).
	usage *usageCache
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
	if opts.TidyHomeAbove == 0 {
		// Fünf Gigabyte: auf der gemessenen Instanz trifft das genau den
		// Agenten mit 19,1 GB und keinen der übrigen sieben (0 bis 1,3 GB).
		// Eine Schwelle, die alle trifft, wird zur Gewohnheit und dann
		// ignoriert.
		opts.TidyHomeAbove = 5 << 30
	}
	if opts.StaleAfter == 0 {
		// Großzügig: ein Weckruf setzt den Zustand, bevor die Sitzung steht,
		// und ein Sandbox-Start darf auf einem frischen Host eine
		// Dreiviertelstunde dauern. Aufgelöst wird erst, was auch nach dieser
		// Zeit noch niemanden hinter sich hat.
		opts.StaleAfter = 5 * time.Minute
	}
	if len(opts.RuntimeTools) == 0 {
		opts.RuntimeTools = daemon.DefaultAllowedTools
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Orchestrator{
		Options:      opts,
		usage:        newUsageCache(),
		sessions:     map[uuid.UUID]*session{},
		waiting:      map[uuid.UUID]chan DaemonLink{},
		dying:        map[uuid.UUID]chan string{},
		warm:         map[uuid.UUID]*warmSession{},
		lastWarmSync: map[uuid.UUID]time.Time{},
		verwaist:     map[uuid.UUID]time.Time{},
		events:       NewBroadcaster(),
	}
}

// warmIdleTTL: after this much idle time a parked warm sandbox is torn down
// after all, so it does not hold resources indefinitely.
const warmIdleTTL = 30 * time.Minute

// warmSyncEvery bounds how often a parked warm home is written into the store.
// Without a bound an agent on a two-minute heartbeat would pay for a full scan
// of its home every two minutes; with it, the price is one scan per interval
// and the exposure — work that exists only in a container volume — is at most
// that interval long. Five minutes against a run that costs a quarter of an
// hour of model time is the right way round.
const warmSyncEvery = 5 * time.Minute

// wikiIndexLimit caps the wiki index in the triage context. It is a backstop,
// not a budget: the index carries slugs only, so even a large wiki stays cheap
// (50 pages ≈ 540 tokens), and cutting it earlier would buy tokens with
// duplicates — the agent writes a second time what it does not see there.
const wikiIndexLimit = 150

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
	// The pool value this waking phase runs on (spec/04): which secret key it
	// came out of and which slot. Held for the whole phase on purpose — the
	// choice must not change mid-run, because Claude Code caches the prompt
	// prefix per credential and a swap would throw that cache away in the middle
	// of the work. The costs are booked against it, and it is what a rejection
	// from the API puts into cooldown.
	credRuntime uuid.UUID
	credOrd     int
	// Where this run is happening: the host the sandbox stands on, and the
	// name it carried at that moment. Held here and not only written into the
	// recording, because the question "where is this agent working right now"
	// is asked while it works — and the recording answers it only for whoever
	// scrolls far enough back, which on a talkative run is further than the
	// log window reaches.
	runnerID   uuid.UUID
	runnerName string
	// The sandbox itself, so that an action can reach it while the run is
	// going on. The agent's own request for services needs it: it comes in over
	// the daemon link, in the middle of a run, and the only other route to the
	// right host would be a lookup by agent — which is the same thing, worse.
	sandbox Sandbox
	// And what stands beside the sandbox. Kept for the same reason as the two
	// above and one more: a warm sandbox serves many jobs, so the services a
	// run worked against were often brought up in a waking phase that is no
	// longer on screen. Each job records them again from here.
	services []sandbox.ServiceRun
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
	o.nebenlaeufig(func() { o.listenLoop(ctx) })
	o.nebenlaeufig(func() { o.wikiMaintenanceLoop(ctx) })
	o.nebenlaeufig(func() { o.warmReaperLoop(ctx) })
	o.nebenlaeufig(func() { o.boardJanitorLoop(ctx) })
	o.nebenlaeufig(func() { o.homeJanitorLoop(ctx) })
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
		WHERE NOT a.killed AND NOT org.fleet_killed AND a.hired_at IS NOT NULL`)
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
	o.reconcileStuck(ctx)
}

// reconcileStuck löst Zustände auf, hinter denen nichts mehr steht.
//
// Der Anlass: ein Agent stand um 08:02 auf `working`, seine letzte Aufgabe war
// um 06:35 fertig, sein Backlog leer, und auf dem Host lief weiter ein
// Container. Zwischen „letzte Aufgabe fertig" und „Sandbox unten" hängt keine
// Frist, die den ganzen Vorgang umfasst — jeder Schritt hat eine, der Ablauf
// als solcher nicht. Und ein Neustart der Steuerebene heilt es nicht, im
// Gegenteil: die Sitzungen liegen im Speicher, der Zustand in der Datenbank,
// und niemand vergleicht die beiden. Danach trägt der Agent einen Zustand, den
// keine Sitzung deckt — derselbe Anblick, zweite Ursache.
//
// Was hier passiert, ist bewusst das Mildeste, das den Zustand wieder wahr
// macht: schlafen legen, in die Aufzeichnung schreiben, und den Container
// stoppen lassen, falls die Datenebene das anbietet. Nicht behoben wird damit
// die Ursache — die ist unbekannt —, aber der Agent kommt ohne Neustart der
// Plattform aus dem Zustand heraus, und das Ereignis sagt, dass es passiert
// ist. Ein Vorgang, den niemand beenden kann, ist kein Zustand, sondern ein
// Ausfall.
func (o *Orchestrator) reconcileStuck(ctx context.Context) {
	rows, err := o.Pool.Query(ctx, `SELECT id, org_id, status FROM agents
		WHERE hired_at IS NOT NULL AND status = ANY($1)`,
		[]string{agents.StatusTriggered, agents.StatusTriage, agents.StatusWorking, agents.StatusSecuring})
	if err != nil {
		o.Log.Warn("reconcile query", "err", err)
		return
	}
	type verdacht struct {
		id     uuid.UUID
		orgID  uuid.UUID
		status string
	}
	var kandidaten []verdacht
	for rows.Next() {
		var v verdacht
		if rows.Scan(&v.id, &v.orgID, &v.status) == nil {
			kandidaten = append(kandidaten, v)
		}
	}
	rows.Close()

	jetzt := time.Now()
	for _, k := range kandidaten {
		o.mu.Lock()
		_, hatSitzung := o.sessions[k.id]
		_, istWarm := o.warm[k.id]
		if hatSitzung || istWarm {
			delete(o.verwaist, k.id)
			o.mu.Unlock()
			continue
		}
		seit, gesehen := o.verwaist[k.id]
		if !gesehen {
			// Erst einmal nur merken: zwischen „Zustand gesetzt" und „Sitzung
			// eingetragen" liegt ein Augenblick, und den soll niemand als
			// Ausfall lesen.
			o.verwaist[k.id] = jetzt
			o.mu.Unlock()
			continue
		}
		if jetzt.Sub(seit) < o.StaleAfter {
			o.mu.Unlock()
			continue
		}
		delete(o.verwaist, k.id)
		o.mu.Unlock()

		o.Log.Warn("agent carries a status no session backs — putting it to sleep",
			"agent", k.id, "status", k.status, "for", jetzt.Sub(seit).Round(time.Second))
		_ = o.Obs.Record(ctx, k.orgID, k.id, nil, observability.KindLifecycle,
			map[string]string{"status": "stale", "was": k.status})
		agent, err := o.Registry.Get(ctx, k.id)
		if err != nil {
			continue
		}
		// Der Reihe nach: erst der Container, dann der Zustand. Andersherum
		// stünde einen Augenblick lang „schläft" über einer laufenden Sandbox.
		o.evictWarm(ctx, k.id, true)
		if stopper, ok := o.Provider.(StrayStopper); ok {
			if err := stopper.StopStray(ctx, k.id, k.orgID); err != nil {
				o.Log.Warn("the stray sandbox could not be stopped", "agent", k.id, "err", err)
			}
		}
		o.setStatus(ctx, agent, nil, agents.StatusSleeping)
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
		WHERE NOT a.killed AND NOT org.fleet_killed AND a.hired_at IS NOT NULL
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
// system resolves an activated target system for the organization — compiled,
// manifest or MCP. Without a store (tests) the compiled registry has to do.
func (o *Orchestrator) system(ctx context.Context, orgID uuid.UUID, name string) (target.System, error) {
	if o.Targets != nil {
		return o.Targets.System(ctx, orgID, name)
	}
	sys, ok := target.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown target system %q", name)
	}
	return sys, nil
}

func (o *Orchestrator) heartbeatHasWork(ctx context.Context, agentID, orgID uuid.UUID, condition string) (bool, string) {
	system, kind, _ := strings.Cut(condition, ":")
	// Über den Store, nicht über die kompilierte Registry: ein Manifest-Plugin
	// steht dort nicht, kann die Frage aber beantworten, wenn seine Datei einen
	// poll:-Block hat. Vorher fiel jedes nur-wenn: auf ein Katalog-Plugin in den
	// Fail-open-Zweig und der Heartbeat feuerte immer.
	sys, err := o.system(ctx, orgID, system)
	if err != nil {
		o.Log.Warn("nur-wenn: unknown target system — firing anyway", "system", system, "err", err)
		return true, ""
	}
	checker, ok := target.WorkChecks(sys)
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
		ca, _ := o.Secrets.Resolve(ctx, orgID, agentID, system+"_ca")
		cred = target.Credential{BaseURL: baseURL, Token: token, CA: ca}
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

// releaseWorkSignature takes the suppression off a heartbeat again: the state it
// last fired on counts as unseen, so the next tick wakes the agent for it once
// more.
//
// It exists because of a silent standstill. The signature is remembered at
// DISPATCH — before the run has done anything. Ends that run without a result
// (turn limit, escalation, error), the state is nevertheless marked as seen, and
// as long as nothing changes on the merge request or issue, no further heartbeat
// fires: the agent has consumed its own alarm clock without doing the work. On
// covey.work that stopped a QA agent for over an hour with three merge requests
// waiting for its review — no error message anywhere, only a log line saying
// "work backlog unchanged".
//
// Anchored on the TITLE, because the heartbeat gives its task exactly that name
// (Backlog.Create with d.name) and a continuation keeps it deliberately.
func (o *Orchestrator) releaseWorkSignature(ctx context.Context, agentID uuid.UUID, title string) {
	if strings.TrimSpace(title) == "" {
		return
	}
	if _, err := o.Pool.Exec(ctx,
		"UPDATE agent_heartbeats SET last_work_sig='' WHERE agent_id=$1 AND name=$2 AND last_work_sig<>''",
		agentID, title); err != nil {
		o.Log.Warn("release heartbeat signature", "agent", agentID, "name", title, "err", err)
	}
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

// refreshWorkSignature advances a successful heartbeat run to the target
// system state visible after the work. Dispatch stores the state from before
// the run; without this refresh an agent's own issue/MR comment changes the
// note id and immediately wakes the same heartbeat again. Empty signatures
// remain fail-open and therefore never overwrite a usable watermark.
//
// The state after the run, however, also contains what arrived from OUTSIDE
// while the run was going on, and authorship cannot tell the two apart under a
// shared identity (that is the whole reason the level check exists). Whatever
// is written into the watermark here is thereby marked as handled — a foreign
// comment caught in it wakes nobody again. The check therefore only advances
// when the run has actually executed a signature-writing action
// (target.SignatureWriter): without one, every change comes from outside and
// the watermark has to stay where it is, so the next tick fires for it.
//
// What remains is the narrow case of a foreign comment landing on exactly the
// thread the agent commented on itself, in exactly that window. Only the ids of
// the notes written by the agent itself could separate that — the recording
// carries the action, not the id of the note it produced (see
// spec/03-lifecycle-scheduling.md).
//
// The check runs synchronously in the completion path, after the task is
// already finished: the watermark has to be settled before the next tick can
// read it. It costs one work check — and only for runs that wrote something at
// all; a silent run does not even get that far.
func (o *Orchestrator) refreshWorkSignature(ctx context.Context, agentID, orgID uuid.UUID, name string, since time.Time) {
	var condition string
	if err := o.Pool.QueryRow(ctx,
		"SELECT only_if FROM agent_heartbeats WHERE agent_id=$1 AND name=$2",
		agentID, name).Scan(&condition); err != nil {
		// Also the normal case of a heartbeat removed or renamed while its run
		// was going: then there is no watermark left to maintain.
		o.Log.Debug("refresh heartbeat signature: heartbeat no longer present", "agent", agentID, "name", name, "err", err)
		return
	}
	if strings.TrimSpace(condition) == "" {
		return
	}
	if !o.runWroteSignature(ctx, agentID, condition, since) {
		o.Log.Info("heartbeat watermark kept: the run wrote nothing itself",
			"agent", agentID, "name", name, "system", condition)
		return
	}
	has, sig := o.heartbeatHasWork(ctx, agentID, orgID, condition)
	if !has {
		o.rememberWorkSignature(ctx, agentID, name, "")
	} else if sig != "" {
		o.rememberWorkSignature(ctx, agentID, name, sig)
	}
}

// runWroteSignature answers whether the run since `since` executed an action of
// its own that can have moved the work signature.
//
// Fail-open in the sense of the old behaviour: a system without
// target.SignatureWriter, an unreadable recording — in both cases the answer is
// yes, and the watermark is advanced as before. The stricter answer belongs to
// the systems that can give it.
func (o *Orchestrator) runWroteSignature(ctx context.Context, agentID uuid.UUID, condition string, since time.Time) bool {
	system, _, _ := strings.Cut(condition, ":")
	sys, ok := target.Get(system)
	if !ok {
		return true
	}
	writer, ok := sys.(target.SignatureWriter)
	if !ok {
		return true
	}
	subjects, err := o.Obs.ActionSubjectsSince(ctx, agentID, since)
	if err != nil {
		o.Log.Warn("heartbeat watermark: actions of the run not readable — advancing anyway",
			"agent", agentID, "system", system, "err", err)
		return true
	}
	for _, s := range subjects {
		if writer.WritesWorkSignature(s) {
			return true
		}
	}
	return false
}

// Placement says where an agent is working right now — the host its sandbox
// stands on, including the parked one of a warm agent, which is compute on a
// machine as much as a running one is. ok=false: nothing of this agent is
// standing anywhere, and the last known host is then the one in its latest
// snapshot (that is where its working copy lies).
func (o *Orchestrator) Placement(agentID uuid.UUID) (uuid.UUID, string, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if s := o.sessions[agentID]; s != nil && s.runnerID != uuid.Nil {
		return s.runnerID, s.runnerName, true
	}
	if ws := o.warm[agentID]; ws != nil && ws.sandbox != nil {
		if placed, ok := ws.sandbox.(Placed); ok {
			id, label := placed.Runner()
			return id, label, id != uuid.Nil
		}
	}
	return uuid.Nil, "", false
}

// WakeNow is EnsureRunning without the backoff: a person asked for it.
//
// Whoever presses the button has just changed something — deposited a
// credential, fixed a host, deployed a version — and is waiting for the answer,
// not for a wait that was measured for an unattended retry (#139).
func (o *Orchestrator) WakeNow(agentID uuid.UUID) {
	o.clearWakeFailure(agentID)
	o.EnsureRunning(agentID)
}

// EnsureRunning starts an agent session if none is running (idempotent).
func (o *Orchestrator) EnsureRunning(agentID uuid.UUID) {
	o.mu.Lock()
	if _, active := o.sessions[agentID]; active {
		o.mu.Unlock()
		return
	}
	// Not while the last wake is still fresh in the wrong way. A cause that
	// stops a wake does not change in thirty seconds, and asking anyway costs a
	// runner slot and four lines of recording per attempt (#139). The wait
	// grows with the number of failures and stops at half an hour.
	if t := o.wakeFehler[agentID]; t != nil && time.Now().Before(t.Until) {
		o.mu.Unlock()
		return
	}
	// Hung off the control plane's lifecycle, not off Background: a shutdown
	// thereby also terminates sessions that came into being this way.
	ctx, cancel := context.WithCancel(o.base())
	s := &session{cancel: cancel}
	o.sessions[agentID] = s
	o.mu.Unlock()

	o.nebenlaeufig(func() {
		defer func() {
			o.mu.Lock()
			delete(o.sessions, agentID)
			o.mu.Unlock()
			cancel()
		}()
		if err := o.runAgent(ctx, agentID, s); err != nil && !errors.Is(err, context.Canceled) {
			o.Log.Error("agent-session", "agent", agentID, "err", err)
		}
	})
}

// SandboxDied reports a sandbox that ended without being asked to — the runner
// watches the container and says so (spec/16). Whoever is waiting for this
// agent's daemon stops waiting; for everyone else it is a log line, because a
// sandbox that dies outside a wake has already been given up on.
func (o *Orchestrator) SandboxDied(agentID uuid.UUID, reason string) {
	o.mu.Lock()
	ch := o.dying[agentID]
	o.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- reason:
	default:
	}
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
	o.events.Publish(Event{Type: "agent_status", AgentID: agent.ID.String(), OrgID: agent.OrgID, Data: map[string]string{"status": status}})
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
	// A draft has no first day, so it has no waking phase either. Queued tasks
	// stay where they are — hiring is what releases them (spec/20).
	if agent.Draft() {
		return nil
	}
	if fleetKilled, err := o.Registry.FleetKilled(ctx, agent.OrgID); err != nil || fleetKilled {
		return err
	}
	hasOpen, err := o.Backlog.HasOpen(ctx, agentID)
	if err != nil || !hasOpen {
		return err
	}

	// The LLM credential is chosen BEFORE the sandbox: is the agent's pool value
	// used up or parked right now, then the whole waking phase is postponed
	// instead of a container being started for a run that cannot work. A rate
	// limit thereby becomes a delay and not a failed task — the work stays in
	// the backlog and is picked up when the value is free again.
	cred, credErr := o.llmCredentialFor(ctx, agent)
	if errors.Is(credErr, runtimes.ErrExhausted) {
		payload := map[string]any{"system": "anthropic", "granted": false, "reason": "pool exhausted"}
		var pe *runtimes.Exhausted
		if errors.As(credErr, &pe) && !pe.Until.IsZero() {
			payload["free_at"] = pe.Until
		}
		// Deliberately CHOSEN fields rather than the error object. Nothing that
		// reaches here carries a secret value — the store's errors name the
		// secret, never its contents — but an error assembled deep in the stack
		// is an unbounded string, and a log line is the one place where "it
		// probably contains nothing bad" is not good enough. Whoever adds the
		// error back is widening what can be printed without deciding to.
		o.Log.Info("wake postponed — no LLM credential free",
			"agent", agent.Slug, "runtime", agent.RuntimeID, "free_at", payload["free_at"])
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential, payload)
		return nil
	}
	// No credential at all, for an engine that cannot work without one: then the
	// sandbox stays down too. Starting it anyway is what the first version did,
	// and the runtime inside reported "Not logged in · run /login" — a sentence
	// that sends whoever deposited their token an hour ago looking in exactly
	// the wrong place. The reason is HERE, so it is said here.
	if credErr != nil && engineNeedsCredential(agent.Runtime) {
		if o.noteMissingCredential(agent.ID) {
			// The error is NAMED, not printed — the same decision as in the
			// branch above, and for the same reason: nothing that arrives here
			// carries a secret value today, but an error assembled deep in the
			// stack is an unbounded string, and a log line is where that is not
			// good enough. errKind keeps what a reader needs (which of the
			// cases) and drops what nobody decided to print.
			o.Log.Warn("wake cancelled — the agent reaches no credential",
				"agent", agent.Slug, "engine", agent.Runtime, "runtime", agent.RuntimeID,
				"reason", errKind(credErr))
			_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential, map[string]any{
				"system": agent.Runtime, "granted": false, "reason": noCredentialReason(agent),
			})
		}
		return nil
	}
	s.credRuntime, s.credOrd = cred.RuntimeID, cred.Ord

	o.setStatus(ctx, agent, nil, agents.StatusTriggered)

	// Wake: take over a warm sandbox, otherwise start cold and wait for ready.
	link, sandbox, err := o.acquireSandbox(ctx, agent)
	if err != nil {
		o.setStatus(ctx, agent, nil, agents.StatusSleeping)
		// Into the recording, not only into the log: a wake that fails is the
		// most common thing an operator has to explain, and the reason lives in
		// the process's stderr while they are looking at the agent's page. The
		// runner names what happened (a dead container, a missing image) — that
		// sentence belongs where the question is asked.
		o.noteWakeFailure(agent.ID, err)
		trouble, _ := o.WakeBlocked(agent.ID)
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindLifecycle, map[string]any{
			"status": "wake_failed", "error": err.Error(),
			// How often, and when the next attempt is due — so that the
			// recording answers "is anybody still trying?" without counting
			// entries.
			"failures": trouble.Failures, "retry_at": trouble.Until.UTC().Format(time.RFC3339),
		})
		return fmt.Errorf("wake: %w", err)
	}
	// A wake that worked ends the trouble — including the report of it.
	o.clearWakeFailure(agent.ID)

	// Where this run happens. With one machine it is not a question; with a
	// second one it is the first one an operator asks in front of a run that
	// behaved oddly — and the answer used to exist only in the process's log,
	// if at all.
	o.mu.Lock()
	s.sandbox = sandbox
	o.mu.Unlock()
	if placed, ok := sandbox.(Placed); ok {
		id, label := placed.Runner()
		o.mu.Lock()
		s.runnerID, s.runnerName = id, label
		o.mu.Unlock()
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindLifecycle, map[string]any{
			"status": "sandbox", "runner": id.String(), "runner_name": label,
		})
	}
	// And what stands beside it. Held on the session as well as recorded,
	// because a warm sandbox serves many jobs: the run that asks "which
	// database was I talking to" may be the fifth one on services that came up
	// hours ago, and only the session still knows.
	if withServices, ok := sandbox.(WithServices); ok {
		if running := withServices.Services(); len(running) > 0 {
			o.mu.Lock()
			s.services = running
			o.mu.Unlock()
			_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindService, map[string]any{
				"status": "started", "services": running,
			})
		}
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
			ws := o.parkWarm(agent.ID, link, sandbox)
			// The job is done, the home is what it produced — and for a warm
			// agent nothing else is going to carry it into the store for the
			// next half hour. Off the sleep path: the agent counts as asleep
			// the moment it is parked, not when the scan is through.
			o.syncParkedHome(agent.ID, sandbox, ws)
		} else {
			link.Close()
			// Said before it happens, not after: stopping the sandbox writes
			// the home into the store, and on a grown home that is half a
			// minute in which the agent is doing nothing while the interface
			// claims it is working. The status carries the platform's own work
			// instead of hiding it behind the agent's.
			o.setStatus(context.WithoutCancel(ctx), agent, nil, agents.StatusSecuring)
			// Detached here too: on kill or abort ctx has already expired — the
			// sandbox still has to go.
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			sandbox.Stop(stopCtx)
			cancel()
		}
		o.setStatus(context.WithoutCancel(ctx), agent, nil, final)
	}()

	// Broker the runtime LLM key proactively (never permanently in the sandbox).
	// Nothing stored: push nothing — the runtime reports the missing credential
	// as a task error with an actionable hint (spec/12).
	if credErr == nil {
		o.pushAnthropicKey(ctx, agent, link, cred)
		// Ask the engine what this credential has consumed — once the link is
		// up, cached per credential, never per run (usage.go). No run waits for
		// the answer; it arrives on the message loop and serves the NEXT
		// decision.
		//
		// Asked HERE and not deferred to the end of the waking phase, which is
		// what the first version did: by then the loop that reads the reply has
		// stopped, so the request went out and the answer fell into a closed
		// room. Nothing failed, no figure ever arrived, and the interface
		// simply showed none.
		o.refreshUsage(ctx, link, cred.RuntimeID, cred.Ord)
	}

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
			if errors.Is(err, errDaemonConnection) {
				// The link is dead either way — no point trying to claim another
				// task over it. Reopen this one instead of failing it: the sandbox
				// dropped out from under the agent, that says nothing about
				// whether the work itself was going fine.
				o.requeueAfterDaemonLoss(context.WithoutCancel(ctx), task)
				o.publishTask(task.ID, agent)
				return err
			}
			_, _ = o.Backlog.Complete(context.WithoutCancel(ctx), task.ID, backlog.StateFailed, "", err.Error())
			o.publishTask(task.ID, agent)
			return err
		}
	}
	return ctx.Err()
}

// recordServicesRefused writes the refusal into the recording. The declaration
// travels with it: whoever reads this afterwards needs to see WHICH image was
// asked for, not only that something was.
func (o *Orchestrator) recordServicesRefused(ctx context.Context, agent agents.Agent, want []sandbox.Service, why error) {
	if o.Obs == nil {
		return
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindService, map[string]any{
		"status": "refused", "services": want, "error": why.Error(),
	})
}

// wake starts the sandbox and waits for the daemon's ready message.
func (o *Orchestrator) wake(ctx context.Context, agent agents.Agent) (DaemonLink, Sandbox, error) {
	tok, err := o.Identity.IssueAgentToken(ctx, agent.ID,
		identity.Scope{Audience: "daemon"}, o.DaemonTokenTTL)
	if err != nil {
		return nil, nil, err
	}

	ch := make(chan DaemonLink, 1)
	died := make(chan string, 1)
	o.mu.Lock()
	o.waiting[agent.ID] = ch
	o.dying[agent.ID] = died
	o.mu.Unlock()
	defer func() {
		o.mu.Lock()
		delete(o.waiting, agent.ID)
		delete(o.dying, agent.ID)
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

	// Operational plugin configuration first (intake scope, limits — see
	// pluginenv.go: the plugins that read it run in the sandbox, and an unset
	// allowlist there means "no restriction"), the connection variables after
	// it. That order is the point: the three below are assigned last and can
	// therefore never be shadowed by a pass-through of the same name.
	env := pluginEnv()
	env["COVEY_WS_URL"] = o.PublicWSURL
	env["COVEY_DAEMON_TOKEN"] = tok.Value
	env["COVEY_AGENT_ID"] = agent.ID.String()

	// The allowlist, and it is enforced HERE rather than only where the
	// declaration was typed. The API checks it too, so this one almost never
	// fires — but "almost never" is the wrong bar for the question "which
	// foreign image runs on the runner": a pattern withdrawn after the fact, a
	// declaration written through a path added later, a bundle imported while
	// the check sat in one handler. This is the last gate before a container
	// exists, so it is the one that has to hold.
	//
	// It refuses the wake instead of dropping the service. A sandbox with two
	// of its three services is the state in which an agent reports the wrong
	// defect — and the message names the pattern that would let it run, so the
	// refusal can be answered rather than only read.
	if o.Workplaces != nil && len(agent.Services) > 0 {
		if err := o.Workplaces.CheckServices(ctx, agent.OrgID, agent.Services); err != nil {
			o.recordServicesRefused(ctx, agent, agent.Services, err)
			return nil, nil, fmt.Errorf("the services of this agent are not allowed here: %w", err)
		}
	}

	sandbox, err := o.Provider.Start(ctx, SandboxSpec{
		AgentID:     agent.ID,
		OrgID:       agent.OrgID,
		Image:       agent.SandboxImage,
		RunnerTags:  agent.RunnerTags,
		EgressToken: egressToken,
		Env:         env,
		Services:    agent.Services,
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
			discard(context.WithoutCancel(ctx), sandbox)
			return nil, nil, fmt.Errorf("daemon not ready: %v (%s)", err, msg.Type)
		}
		return link, sandbox, nil
	case reason := <-died:
		// The runner watched the container and says it is gone. Reported
		// instead of waited out: the ReadyTimeout would give the same outcome
		// minutes later and blame the daemon for it.
		discard(context.WithoutCancel(ctx), sandbox)
		return nil, nil, fmt.Errorf("the sandbox did not survive its start: %s", reason)
	case <-time.After(o.ReadyTimeout):
		discard(context.WithoutCancel(ctx), sandbox)
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
func (o *Orchestrator) parkWarm(agentID uuid.UUID, link DaemonLink, sandbox Sandbox) *warmSession {
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
	return ws
}

// syncParkedHome writes the home of a just-parked warm sandbox into the store.
//
// This is the place where the promise of `spec/16-runner.md` is kept for warm
// agents: after every job the home goes into the store. Not on the sleep path
// — a scan of a seven-gigabyte home would otherwise stand between the end of
// the run and the agent counting as asleep — and not more often than
// warmSyncEvery, so an agent on a short heartbeat does not scan its home all
// day.
//
// Three things it refuses to do: sync a sandbox that has meanwhile been taken
// over (the next wake writes its own state), sync one that has been torn down
// (the teardown syncs itself), and hold the caller up.
func (o *Orchestrator) syncParkedHome(agentID uuid.UUID, sandbox Sandbox, ws *warmSession) {
	syncer, ok := sandbox.(HomeSyncer)
	if !ok || ws == nil {
		return // a provider without a store — the mock in the tests, for one
	}
	o.mu.Lock()
	if time.Since(o.lastWarmSync[agentID]) < warmSyncEvery {
		o.mu.Unlock()
		return
	}
	// Noted before the attempt: two runs finishing in quick succession are not
	// two reasons to scan the same home twice.
	o.lastWarmSync[agentID] = time.Now()
	o.mu.Unlock()

	go func() {
		if !o.stillParked(agentID, ws) {
			return
		}
		// Its own context: the run's has just ended, and this is exactly the
		// work that must survive it. Generously bounded — the first sync of a
		// grown home is a full pass.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(o.base()), 30*time.Minute)
		defer cancel()
		if err := syncer.SyncHome(ctx); err != nil {
			// Not a failure of the run, which is long over. But the interval
			// is released again, so the next job tries rather than waiting out
			// the five minutes on a home that is not in the store.
			o.mu.Lock()
			delete(o.lastWarmSync, agentID)
			o.mu.Unlock()
			o.Log.Warn("home of the parked sandbox not synced", "agent", agentID, "err", err)
		}
	}()
}

// discard takes a sandbox down that never became a run — the container died at
// its start, or the daemon never connected. Its home is what was materialised
// into it minutes ago and nothing else, so writing it back would be a full scan
// for a byte-identical result: on a grown home that is half an hour, and it
// stands between the failure and the record of it.
//
// A provider that cannot tell the difference gets the ordinary stop; nothing is
// lost then except the time.
func discard(ctx context.Context, sandbox Sandbox) {
	if d, ok := sandbox.(Discardable); ok {
		d.Discard(ctx)
		return
	}
	sandbox.Stop(ctx)
}

// stillParked: is this exact session still the parked one? A session that has
// been taken over or torn down in the meantime is none of our business.
func (o *Orchestrator) stillParked(agentID uuid.UUID, ws *warmSession) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.warm[agentID] == ws
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
// DropWarm ends a parked warm sandbox now instead of waiting for its idle TTL.
//
// It is what "keep no sandbox warm any more" has to mean. Setting the flag to
// false used to change a column and nothing else: the container stayed up
// until the reaper came round, and on covey.work that made a host
// unupdatable — the update refuses while a sandbox is running, and this one
// never ended by itself.
//
// Does nothing when the agent has no warm sandbox, which is the normal case.
func (o *Orchestrator) DropWarm(agentID uuid.UUID) {
	o.mu.Lock()
	ws := o.warm[agentID]
	if ws != nil {
		delete(o.warm, agentID)
	}
	o.mu.Unlock()
	if ws == nil {
		return
	}
	ws.cancel()
	ws.teardown()
	o.Log.Info("warm sandbox released", "agent", agentID)
}

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

// anthropicCredential picks the LLM credential for one waking phase.
//
// Deliberately BEFORE the sandbox: if the pool has nothing usable right now,
// there is no point starting a container just to have the runtime fail on a
// missing credential. The caller then postpones the wake.
//
// Which credential and how it reaches the sandbox comes from the ENGINE's
// declaration (daemon.RuntimeCredential) via the agent's runtime — never from a
// list here. That list was the reason a second provider could not be added
// without touching the orchestrator (spec/18).
func (o *Orchestrator) llmCredentialFor(ctx context.Context, agent agents.Agent) (llmCredential, error) {
	if o.Runtimes == nil || agent.RuntimeID == nil {
		return llmCredential{}, errNoRuntime
	}
	sig := runtimes.Signals{Reported: o.reportedUtilisation}
	if o.Obs != nil {
		sig.Usage = o.Obs.CredentialUsage
	}
	p, err := o.Runtimes.Pick(ctx, agent.OrgID, agent.ID, *agent.RuntimeID, agent.Runtime, sig)
	if err != nil {
		return llmCredential{}, err
	}
	if p.Ord < 0 {
		return llmCredential{}, errNoRuntime // an engine that needs none (the mock)
	}
	// TrimSpace catches copy-and-paste whitespace/newlines.
	return llmCredential{Token: strings.TrimSpace(p.Value), EnvVar: p.EnvVar, Path: p.Path,
		RuntimeID: p.RuntimeID, Ord: p.Ord, Label: p.Label}, nil
}

// errNoRuntime: nothing to broker — either the agent has no runtime assigned or
// its engine needs no credential. Whether that is a defect depends on the
// engine, and only the engine knows: the mock in the test suite works without
// one, Claude Code does not (engineNeedsCredential).
var errNoRuntime = errors.New("no LLM credential to broker")

// engineNeedsCredential: does a run without a brokered credential stand a
// chance? Read from the engine's declaration, never from a list here — that
// list is exactly what made a second provider impossible to add (spec/18).
// An unknown engine gets the benefit of the doubt: a wake refused because of a
// name we do not know would be worse than a run that fails and says why.
func engineNeedsCredential(engine string) bool {
	d, ok := daemon.Describe(engine)
	return ok && d.NeedsCredential()
}

// noCredentialInterval is how often a missing credential is worth saying again.
// Long enough not to fill the recording of an unattended instance, short enough
// that whoever comes back to the interface after a coffee finds the reason
// rather than silence.
const noCredentialInterval = 15 * time.Minute

// noteMissingCredential reports whether this agent's missing credential should
// be put on the record now. In memory and not in the database: it throttles a
// log line, and after a restart saying it once more is the right answer anyway.
func (o *Orchestrator) noteMissingCredential(agentID uuid.UUID) bool {
	o.noCredMu.Lock()
	defer o.noCredMu.Unlock()
	if last, ok := o.noCredNoted[agentID]; ok && time.Since(last) < noCredentialInterval {
		return false
	}
	if o.noCredNoted == nil {
		o.noCredNoted = map[uuid.UUID]time.Time{}
	}
	o.noCredNoted[agentID] = time.Now()
	return true
}

// errKind names an error instead of printing it: for the cases this path knows,
// the case; for everything else the type. It exists so that the log line about
// a missing credential says which of the handful of reasons it was without
// carrying an arbitrary string out of the depths of the stack into the log —
// the store's errors name a secret's KEY and never its content, and this keeps
// it that way even if somebody later wraps more into one of them.
func errKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, errNoRuntime):
		return "no workplace assigned"
	case errors.Is(err, runtimes.ErrNotFound):
		return "workplace without a credential"
	case errors.Is(err, secrets.ErrNotFound):
		return "the deposited value is gone"
	}
	var wrong *runtimes.WrongEngine
	if errors.As(err, &wrong) {
		return "the seat belongs to another engine"
	}
	// The TYPE, asked for where the type lives. fmt.Sprintf("%T", err) would
	// say the same thing today and is one character away from saying the error
	// itself — a guarantee that lives in a formatting verb is the weakest kind
	// there is. reflect leaves no verb to change (issue #109).
	return reflect.TypeOf(err).String()
}

// noCredentialReason names the case in the recording, in the words of whoever
// has to fix it. The two cases have different remedies, and "no credential" for
// both would send half the readers to the wrong screen.
func noCredentialReason(agent agents.Agent) string {
	if agent.RuntimeID == nil {
		return "the agent has no workplace — assign one under Runtimes"
	}
	return "the workplace has no usable credential — deposit anthropic_api_key or claude_code_oauth_token under Secrets"
}

// How long a rejected value stays parked.
//
// Two lengths, because the two rejections mean different things: a rate limit
// passes by itself, and an hour is roughly the granularity at which it is worth
// trying again. An expired or revoked token does NOT recover — it stays parked
// long enough that nothing keeps running into it, but not forever, because
// depositing a new value lifts the cooldown anyway (see builtin.Put) and a
// wrong diagnosis should not park a value for good.
const (
	cooldownRateLimit = time.Hour
	cooldownRejected  = 24 * time.Hour
	// cooldownSeatWindow: a subscription seat whose window is used up.
	//
	// An hour, not the five the window nominally lasts. The window ROLLS, so it
	// frees up gradually rather than at a stroke, and the message even states
	// when ("resets 4:10pm") — parking until then would be exact. Parsing that
	// timestamp is the obvious refinement and deliberately not done here: it
	// carries a timezone and an implied date, and a wrong reading would park a
	// working seat for hours.
	//
	// An hour is the self-correcting choice. Is the window still full, the next
	// run parks the seat again; is it free, the agent returns to it. The cost of
	// being wrong is one run, and meanwhile the agent works on another seat.
	cooldownSeatWindow = time.Hour
)

// noteCredentialRejection reads the runtime's error text and parks the value
// the run was using.
//
// Matched on text, because that is what the API gives us — Claude Code passes
// the provider's message through, and the control plane never sees an HTTP
// status of its own. The adapter (spec/12) already recognises the same phrases
// in order to explain them; here they decide which value is out of play.
// rejectionCooldown is the rule itself, apart from the plumbing: how long an
// error text parks the value it occurred on. Zero means "not a credential
// problem" — the run failed for some other reason and the value stays in play.
//
// Kept as a pure function because THIS is the part worth checking. Whether the
// cooldown then reaches the database is a matter of two lines; whether a
// rate limit is told apart from a revoked token decides whether an agent waits
// an hour or a day.
func rejectionCooldown(errText string) (time.Duration, string) {
	if errText == "" {
		return 0, ""
	}
	// Matched case-insensitively on purpose. These are the provider's own
	// sentences, and their casing and punctuation are not ours to rely on — the
	// first version of this rule matched "Rate limit" exactly and would have
	// missed "rate limit" for no reason anybody could have foreseen.
	t := strings.ToLower(errText)
	has := func(needles ...string) bool {
		for _, n := range needles {
			if strings.Contains(t, n) {
				return true
			}
		}
		return false
	}
	switch {
	// The credential itself is bad: expired, revoked, wrong. It does not
	// recover on its own.
	case has("invalid bearer token", "oauth token has expired", "authentication_error"):
		return cooldownRejected, runtimes.ReasonError

	// A SUBSCRIPTION seat that has used up its window. This is the common case
	// for a fleet on seats and the one the first version of this rule missed
	// entirely: the message is "You've hit your session limit · resets 4:10pm
	// (UTC)" — no "rate limit" anywhere in it, so nothing matched and the seat
	// was handed out again fifteen minutes later, run after run.
	//
	// Deliberately without the apostrophe: "you've" appears with a typographic
	// and an ASCII one depending on where the text travelled through.
	case has("hit your session limit", "hit your weekly limit", "hit your opus limit",
		"usage limit reached"):
		return cooldownSeatWindow, runtimes.ReasonLimit

	// A rate limit on the API side.
	case has("rate_limit", "rate limit", "429"):
		return cooldownRateLimit, runtimes.ReasonError
	}
	return 0, ""
}

func (o *Orchestrator) noteCredentialRejection(ctx context.Context, agent agents.Agent, s *session, errText string) {
	if s == nil || s.credRuntime == uuid.Nil || o.Runtimes == nil {
		return
	}
	until, reason := rejectionCooldown(errText)
	if until == 0 {
		return
	}
	if err := o.Runtimes.Cooldown(ctx, s.credRuntime, s.credOrd,
		time.Now().Add(until), reason); err != nil {
		o.Log.Warn("credential could not be parked",
			"runtime", s.credRuntime, "ord", s.credOrd, "err", err)
		return
	}
	o.Log.Warn("credential rejected — value parked",
		"agent", agent.Slug, "runtime", s.credRuntime, "ord", s.credOrd, "until", until)
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": "anthropic", "granted": false, "reason": "rejected",
			"runtime": s.credRuntime, "ord": s.credOrd, "cooldown_secs": int(until.Seconds())})
}

// llmCredential is the credential of one waking phase plus its origin: which
// secret and which value of it. The origin is what makes consumption
// attributable and a rejection assignable to exactly one value.
type llmCredential struct {
	Token string
	// Exactly one of these, from the engine's declaration: the value arrives as
	// an environment variable, or as a file at this path in the agent home.
	EnvVar string
	Path   string
	// Where it came from, so consumption is attributable and a rejection lands
	// on exactly one credential.
	RuntimeID uuid.UUID
	Ord       int
	Label     string
}

// pushAnthropicKey hands the already picked credential to the daemon. Never
// permanently in the sandbox — it arrives per waking phase and with a TTL.
func (o *Orchestrator) pushAnthropicKey(ctx context.Context, agent agents.Agent, link DaemonLink, cred llmCredential) {
	_ = o.sendMsg(ctx, link, daemon.TypeInjectCredentials, daemon.InjectCredentials{
		System: "anthropic", Granted: true, Token: cred.Token,
		EnvVar: cred.EnvVar, Path: cred.Path,
		TTLSecs: int(o.DaemonTokenTTL.Seconds()),
	})
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": "anthropic", "granted": true, "proactive": true,
			"runtime": cred.RuntimeID, "ord": cred.Ord, "label": cred.Label})
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
		FROM agents WHERE org_id=$1 AND id<>$2 AND NOT killed AND hired_at IS NOT NULL
		ORDER BY created_at`, self.OrgID, self.ID)
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

func (o *Orchestrator) publishTask(taskID uuid.UUID, agent agents.Agent) {
	o.events.Publish(Event{Type: "task", AgentID: agent.ID.String(), OrgID: agent.OrgID,
		Data: map[string]string{"task_id": taskID.String()}})
}

// processTask drives a task through triage → working → done/blocked/failed.
func (o *Orchestrator) processTask(ctx context.Context, agent agents.Agent, link DaemonLink, task backlog.Task, s *session) error {
	taskID := task.ID
	o.setStatus(ctx, agent, &taskID, agents.StatusTriage)
	o.publishTask(taskID, agent)

	// What this job runs against, on the job. The wake already recorded the
	// start of the services — but that event hangs off the AGENT, and a warm
	// sandbox serves job after job on services that came up hours earlier. So
	// the run that produced a result nobody can reproduce carries the answer
	// itself: these images, these ids, this job.
	o.mu.Lock()
	running := s.services
	o.mu.Unlock()
	if len(running) > 0 && o.Obs != nil {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindService, map[string]any{
			"status": "running", "services": running,
		})
	}

	// Triage: check the wiki (spec/05) and compile the config (M2). Relevant
	// pages (vector hits) plus the compact index of the whole wiki.
	memCtx := ""
	if entries, err := o.Memory.Query(ctx, agent.ID, task.Title+" "+task.Body, 5); err == nil {
		memCtx = memory.FormatForPrompt(entries)
	}
	// The index deliberately reaches further than the 40 pages it used to: it
	// only carries slugs now (FormatIndexForPrompt), so full coverage costs less
	// than the truncated list with titles did — and coverage is the point, a
	// page the agent does not see there it writes a second time. The cap stays
	// as a backstop against a wiki that has run away.
	if idx, err := o.Memory.List(ctx, agent.ID, wikiIndexLimit); err == nil {
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
	// organization's currently enabled plugins (including manifest uploads) and
	// the agent's current ACCESS.md, not the state at the time the config was
	// compiled.
	var actionTools []daemon.ActionTool
	// Welche Zielsysteme dieser Agent wirklich erreicht — DocsForAgent ist auf
	// beiden Seiten fail-closed (von der Organisation aktiviert UND in der
	// ACCESS.md des Agenten). Der Plattform-Repo-Abschnitt weiter unten haengt
	// daran.
	grantedSystems := map[string]bool{}
	if o.Targets != nil {
		if docs, err := o.Targets.DocsForAgent(ctx, agent.OrgID, agent.ID); err == nil {
			texts := make([]string, 0, len(docs))
			for _, d := range docs {
				texts = append(texts, d.Doc)
				grantedSystems[d.System] = true
				actionTools = append(actionTools, daemon.ActionTool{Name: d.System, Description: d.Doc})
			}
			if section := agents.TargetDocs(texts); section != "" {
				compiled += "\n\n" + section
			}
		}
	}
	// Hiring only for whoever has the access for it: `- system: covey scope:
	// agents:write` in ACCESS.md. The same check the actions themselves run, and
	// that is the point — otherwise an agent would read in its prompt that it can
	// draft colleagues and then run into a refusal in the control plane, a
	// capability by suggestion, which is the worst kind (spec/20).
	mayDraft := o.mayDraftAgents(ctx, agent)
	if mayDraft {
		compiled += "\n\n" + agents.HiringDoc
	}
	// Und dasselbe fuer die andere Haelfte: `scope: agents:review` schaltet das
	// Lesen und Vorschlagen frei (spec/21). Zwei Scopes, zwei Abschnitte — wer
	// nur begutachten darf, liest nichts ueber das Entwerfen und umgekehrt.
	// Und dasselbe für die Dienste: Wer `scope: services:write` trägt, liest,
	// wie er die Compose-Datei seines Projekts hochfährt — und wer ihn nicht
	// trägt, liest nichts davon. Eine Fähigkeit anzudeuten, die dann abgewiesen
	// wird, ist die schlechteste Sorte (spec/20).
	if o.mayStartServices(ctx, agent) {
		compiled += "\n\n" + agents.ServicesDoc
	}
	mayReview := o.mayReviewAgents(ctx, agent)
	if mayReview {
		compiled += "\n\n" + agents.ReviewDoc
		// Die dritte Schicht: der eigene Quelltext, gepinnt auf den Stand, den
		// diese Instanz laeuft (spec/21). Drei Bedingungen, und die dritte ist
		// die, die beim ersten Bau fehlte:
		//
		//  1. Es gibt eine Adresse. Voreinstellung ist das Projekt, aus dem
		//     dieses Programm stammt (buildinfo.SourceRepo) — die Plattform
		//     weiss, wo ihr Quelltext liegt, und hat nie danach fragen muessen.
		//     Eine Organisation, die ihre Befunde im Haus behalten will, traegt
		//     ihr eigenes Repository ein; wer die Schicht gar nicht will, setzt
		//     das Zielsystem auf "-" (repoAus).
		//  2. Der Agent darf begutachten (mayReview, siehe oben).
		//  3. Er hat dieses Zielsystem WIRKLICH in seiner ACCESS.md.
		//
		// Ohne (3) stuende im Prompt „you may READ it — check it out and search
		// it like any other repository", und der Broker wiese den Checkout
		// gleich darauf ab: Faehigkeit durch Andeutung, dieselbe, die der
		// Abschnitt darueber fuer das Entwerfen ausdruecklich vermeidet. Das
		// Stammdatum allein ist die halbe Einrichtung — die andere Haelfte ist
		// eine Zeile in der ACCESS.md von covey Doctor.
		//
		// Der Scope INNERHALB des Systems bleibt Sache dieser Zeile: welche
		// Aktionen sie traegt, steht ohnehin im Zielsystem-Abschnitt des
		// Prompts, der schon auf die Scopes des Agenten zugeschnitten ist.
		var repoSystem, repoProject string
		if err := o.Pool.QueryRow(ctx,
			"SELECT platform_repo_system, platform_repo_project FROM organizations WHERE id=$1",
			agent.OrgID).Scan(&repoSystem, &repoProject); err == nil {
			repoSystem, repoProject = agents.PlatformRepo(repoSystem, repoProject)
			if grantedSystems[repoSystem] {
				ref, istTag := buildinfo.Ref()
				if section := agents.PlatformRepoDoc(repoSystem, repoProject, ref, istTag); section != "" {
					compiled += "\n\n" + section
				}
			}
		}
	}
	// The platform's own meta actions (board, notes, wiki, delegation) are not a
	// target system, but they are callable in exactly the same way — so on the
	// MCP route they belong in the tool list too. Their description is the
	// platform protocol, which stands in the prompt anyway.
	//
	// The list used to be tied to having at least one target system, and for the
	// People department that is exactly wrong: its whole job runs through these
	// actions, and it reaches no external system at all. So whoever may draft
	// agents gets the tool even with an otherwise empty list.
	if len(actionTools) > 0 || mayDraft || mayReview {
		doc := agents.CoveyActionsDoc
		if mayDraft {
			doc += "\n\n" + agents.HiringDoc
		}
		if mayReview {
			doc += "\n\n" + agents.ReviewDoc
		}
		actionTools = append(actionTools, daemon.ActionTool{Name: "covey", Description: doc})
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
		maxTurns = agents.DefaultMaxTurns
	}
	if err := o.sendMsg(ctx, link, daemon.TypeInjectConfig, daemon.InjectConfig{
		SystemPrompt: compiled,
		Runtime:      agent.Runtime,
		Model:        agent.Model,
		Effort:       agent.Effort,
		AllowedTools: o.RuntimeTools,
		MaxTurns:     maxTurns,
		ActionTools:  actionTools,
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
			return fmt.Errorf("%w: %w", errDaemonConnection, err)
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
		o.events.Publish(Event{Type: "recording", AgentID: agent.ID.String(), OrgID: agent.OrgID,
			Data: map[string]any{"task_id": taskID.String(), "kind": kind}})
		return false, nil

	case daemon.TypeCost:
		c, err := daemon.DecodePayload[daemon.Cost](msg)
		if err != nil {
			return false, nil
		}
		// Booked against the pool value this waking phase runs on — without that
		// attribution no limit per value is measurable and no utilisation
		// showable (spec/04).
		_ = o.Obs.AddCost(ctx, agent.ID, &taskID, c.USD, observability.Tokens{
			Input: c.InputTokens, Output: c.OutputTokens,
			CacheRead: c.CacheReadTokens, CacheCreation: c.CacheCreationTokens,
		}, c.Model, s.credRuntime, s.credOrd)
		return false, o.enforceBudget(ctx, agent, link, taskID, s)

	case daemon.TypeUsageReport:
		rep, err := daemon.DecodePayload[daemon.UsageReport](msg)
		if err != nil {
			return false, nil
		}
		o.noteUsage(rep, s.credRuntime, s.credOrd)
		return false, nil

	case daemon.TypeRequestCredential:
		req, err := daemon.DecodePayload[daemon.RequestCredential](msg)
		if err != nil {
			return false, nil
		}
		resp := o.brokerCredential(ctx, agent, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectCredentials, resp)

	case daemon.TypeRequestSecret:
		req, err := daemon.DecodePayload[daemon.RequestSecret](msg)
		if err != nil {
			return false, nil
		}
		resp := o.brokerSecret(ctx, agent, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectSecret, resp)

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

	case daemon.TypeRequestTool:
		req, err := daemon.DecodePayload[daemon.RequestTool](msg)
		if err != nil {
			return false, nil
		}
		resp := o.toolRequest(ctx, agent, taskID, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectTool, resp)

	case daemon.TypeRequestHiring:
		req, err := daemon.DecodePayload[daemon.RequestHiring](msg)
		if err != nil {
			return false, nil
		}
		resp := o.hiring(ctx, agent, taskID, req)
		return false, o.sendMsg(ctx, link, daemon.TypeInjectHiring, resp)

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
		o.publishTask(taskID, agent)
		return true, nil

	case daemon.TypeTaskDone:
		d, err := daemon.DecodePayload[daemon.TaskDone](msg)
		if err != nil {
			return true, err
		}
		// The hard signal: the API itself rejected the credential. That beats
		// every estimate the limit makes — park the value before the next run
		// walks into the same wall.
		o.noteCredentialRejection(ctx, agent, s, d.Error)
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
		// A run that ended without a result must not keep its wake-up: the work
		// it was woken for is still lying there. A successful run does the
		// converse and advances its watermark past any target-system action it
		// performed itself, so that action cannot wake it again.
		if t, err := o.Backlog.Get(ctx, taskID); err == nil {
			heartbeatRun := t.Origin == "heartbeat"
			if !heartbeatRun && strings.HasPrefix(t.Origin, originContinuation+":") {
				n, ancestorErr := o.Backlog.AncestorsWithOrigin(ctx, taskID, "heartbeat")
				if ancestorErr != nil {
					// Whether the chain descends from a heartbeat is not
					// determinable: treat it as one. Both branches then merely
					// touch the watermark of a heartbeat with this task's title
					// — releasing wakes it once too often, keeping it silences
					// work that is lying there. Erring towards the former.
					o.Log.Warn("heartbeat ancestry not determinable — treating the run as a heartbeat run",
						"task", taskID, "err", ancestorErr)
				}
				heartbeatRun = ancestorErr != nil || n > 0
			}
			if heartbeatRun {
				if state == backlog.StateFailed || d.Status == "escalated" {
					o.releaseWorkSignature(ctx, agent.ID, t.Title)
				} else {
					// The beginning of the whole chain, not of this segment: the
					// refresh asks what the RUN wrote itself, and a continuation
					// carries on what an earlier task started. Unreadable (zero
					// time) means "look at everything" — fail-open towards the
					// previous behaviour of advancing unconditionally.
					since, chainErr := o.Backlog.ChainStart(ctx, taskID)
					if chainErr != nil {
						o.Log.Warn("start of the run not determinable — watermark over the full history",
							"task", taskID, "err", chainErr)
						since = time.Time{}
					}
					o.refreshWorkSignature(ctx, agent.ID, agent.OrgID, t.Title, since)
				}
			}
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
		o.publishTask(taskID, agent)
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
		o.publishTask(target, agent)
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
		o.publishTask(target, agent)
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
		// The chain is exhausted without a result — the state that woke the
		// agent is unfinished and has to wake it again.
		o.releaseWorkSignature(ctx, agent.ID, task.Title)
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindLifecycle,
			map[string]string{"status": "task_escalated", "reason": "max_turns", "continuations": strconv.Itoa(depth)})
		o.publishTask(taskID, agent)
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
	o.publishTask(taskID, agent)
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

// errDaemonConnection marks a lost sandbox connection mid-task (container
// killed, Docker under resource pressure, network blip) — an infrastructure
// event, not a verdict on the agent's work. Observed in practice: several
// agents' daemon links dropped within the same second under concurrent
// sandbox load, and every one of those tasks landed on StateFailed with no
// automatic way back — real backlog items sitting there until someone
// happens to notice and retries them by hand via the task API. Treated the
// same way as errBudgetExceeded: reopen, don't fail.
var errDaemonConnection = errors.New("daemon connection lost")

// maxDaemonLossRetries is where "reopen, don't fail" stops. Requeueing without
// a limit is right for the sporadic case above, and wrong for the
// reproducible one: a broken sandbox image after a deploy, an OOM on container
// start, an agent config that reliably tears the container down. There the
// task runs open → ClaimNext → sandbox dies → open in circles, each round
// paying for a full sandbox start, and nothing in the system shows that this
// is stuck rather than working.
//
// Five in a row is the point where a blip stops being a plausible explanation.
// The counter lives on the task (backlog_tasks.daemon_retries), not in this
// process: a control-plane restart is one of the things that produces these
// losses, and an in-memory count would be back at zero right afterwards.
const maxDaemonLossRetries = 5

// givesUpAfterDaemonLoss decides, for a task that has already lost its sandbox
// connection `previous` times and just lost it once more, whether to fail it
// instead of requeueing. It returns the failure text along with the verdict, so
// the count that led to it cannot drift apart from the message that explains
// it.
//
// Separate from requeueAfterDaemonLoss because this is where the off-by-one
// lives: `previous` is the count as it stood when this run CLAIMED the task,
// i.e. it does not yet include the loss being handled.
func givesUpAfterDaemonLoss(previous int) (bool, string) {
	losses := previous + 1
	if losses < maxDaemonLossRetries {
		return false, ""
	}
	return true, fmt.Sprintf(
		"sandbox connection lost %d times in a row — giving up instead of requeueing again", losses)
}

// requeueAfterDaemonLoss puts a task whose sandbox connection dropped back into
// the backlog — or gives up on it, once that has happened
// maxDaemonLossRetries times in a row.
func (o *Orchestrator) requeueAfterDaemonLoss(ctx context.Context, task backlog.Task) {
	if giveUp, why := givesUpAfterDaemonLoss(task.DaemonRetries); giveUp {
		// The error text names the infrastructure cause, so this stays
		// distinguishable in the backlog from a task the agent itself ran into
		// the ground — that difference is the whole reason errDaemonConnection
		// exists.
		_, _ = o.Backlog.Complete(ctx, task.ID, backlog.StateFailed, "", why)
		o.Log.Warn("task failed after repeated sandbox connection losses",
			"task", task.ID, "agent", task.AgentID, "losses", task.DaemonRetries+1)
		return
	}
	_, _ = o.Backlog.ReopenAfterDaemonLoss(ctx, task.ID, "daemon connection lost — retrying")
}

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
		if found.Draft() {
			return fail(fmt.Sprintf("agent %q has not been hired yet — no delegation", slug))
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
	o.publishTask(created.ID, targetAgent)
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
	// Systems whose secret only RAISES what they may do (CredentialsOptional,
	// e.g. an NVD API key that lifts the public rate limit): a missing secret
	// must not deny — the plugin works without one, only more slowly. Whatever
	// is stored is passed on.
	if d, ok := target.Describe(req.System); ok && d.CredentialsOptional {
		token, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_token")
		baseURL, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_url")
		ca := o.brokerCA(ctx, agent, req.System)
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": true, "optional": true,
				"authenticated": token != "", "ttl_secs": int(o.DaemonTokenTTL.Seconds())})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
			Granted: true, Token: token, BaseURL: baseURL, CA: ca, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
	}
	// MCP servers carry their endpoint in the config; auth is optional. A
	// missing token therefore does NOT deny — the server may be reachable
	// without auth. The URL secret remains an optional override.
	if kind == "mcp" {
		token, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_token")
		baseURL, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_url")
		ca := o.brokerCA(ctx, agent, req.System)
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": true, "kind": "mcp",
				"ttl_secs": int(o.DaemonTokenTTL.Seconds())})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
			Granted: true, Token: token, BaseURL: baseURL, CA: ca, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
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
		Granted: true, Token: token, BaseURL: baseURL, CA: o.brokerCA(ctx, agent, req.System),
		TTLSecs: int(o.DaemonTokenTTL.Seconds())}
}

// brokerCA resolves the optional trust anchor of a target system,
// <system>_ca. Optional in the strict sense: almost every endpoint is signed by
// a public authority, and a missing secret is the normal case rather than a
// failure — only an internal API server behind a company CA needs one, and
// then its absence shows up as the TLS error it is.
//
// It travels with the credential, not as an action parameter. A parameter goes
// through the model's call, the guard-rail subject and the recording of every
// single action; a certificate has no business in any of the three, and a
// plugin kind that cannot dial for itself (wasm, manifest) could never use one
// there anyway — the host builds the trust store, so the host has to be the one
// that gets it.
func (o *Orchestrator) brokerCA(ctx context.Context, agent agents.Agent, system string) string {
	ca, _ := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, system+"_ca")
	return ca
}

// brokerSecret resolves one custom, agent-scoped secret for the
// {{secret:<key>}} placeholder substitution in action params (spec/04): the
// agent's own secret (PutAgent) before an org-wide one explicitly assigned to
// it (Assign) — same precedence as brokerCredential's <system>_token/_url
// lookups. Unlike brokerCredential there is no ACCESS.md/target-system gate to
// check first: the key is not a target system, and the explicit
// PutAgent/Assign onto this specific agent already IS the authorization —
// nothing reaches an agent it was not put there for.
func (o *Orchestrator) brokerSecret(ctx context.Context, agent agents.Agent, req daemon.RequestSecret) daemon.InjectSecret {
	value, err := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.Key)
	if err != nil {
		reason := "no secret named " + req.Key + " set for this agent"
		if !errors.Is(err, secrets.ErrNotFound) {
			reason = "secret lookup failed"
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": "secret:" + req.Key, "granted": false, "reason": reason})
		return daemon.InjectSecret{RequestID: req.RequestID, Key: req.Key, Granted: false, Reason: reason}
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": "secret:" + req.Key, "granted": true})
	return daemon.InjectSecret{RequestID: req.RequestID, Key: req.Key, Granted: true, Value: value}
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
				o.events.Publish(Event{Type: "guardrail", AgentID: agent.ID.String(), OrgID: agent.OrgID,
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
		o.events.Publish(Event{Type: "guardrail", AgentID: agent.ID.String(), OrgID: agent.OrgID,
			Data: map[string]string{"action": req.Action, "decision": "denied"}})
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
			Reason: "forbidden by guard rail " + verdict.Rule.Pattern}

	case guardrails.RequireApproval:
		v := o.approvalGate(ctx, agent, taskID, req.Action, json.RawMessage(req.Params), "")
		switch {
		case v.Error != "":
			return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied", Reason: v.Error}
		case v.Approved:
			return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "approved"}
		}
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "pending",
			ApprovalID: v.ApprovalID, CorrelationKey: v.CorrelationKey}

	default:
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindApproval,
			map[string]any{"action": req.Action, "decision": "auto-allow"})
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "approved"}
	}
}

// gateVerdict ist die Antwort des Freigabe-Gates: entweder lag eine erteilte,
// unverbrauchte Freigabe vor (Approved) — oder es wurde eine angelegt, und die
// Aufgabe muss auf einen Menschen warten (CorrelationKey).
type gateVerdict struct {
	Approved       bool
	ApprovalID     string
	CorrelationKey string
	// Error steht, wenn die Freigabe gar nicht erst angelegt werden konnte.
	// Fail-closed: der Aufrufer verbietet dann.
	Error string
}

// approvalGate ist der require_approval-Zweig der Guard-Rails.
//
// Bewusst herausgelöst und nicht beim Zielsystem-Pfad gelassen: dieselbe
// Mechanik trägt die Meta-Actions der Plattform (spec/21). Dort lehnte eine
// require_approval-Regel bisher hart ab — „requires an approval and cannot be
// performed unattended". Das ist eine Leitplanke, die für eine Klasse von
// Aktionen still zu einem Verbot wird, und damit eine Governance-Oberfläche,
// die über sich selbst die Unwahrheit sagt: wer die Regel setzt, meint
// „jemand schaut drauf" und bekommt „geht nicht".
//
// Die Freigabe ist EINMALIG verbrauchbar (approvals.used). Eine erteilte
// Freigabe ist die Antwort auf eine Handlung, keine Lizenz auf die Aktion.
// binding schnürt eine Freigabe auf EINEN Gegenstand fest (leer = auf die
// Aktion). Die Meta-Actions setzen es ausnahmslos (bindingOf in hiring.go): eine
// erteilte Freigabe ist die Antwort auf die Parameter, die ein Mensch gelesen
// hat, und nicht auf die Aktion als solche — sonst wäre die Freigabe für Lauf A
// die Eintrittskarte für Lauf B, und die für `slug: "helper"` die für
// `slug: "backdoor"`. Nur der Zielsystem-Pfad kommt noch mit leerem binding
// hierher; dort ist die Aktion selbst der Gegenstand.
func (o *Orchestrator) approvalGate(ctx context.Context, agent agents.Agent, taskID uuid.UUID,
	action string, params json.RawMessage, binding string) gateVerdict {

	// Eine unverbrauchte Freigabe für genau diese Aktion? Dann verbrauchen.
	var approvalID uuid.UUID
	err := o.Pool.QueryRow(ctx, `UPDATE approvals SET used=TRUE
		WHERE id = (SELECT id FROM approvals
			WHERE agent_id=$1 AND action=$2 AND status='approved' AND NOT used
			  AND ($3='' OR params->>'binding' = $3)
			ORDER BY decided_at DESC LIMIT 1)
		RETURNING id`, agent.ID, action, binding).Scan(&approvalID)
	if err == nil {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindApproval,
			map[string]any{"action": action, "decision": "approved", "approval_id": approvalID.String()})
		return gateVerdict{Approved: true, ApprovalID: approvalID.String()}
	}
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	appr, err := o.Obs.CreateApproval(ctx, agent.OrgID, agent.ID, &taskID, action, params)
	if err != nil {
		return gateVerdict{Error: err.Error()}
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindApproval,
		map[string]any{"action": action, "decision": "pending", "approval_id": appr.ID.String()})
	o.events.Publish(Event{Type: "approval", AgentID: agent.ID.String(), OrgID: agent.OrgID,
		Data: map[string]string{"approval_id": appr.ID.String(), "action": action}})
	return gateVerdict{ApprovalID: appr.ID.String(), CorrelationKey: "approval:" + appr.ID.String()}
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
		o.events.Publish(Event{Type: "agent_status", AgentID: agentID.String(), OrgID: agent.OrgID,
			Data: map[string]string{"status": "killed"}})
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

	// Und jetzt warten, bis die abgebrochene Arbeit auch wirklich aufgehört
	// hat. Abbrechen ist ein Signal; eine Sitzung merkt es erst, wenn sie
	// wieder an ihrem Kontext vorbeikommt, und schreibt bis dahin weiter — in
	// das Home des Agenten, in die Aufzeichnung, in die Datenbank.
	//
	// Mit Frist, denn das Warten darf nicht das neue Hängen sein: eine Sitzung,
	// die in einem Netzaufruf ohne eigene Frist steckt, hielte sonst das
	// Herunterfahren der ganzen Plattform auf. Wer die Frist reißt, steht im
	// Log — das ist die Auskunft, mit der man ihn beim nächsten Mal findet.
	fertig := make(chan struct{})
	go func() {
		o.laufend.Wait()
		close(fertig)
	}()
	select {
	case <-fertig:
	case <-time.After(shutdownGrace):
		o.Log.Warn("shutdown: something is still running after the grace period",
			"grace", shutdownGrace)
	}
}

// shutdownGrace: so lange wartet das Herunterfahren auf die eigene, bereits
// abgebrochene Arbeit. Großzügig genug für einen Abbau, der noch einen
// Container stoppt, und kurz genug, dass ein Deploy nicht daran hängen bleibt.
const shutdownGrace = 30 * time.Second

// nebenlaeufig startet etwas, worauf das Herunterfahren wartet. Jede
// Nebenläufigkeit des Orchestrators geht hier durch — eine, die daran vorbei
// gestartet wird, ist genau die, die niemand mehr einholt.
func (o *Orchestrator) nebenlaeufig(f func()) {
	o.laufend.Add(1)
	go func() {
		defer o.laufend.Done()
		f()
	}()
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
		o.publishTask(task.ID, agent)
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
	o.publishTask(task.ID, agent)
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
	o.publishTask(task.ID, agent)
	return "created", nil
}

// WakeTrouble is what an agent that cannot be woken looks like from outside:
// how often it has failed, what it said, and when the next attempt is due.
//
// It exists because "sleeping" was the only word the interface had for this.
// An agent that has failed 900 times looked exactly like one waiting for work,
// and the reason lived four clicks deep in the raw events (#139). Whoever
// looks at the org chart has to see the difference.
type WakeTrouble struct {
	// Failures counts consecutive failed wakes; a successful one clears it.
	Failures int `json:"failures"`
	// Err is the last reason, as the runner phrased it.
	Err string `json:"error,omitempty"`
	// Since is when it started failing, Until when the next attempt is due.
	Since time.Time `json:"since"`
	Until time.Time `json:"until"`
}

// wakeBackoff is how long to wait after n consecutive failures.
//
// It grows and it stops growing: half an hour is long enough that a broken
// agent costs nothing, and short enough that a repair is noticed without
// anybody pressing anything. The first retry stays quick — a host that was
// briefly away is the common case, and it deserves the fast path.
func wakeBackoff(n int) time.Duration {
	stufen := []time.Duration{
		30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
		10 * time.Minute, 20 * time.Minute, 30 * time.Minute,
	}
	if n < 1 {
		n = 1
	}
	if n > len(stufen) {
		n = len(stufen)
	}
	return stufen[n-1]
}

// noteWakeFailure records a failed wake and says when the next one may happen.
func (o *Orchestrator) noteWakeFailure(agentID uuid.UUID, err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.wakeFehler == nil {
		o.wakeFehler = map[uuid.UUID]*WakeTrouble{}
	}
	t := o.wakeFehler[agentID]
	if t == nil {
		t = &WakeTrouble{Since: time.Now()}
		o.wakeFehler[agentID] = t
	}
	t.Failures++
	t.Err = err.Error()
	t.Until = time.Now().Add(wakeBackoff(t.Failures))
}

// clearWakeFailure forgets the trouble — after a wake that worked, and after a
// person asked for one by hand: whoever presses the button has just changed
// something and is waiting for the answer, not for a backoff to expire.
func (o *Orchestrator) clearWakeFailure(agentID uuid.UUID) {
	o.mu.Lock()
	defer o.mu.Unlock()
	delete(o.wakeFehler, agentID)
}

// WakeBlocked reports whether this agent is inside its backoff, and what for.
func (o *Orchestrator) WakeBlocked(agentID uuid.UUID) (*WakeTrouble, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	t := o.wakeFehler[agentID]
	if t == nil {
		return nil, false
	}
	kopie := *t
	return &kopie, time.Now().Before(t.Until)
}

// WakeTroubles is the same for everyone at once — what the agent list needs in
// order to stop calling this "sleeping".
func (o *Orchestrator) WakeTroubles() map[uuid.UUID]WakeTrouble {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make(map[uuid.UUID]WakeTrouble, len(o.wakeFehler))
	for id, t := range o.wakeFehler {
		out[id] = *t
	}
	return out
}
