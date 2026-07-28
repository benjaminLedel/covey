// Package orchestrator ist die Control-Plane-Seite des Daemon-Protokolls:
// Dispatch-Loop (LISTEN/NOTIFY + Tick), Agent-Sessions (seriell, eine Aufgabe
// zur Zeit), Secrets-Broker-Entscheidungen, Guard-Rail-Enforcement, Kill-Switch.
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
	"covey/internal/secrets"
	"covey/internal/target"
	targetstore "covey/internal/target/store"
)

// defaultMaxTurns ist der Runaway-Guard je Runtime-Lauf, wenn der Agent
// kein eigenes Turn-Limit gesetzt hat (agents.max_turns = 0).
const defaultMaxTurns = 30

// DaemonLink abstrahiert die bidirektionale Verbindung zu einem Sandbox-Daemon.
// Der HTTP-Layer implementiert sie über WebSocket; Tests in-process.
type DaemonLink interface {
	Send(ctx context.Context, msg daemon.Message) error
	Receive(ctx context.Context) (daemon.Message, error)
	Close() error
}

type Options struct {
	Pool           *pgxpool.Pool
	Registry       *agents.Registry
	Backlog        *backlog.Store
	Obs            *observability.Store
	Rails          *guardrails.Store
	Secrets        secrets.Store
	Identity       identity.Provider
	Memory         *memory.Store
	Targets        *targetstore.Store
	Egress         *egress.Store
	Provider       SandboxProvider
	PublicWSURL    string // ws://…/api/daemon/ws — von Sandboxen erreichbar
	DaemonTokenTTL time.Duration
	TickInterval   time.Duration
	// WikiMaintenanceInterval taktet den Wiki-Konsolidierungs-Pass (spec/05:
	// aufgabenunabhängig, nicht im Hot-Path). 0 → Default.
	WikiMaintenanceInterval time.Duration
	ReadyTimeout            time.Duration
	Log                     *slog.Logger
}

type Orchestrator struct {
	Options

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
	waiting  map[uuid.UUID]chan DaemonLink
	// warm hält geparkte Sandbox-Sessions warm-geschalteter Agenten am Leben,
	// während der Agent schläft (Link offen, Container läuft weiter). Der nächste
	// Wake übernimmt sie, statt kalt hochzufahren.
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

// warmIdleTTL: nach so langer Idle-Zeit wird eine geparkte warme Sandbox doch
// abgebaut, damit sie nicht unbegrenzt Ressourcen hält.
const warmIdleTTL = 30 * time.Minute

// warmSession ist eine zwischen Wach-Phasen offen gehaltene Sandbox samt Daemon-
// Link. Im Idle drained eine Goroutine die Heartbeats des Daemons (sonst würde
// die WS als tot gelten). Der nächste Wake bricht den Drain ab und übernimmt.
type warmSession struct {
	link         DaemonLink
	sandbox      Sandbox
	lastUsed     time.Time
	cancel       context.CancelFunc // stoppt den Drain
	done         chan struct{}      // geschlossen, wenn der Drain beendet ist
	dead         atomic.Bool        // Link während des Idle gestorben
	teardownOnce sync.Once
}

func (ws *warmSession) teardown() {
	ws.teardownOnce.Do(func() {
		ws.link.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = ws.sandbox.Stop(stopCtx)
	})
}

// warmLink umschließt einen Daemon-Link für warme Sessions. Grund: bei
// coder/websocket schließt ein Read, dessen Context gecancelt wird, die
// Verbindung — der Idle-Drain (Cancel zum Handoff) würde den warmen Link also
// killen. Eine Pump-Goroutine liest den inneren Link mit Hintergrund-Context und
// verteilt die Nachrichten über einen Channel; Receive(ctx) liest nur aus dem
// Channel, ein Cancel betrifft damit nie die eigentliche Verbindung.
type warmLink struct {
	inner     DaemonLink
	incoming  chan daemon.Message
	pumpErr   chan error
	stopPump  context.CancelFunc
	closeOnce sync.Once
}

func newWarmLink(inner DaemonLink) *warmLink {
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
				return daemon.Message{}, fmt.Errorf("daemon-link geschlossen")
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

// Events liefert den SSE-Broadcaster für Live-Updates der Admin-UI.
func (o *Orchestrator) Events() *Broadcaster { return o.events }

type session struct {
	agent  agents.Agent
	cancel context.CancelFunc
	link   DaemonLink
	killed bool
}

// Run startet den Dispatch-Loop: billig, dauerhaft, kein LLM (spec/03).
// Wake-Quellen: NOTIFY (Event) und der periodische Tick.
func (o *Orchestrator) Run(ctx context.Context) error {
	// Startup-Reconcile: verwaiste in_progress-Aufgaben (Sandbox mit dem letzten
	// Prozess verschwunden) zurück auf open, sonst hingen sie nach Crash/Deploy
	// dauerhaft. Muss vor dem ersten tick laufen, damit sie sofort neu greifen.
	if n, err := o.Backlog.RequeueOrphaned(ctx); err != nil {
		o.Log.Warn("startup-reconcile fehlgeschlagen", "err", err)
	} else if n > 0 {
		o.Log.Info("startup-reconcile: verwaiste Aufgaben requeued", "count", n)
	}
	go o.listenLoop(ctx)
	go o.wikiMaintenanceLoop(ctx)
	go o.warmReaperLoop(ctx)
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

// listenLoop lauscht auf Postgres NOTIFY (Wake-Events der Stores).
func (o *Orchestrator) listenLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if err := o.listenOnce(ctx); err != nil && ctx.Err() == nil {
			o.Log.Warn("listen/notify unterbrochen, reconnect", "err", err)
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

// tick ist der periodische "was liegt an?"-Impuls: findet Agenten mit
// offener Arbeit und weckt sie — reine SQL-Entscheidung, kein Modell.
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

// wikiMaintenanceLoop taktet den Wiki-Konsolidierungs-Pass (spec/05): der
// Lint-/Dedup-Pass ist aufgabenunabhängig und läuft nicht im Hot-Path des
// done-Schritts, sondern hier gebündelt und gedrosselt.
func (o *Orchestrator) wikiMaintenanceLoop(ctx context.Context) {
	ticker := time.NewTicker(o.WikiMaintenanceInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := o.ConsolidateWikis(ctx); err != nil {
				o.Log.Warn("wiki-wartung fehlgeschlagen", "err", err)
			} else if n > 0 {
				o.Log.Info("wiki-wartung: Duplikate verschmolzen", "count", n)
			}
		}
	}
}

// ConsolidateWikis konsolidiert die Wikis aller Agenten, deren Seiten sich seit
// dem letzten Durchlauf geändert haben (beim ersten Lauf: alle mit Seiten), und
// gibt die Gesamtzahl der Verschmelzungen zurück. Vom Wartungs-Ticker und für
// die manuelle Auslösung (UI) genutzt.
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
			o.Log.Warn("wiki-konsolidierung fehlgeschlagen", "agent", id, "err", err)
			continue
		}
		total += n
	}
	o.wikiSweepMu.Lock()
	o.lastWikiSweep = time.Now()
	o.wikiSweepMu.Unlock()
	return total, nil
}

// fireHeartbeats legt fällige Heartbeat-Einträge (HEARTBEAT.md, materialisiert
// in agent_heartbeats) als Backlog-Aufgabe an. Fällig ist die Intervall-Form
// nach Ablauf des Intervalls seit last_fired_at, die Tageszeit-Form einmal pro
// Tag ab der konfigurierten Uhrzeit (Serverzeit). Kill-Switches greifen wie
// beim Wake. Dedup: solange eine nicht-terminale Aufgabe gleichen Titels mit
// origin='heartbeat' existiert, wird keine neue angelegt — der Lauf gilt
// trotzdem als "gefeuert", damit nach deren Abschluss kein sofortiger
// Nachschlag kommt, sondern der reguläre Zeitplan weiterläuft.
func (o *Orchestrator) fireHeartbeats(ctx context.Context) {
	tx, err := o.Pool.Begin(ctx)
	if err != nil {
		o.Log.Warn("heartbeat tx", "err", err)
		return
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, `SELECT h.agent_id, a.org_id, h.name, h.task_body, h.only_if,
			EXISTS (SELECT 1 FROM backlog_tasks t
				WHERE t.agent_id=h.agent_id AND t.origin='heartbeat' AND t.title=h.name
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
		pending            bool
	}
	var dues []due
	for rows.Next() {
		var d due
		if err := rows.Scan(&d.agentID, &d.orgID, &d.name, &d.body, &d.onlyIf, &d.pending); err != nil {
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
			o.Log.Warn("heartbeat fortschreiben", "agent", d.agentID, "name", d.name, "err", err)
			return
		}
	}
	if err := tx.Commit(ctx); err != nil {
		o.Log.Warn("heartbeat commit", "err", err)
		return
	}
	// Aufgaben erst nach dem Commit anlegen — Create feuert NOTIFY und weckt
	// den Agenten. Schlägt ein Create fehl, entfällt genau ein Lauf (best-effort).
	// Die nur-wenn:-Prüfung läuft ebenfalls nach dem Commit (Netzwerk-I/O gehört
	// nicht in die Transaktion); last_fired_at ist dann schon fortgeschrieben —
	// ein übersprungener Lauf zählt als gelaufen, der Zeitplan pollt regulär weiter.
	for _, d := range dues {
		if d.pending {
			o.Log.Info("heartbeat übersprungen: aufgabe noch offen", "agent", d.agentID, "name", d.name)
			continue
		}
		if d.onlyIf != "" && !o.heartbeatHasWork(ctx, d.agentID, d.orgID, d.onlyIf) {
			o.Log.Info("heartbeat übersprungen: keine arbeit", "agent", d.agentID, "name", d.name, "system", d.onlyIf)
			continue
		}
		if _, err := o.Backlog.Create(ctx, d.orgID, d.agentID, d.name, d.body, "heartbeat", 0); err != nil {
			o.Log.Warn("heartbeat-aufgabe anlegen", "agent", d.agentID, "name", d.name, "err", err)
		}
	}
}

// heartbeatHasWork prüft die nur-wenn:-Bedingung eines fälligen Heartbeats:
// das Zielsystem-Plugin meldet über target.WorkChecker, ob Arbeit vorliegt.
// Der Bedingungs-Wert ist "<system>" oder "<system>:<kind>" — Letzteres gatet
// eine einzelne Arbeits-Art (z. B. gitlab:mr für den Review-Loop, gitlab:issues
// für die Issue-Triage) über target.KindWorkChecker, sodass zwei Heartbeats
// desselben Systems getrennt feuern statt über einen gemeinsamen Boolean.
// Secrets und Plugin-Lookup laufen über den System-Namen (vor dem ":").
// Die Secrets löst die Control Plane selbst auf — das Credential verlässt sie
// nicht. Fail-open: lässt sich die Bedingung nicht prüfen (Plugin ohne
// WorkChecker, fehlende Secrets, Verbindungsfehler), feuert der Heartbeat
// regulär — eine kaputte Bedingung darf keine Arbeit liegen lassen.
func (o *Orchestrator) heartbeatHasWork(ctx context.Context, agentID, orgID uuid.UUID, condition string) bool {
	system, kind, _ := strings.Cut(condition, ":")
	sys, ok := target.Get(system)
	if !ok {
		o.Log.Warn("nur-wenn: unbekanntes zielsystem — feuere trotzdem", "system", system)
		return true
	}
	checker, ok := sys.(target.WorkChecker)
	if !ok {
		o.Log.Warn("nur-wenn: zielsystem kann arbeit nicht vorab prüfen — feuere trotzdem", "system", system)
		return true
	}
	var cred target.Credential
	if d, _ := target.Describe(system); !d.NoCredentials {
		token, err := o.Secrets.Resolve(ctx, orgID, agentID, system+"_token")
		if err != nil {
			o.Log.Warn("nur-wenn: secret fehlt — feuere trotzdem", "system", system, "err", err)
			return true
		}
		baseURL, err := o.Secrets.Resolve(ctx, orgID, agentID, system+"_url")
		if err != nil {
			o.Log.Warn("nur-wenn: secret fehlt — feuere trotzdem", "system", system, "err", err)
			return true
		}
		cred = target.Credential{BaseURL: baseURL, Token: token}
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var (
		has bool
		err error
	)
	if kc, ok := checker.(target.KindWorkChecker); ok && kind != "" {
		has, err = kc.HasWorkKind(cctx, cred, kind)
	} else {
		if kind != "" {
			o.Log.Warn("nur-wenn: zielsystem kennt keinen unterscope — prüfe gesamt", "system", system, "kind", kind)
		}
		has, err = checker.HasWork(cctx, cred)
	}
	if err != nil {
		o.Log.Warn("nur-wenn: prüfung fehlgeschlagen — feuere trotzdem", "system", system, "kind", kind, "err", err)
		return true
	}
	return has
}

// EnsureRunning startet eine Agent-Session, falls keine läuft (idempotent).
func (o *Orchestrator) EnsureRunning(agentID uuid.UUID) {
	o.mu.Lock()
	if _, active := o.sessions[agentID]; active {
		o.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
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

// AttachDaemon übergibt eine authentifizierte Daemon-Verbindung an die
// wartende Session. Blockiert nicht: ohne wartende Session wird abgelehnt.
func (o *Orchestrator) AttachDaemon(agentID uuid.UUID, link DaemonLink) error {
	o.mu.Lock()
	ch := o.waiting[agentID]
	o.mu.Unlock()
	if ch == nil {
		return fmt.Errorf("keine session wartet auf agent %s", agentID)
	}
	select {
	case ch <- link:
		return nil
	default:
		return fmt.Errorf("agent %s hat bereits eine verbindung", agentID)
	}
}

func (o *Orchestrator) setStatus(ctx context.Context, agent agents.Agent, taskID *uuid.UUID, status string) {
	if err := o.Registry.SetStatus(ctx, agent.ID, status); err != nil {
		o.Log.Warn("status setzen", "agent", agent.ID, "err", err)
		return
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, taskID, observability.KindLifecycle,
		map[string]string{"status": status})
	o.events.Publish(Event{Type: "agent_status", AgentID: agent.ID.String(), Data: map[string]string{"status": status}})
}

// runAgent ist eine komplette Wach-Phase: Sandbox hoch, Aufgaben seriell
// abarbeiten, Sandbox runter. sleeping → triggered → (triage → working)* → sleeping.
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

	// Wake: warme Sandbox übernehmen, sonst kalt hochfahren und auf ready warten.
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
		// Warm & sauber eingeschlafen: Sandbox + Link am Leben lassen (im Idle
		// gedraint). Sonst — oder bei kill/Abbruch — Compute abbauen.
		if agent.WarmSandbox && !s.killed && ctx.Err() == nil {
			o.parkWarm(agent.ID, link, sandbox)
		} else {
			link.Close()
			stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			sandbox.Stop(stopCtx)
			cancel()
		}
		o.setStatus(context.WithoutCancel(ctx), agent, nil, final)
	}()

	// Runtime-LLM-Key proaktiv brokern (nie dauerhaft in der Sandbox).
	o.pushAnthropicKey(ctx, agent, link)

	for ctx.Err() == nil {
		if killed, _ := o.isKilled(ctx, agent); killed {
			s.killed = true
			_ = o.sendMsg(ctx, link, daemon.TypeKill, map[string]string{})
			return nil
		}
		task, err := o.Backlog.ClaimNext(ctx, agentID)
		if errors.Is(err, backlog.ErrNotFound) {
			// Warm: KEIN Sleep — sonst räumt coveyd Dev-Server/Browser ab und
			// beendet sich. Der Daemon idlet in seiner Receive-Schleife weiter.
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
				return nil // Aufgabe wurde bereits wieder geöffnet, Agent pausiert
			}
			_, _ = o.Backlog.Complete(context.WithoutCancel(ctx), task.ID, backlog.StateFailed, "", err.Error())
			o.publishTask(task.ID, agent.ID)
			return err
		}
	}
	return ctx.Err()
}

// wake startet die Sandbox und wartet auf die ready-Meldung des Daemons.
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

	// Per-Sandbox-Egress-Token: der Proxy identifiziert den Agenten darüber.
	// Rotiert bei jedem Wake; nur der Hash wird gespeichert.
	egressToken := ""
	if o.Egress != nil {
		buf := make([]byte, 32)
		if _, err := rand.Read(buf); err == nil {
			egressToken = hex.EncodeToString(buf)
			if err := o.Egress.SetAgentToken(ctx, agent.ID, egress.HashToken(egressToken)); err != nil {
				o.Log.Warn("egress-token setzen fehlgeschlagen", "agent", agent.ID, "err", err)
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
		// Erste Nachricht muss ready sein.
		readyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		msg, err := link.Receive(readyCtx)
		if err != nil || msg.Type != daemon.TypeReady {
			link.Close()
			sandbox.Stop(context.WithoutCancel(ctx))
			return nil, nil, fmt.Errorf("daemon nicht ready: %v (%s)", err, msg.Type)
		}
		return link, sandbox, nil
	case <-time.After(o.ReadyTimeout):
		sandbox.Stop(context.WithoutCancel(ctx))
		return nil, nil, fmt.Errorf("daemon hat sich nicht verbunden (timeout %s)", o.ReadyTimeout)
	case <-ctx.Done():
		sandbox.Stop(context.WithoutCancel(ctx))
		return nil, nil, ctx.Err()
	}
}

// acquireSandbox übernimmt für warm-geschaltete Agenten eine geparkte Sandbox
// (kein kalter Start), sonst fährt es eine frische hoch (wake).
func (o *Orchestrator) acquireSandbox(ctx context.Context, agent agents.Agent) (DaemonLink, Sandbox, error) {
	if agent.WarmSandbox {
		if ws := o.takeWarm(agent.ID); ws != nil {
			o.Log.Info("warme sandbox übernommen", "agent", agent.ID)
			return ws.link, ws.sandbox, nil
		}
	}
	link, sandbox, err := o.wake(ctx, agent)
	if err != nil {
		return nil, nil, err
	}
	// Warm: den frischen Link in die Pump-Hülle wickeln, damit ihn der Idle-Drain
	// später gefahrlos übernehmen kann.
	if agent.WarmSandbox {
		link = newWarmLink(link)
	}
	return link, sandbox, nil
}

// parkWarm hält Link + Sandbox nach dem Einschlafen offen und drained im
// Hintergrund die Daemon-Heartbeats, bis der nächste Wake übernimmt oder der
// Reaper abräumt. Stirbt der Link im Idle, wird die Sandbox sofort abgebaut.
func (o *Orchestrator) parkWarm(agentID uuid.UUID, link DaemonLink, sandbox Sandbox) {
	drainCtx, cancel := context.WithCancel(context.Background())
	ws := &warmSession{link: link, sandbox: sandbox, lastUsed: time.Now(), cancel: cancel, done: make(chan struct{})}
	o.mu.Lock()
	// Eine evtl. noch registrierte Vorgänger-Session desselben Agenten weichen.
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
					return // gesunder Handoff (takeWarm/Reaper) — Link nicht anfassen
				}
				// Link im Idle gestorben (Container-Crash o. ä.): abräumen.
				ws.dead.Store(true)
				o.mu.Lock()
				if o.warm[agentID] == ws {
					delete(o.warm, agentID)
				}
				o.mu.Unlock()
				ws.teardown()
				o.Log.Warn("warme sandbox im idle verloren", "agent", agentID, "err", err)
				return
			}
			// Heartbeat/Idle-Nachricht verwerfen — es läuft keine Aufgabe.
		}
	}()
}

// takeWarm zieht eine geparkte Session aus dem Cache, stoppt ihren Drain und gibt
// sie zurück. Ist der Link inzwischen gestorben, liefert es nil (→ kalter Start).
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

// evictWarm baut eine geparkte warme Sandbox sofort ab (Kill/Disable). kill=true
// schickt vorher TypeKill (sofortiges Ende), sonst TypeSleep (sauber). No-op,
// wenn der Agent keine geparkte Session hat.
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

// warmReaperLoop baut geparkte warme Sandboxen ab, die zu lange idle waren.
func (o *Orchestrator) warmReaperLoop(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			o.reapIdleWarm(ctx)
		}
	}
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
		// Sauber einschlafen lassen (coveyd räumt Dev-Server ab und beendet sich),
		// dann Compute abbauen.
		_ = o.sendMsg(ctx, ws.link, daemon.TypeSleep, map[string]string{})
		ws.teardown()
		o.Log.Info("warme sandbox nach idle abgebaut", "ttl", warmIdleTTL)
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
	// Der Secret-Name bestimmt den Credential-Typ und damit die Runtime-Env:
	// anthropic_api_key → ANTHROPIC_API_KEY, claude_code_oauth_token (Abo, via
	// `claude setup-token`) → CLAUDE_CODE_OAUTH_TOKEN. Nicht am Token-Präfix
	// raten — der Name ist die verbindliche Absicht.
	key, err := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, "anthropic_api_key")
	envVar := "ANTHROPIC_API_KEY"
	if err != nil {
		key, err = o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, "claude_code_oauth_token")
		envVar = "CLAUDE_CODE_OAUTH_TOKEN"
	}
	if err != nil {
		return // kein Credential hinterlegt — die Runtime meldet das als Task-Fehler
	}
	key = strings.TrimSpace(key) // Copy-&-Paste-Whitespace/Newline abfangen
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

// teamSection lädt die Mitarbeiter-Profile der Organisation und baut daraus
// den Team-Abschnitt des System-Prompts. Fehler sind nicht fatal — dann
// arbeitet der Agent ohne Mitarbeiterverzeichnis. Die Plattform-Kennungen
// sind generisch (Map system → kennung); Anzeige-Labels kommen aus den
// Zielsystem-Plugins der Organisation, unbekannte Systeme behalten ihren
// Schlüssel. supervisorID (agents.supervisor_id, nil = keiner) markiert den
// Vorgesetzten — der Empfänger von Merge Requests und Eskalationen.
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
	// Org-weit konfigurierte Profilfelder: key → Label, in Definitions-
	// Reihenfolge — nur definierte Felder erscheinen im Verzeichnis.
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

// agentTeamSection baut das Verzeichnis der KI-Kollegen für den Prompt von
// `self`: alle anderen (nicht gekillten) Agenten der Organisation, ihre
// GitLab-/Zielsystem-Kennungen, Zuständigkeiten und Abteilung. Agenten aus
// derselben Abteilung wie `self` werden als eigenes Team markiert — so findet
// z. B. ein Entwickler-Agent den QA-Agenten seines Teams, um ihm den Merge
// Request zum Review zu übergeben. Läuft wie teamSection zur Dispatch-Zeit.
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

// processTask fährt eine Aufgabe durch triage → working → done/blocked/failed.
func (o *Orchestrator) processTask(ctx context.Context, agent agents.Agent, link DaemonLink, task backlog.Task, s *session) error {
	taskID := task.ID
	o.setStatus(ctx, agent, &taskID, agents.StatusTriage)
	o.publishTask(taskID, agent.ID)

	// Triage: Wiki prüfen (spec/05) und Config kompilieren (M2). Relevante
	// Seiten (Vektor-Treffer) plus der kompakte Index des gesamten Wikis.
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
	compiled := cfg.CompiledPrompt
	if compiled == "" {
		compiled = agents.CompilePrompt(nil)
	}
	// Zielsystem-Doku zur Dispatch-Zeit anhängen — sie spiegelt die aktuell
	// aktivierten Plugins der Organisation (inkl. Manifest-Uploads), nicht
	// den Stand beim Kompilieren der Config.
	if o.Targets != nil {
		if docs, err := o.Targets.EnabledDocsForAgent(ctx, agent.OrgID, agent.ID); err == nil {
			if section := agents.TargetDocs(docs); section != "" {
				compiled += "\n\n" + section
			}
		}
	}
	// Team-Verzeichnis ebenfalls zur Dispatch-Zeit: die Mitarbeiter-Profile
	// (Zuständigkeiten, GitLab-Usernames) sagen dem Agenten, wem er in
	// Zielsystemen etwas übergibt — z. B. ein Issue zum Testen zuweist.
	if section := o.teamSection(ctx, agent.OrgID, agent.SupervisorID); section != "" {
		compiled += "\n\n" + section
	}
	// KI-Kollegen dazu: die anderen Agenten der Organisation, damit ein Agent
	// Arbeit an den passenden Kollegen übergeben kann (z. B. der Entwickler
	// seinen MR an den QA-Agenten aus seinem Team) — Abteilung markiert das
	// eigene Team.
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
		// blocked→working: Wiederaufnahme über die native Runtime-Session (spec/12).
		assign.ResumeSessionID = *task.RuntimeSessionID
		assign.ResumeInput = *task.ResumeInput
	}
	if err := o.sendMsg(ctx, link, daemon.TypeAssignTask, assign); err != nil {
		return err
	}

	// Nachrichtenschleife bis blocked/task_done.
	for {
		msg, err := link.Receive(ctx)
		if err != nil {
			return fmt.Errorf("daemon-verbindung: %w", err)
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

// storeActionArtifact zieht ein base64-Bild aus einem action-Event, legt es als
// Blob ab und ersetzt es im Payload durch die Referenz (screenshot=<blob-id>) —
// so bleiben die Bytes aus dem JSONB der Recording-Timeline heraus (spec/06).
// Bei jedem Fehler bleibt der Payload unverändert (fail-open fürs Recording).
func (o *Orchestrator) storeActionArtifact(ctx context.Context, agent agents.Agent, taskID uuid.UUID, payload json.RawMessage) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(payload, &m) != nil {
		return payload
	}
	b64, ok := m["image_b64"].(string)
	if !ok || b64 == "" {
		return payload
	}
	// Aufzeichnungstiefe (spec/06): Screenshots nur bei 'full' speichern. Sonst
	// das Bild verwerfen — die Aktion selbst bleibt im Recording. Control-Plane-
	// seitige Durchsetzung ist die letzte Instanz (fail-closed: im Zweifel nicht
	// speichern).
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

// handleDaemonMessage verarbeitet eine Daemon-Nachricht; true = Aufgabe beendet.
func (o *Orchestrator) handleDaemonMessage(ctx context.Context, agent agents.Agent, link DaemonLink, taskID uuid.UUID, msg daemon.Message, s *session) (bool, error) {
	switch msg.Type {
	case daemon.TypeHeartbeat:
		return false, nil

	case daemon.TypeEvent:
		ev, err := daemon.DecodePayload[daemon.Event](msg)
		if err != nil {
			return false, nil
		}
		kind := observability.KindRuntime
		payload := json.RawMessage(ev.Payload)
		if ev.Kind == "action" {
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
		state := backlog.StateDone
		if d.Status == "failed" {
			state = backlog.StateFailed
		}
		result := d.Result
		if d.Status == "escalated" {
			result = "ESKALIERT: " + result
			if name, err := o.Registry.SupervisorName(ctx, agent.ID); err == nil && name != "" {
				result += " (an " + name + ")"
			}
		}
		if _, err := o.Backlog.Complete(ctx, taskID, state, result, d.Error); err != nil {
			return true, err
		}
		// done-Schritt: Gelerntes ins Wiki einspeisen (spec/05). Die
		// Konsolidierung (Beinahe-Duplikate verschmelzen) läuft aufgabenunabhängig
		// im getakteten Wartungs-Job, nicht hier im Hot-Path.
		if d.Memory != "" {
			_ = o.Memory.Ingest(ctx, agent.ID, d.Memory, map[string]string{"task_id": taskID.String()})
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
		// Ensure + Move + Cleanup leerer Agenten-Spalten in einer Transaktion.
		stage, err := o.Backlog.SetTaskStageByName(ctx, agent.ID, target, ss.Stage)
		if err != nil {
			o.Log.Warn("set_stage: task konnte nicht verschoben werden", "err", err)
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
			// Allgemeingültige Erkenntnis: sofort ins Gedächtnis, nicht erst
			// über das memory-Feld beim Abschluss (M7).
			_ = o.Memory.Ingest(ctx, agent.ID, n.Content, map[string]string{
				"task_id": target.String(), "origin": "proactive"})
		} else {
			// Aufgabenbezogene Notiz: an die Aufgabe selbst.
			if _, err := o.Backlog.AddNote(ctx, target, "agent", n.Content); err != nil {
				o.Log.Warn("note: konnte nicht gespeichert werden", "err", err)
				return false, nil
			}
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &target, observability.KindLifecycle,
			map[string]string{"status": "note", "scope": n.Scope})
		o.publishTask(target, agent.ID)
		return false, nil

	default:
		o.Log.Warn("unerwartete daemon-nachricht", "type", msg.Type)
		return false, nil
	}
}

// enforceBudget prüft Budget-Deckel (Agent-Feld + budget_limit-Guard-Rails).
// Überschreitung pausiert den Agenten (fail-closed) und stoppt den Lauf.
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
		map[string]any{"rule": "budget_limit", "limit_usd": limit, "spent_usd": summary.TotalUSD, "action": "agent pausiert"})
	_ = o.Registry.SetKilled(ctx, agent.ID, true)
	s.killed = true
	_, _ = o.Backlog.Reopen(ctx, taskID, "budget überschritten — agent pausiert")
	_ = o.sendMsg(ctx, link, daemon.TypeKill, map[string]string{"reason": "budget"})
	return fmt.Errorf("%w (%.4f ≥ %.4f USD)", errBudgetExceeded, summary.TotalUSD, limit)
}

// errBudgetExceeded markiert den Budget-Stopp: Aufgabe ist wieder offen,
// der Agent pausiert — kein failed-Abschluss.
var errBudgetExceeded = errors.New("budget überschritten")

// brokerWiki bedient die Wiki-Tools des Agenten (covey/wiki_*, spec/05) gegen
// den Memory-Store der Control Plane.
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
		// Alle Seiten für die Home-Arbeitskopie (spec/05).
		entries, err := o.Memory.List(ctx, agent.ID, 1000)
		if err != nil {
			return fail(err.Error())
		}
		type page struct {
			Slug  string   `json:"slug"`
			Title string   `json:"title"`
			Body  string   `json:"body"`
			Links []string `json:"links"`
		}
		pages := make([]page, 0, len(entries))
		for _, e := range entries {
			pages = append(pages, page{e.Slug, e.Title, e.Content, e.Links})
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
			return fail("seite nicht gefunden")
		}
		return ok(e)
	case "write":
		e, err := o.Memory.Write(ctx, agent.ID, req.Slug, req.Title, req.Body, "agent")
		if err != nil {
			return fail(err.Error())
		}
		return ok(map[string]string{"slug": e.Slug, "title": e.Title})
	case "delete":
		// Wiki-Pflege: überflüssige/zusammengeführte Seite entfernen. Agent-gescopt
		// im Store, der Agent kann also nur im eigenen Wiki löschen (spec/05).
		if err := o.Memory.DeleteBySlug(ctx, agent.ID, req.Slug); err != nil {
			return fail("seite nicht gefunden")
		}
		return ok(map[string]string{"slug": req.Slug, "deleted": "true"})
	default:
		return fail("unbekannte wiki-operation: " + req.Op)
	}
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + " …"
}

// brokerCredential ist die Broker-Entscheidung (spec/04): Berechtigung laut
// ACCESS.md, Guard-Rails, dann kurzlebige Durchreichung aus dem SecretStore.
func (o *Orchestrator) brokerCredential(ctx context.Context, agent agents.Agent, req daemon.RequestCredential) daemon.InjectCredentials {
	deny := func(reason string) daemon.InjectCredentials {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": false, "reason": reason})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System, Granted: false, Reason: reason}
	}
	ok, err := o.Registry.HasAccess(ctx, agent.ID, req.System, "")
	if err != nil || !ok {
		return deny("kein Zugang laut ACCESS.md")
	}
	// Plugin-Aktivierung ist ein zentraler Enforcement-Punkt (fail-closed):
	// ein deaktiviertes oder unbekanntes Zielsystem bekommt keine Credentials.
	var kind string
	if o.Targets != nil {
		if _, err := o.Targets.System(ctx, agent.OrgID, req.System); err != nil {
			return deny("zielsystem nicht aktiviert: " + req.System)
		}
		kind, _ = o.Targets.Kind(ctx, agent.OrgID, req.System)
	}
	rules, err := o.Rails.List(ctx, agent.OrgID)
	if err != nil {
		return deny("guard-rails nicht lesbar (fail-closed)")
	}
	if v := guardrails.Evaluate(rules, agent.ID, req.System); v.Decision == guardrails.Deny {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindGuardrail,
			map[string]any{"rule": "deny_system", "system": req.System, "pattern": v.Rule.Pattern})
		return deny("durch Guard-Rail verboten")
	}
	// Lokale Systeme (NoCredentials, z. B. das dev-Plugin) brauchen keine
	// Secrets — die Aktionen laufen komplett in der Sandbox. Die Prüfungen
	// oben (ACCESS.md, Aktivierung, Guard-Rails) gelten trotzdem; gewährt
	// wird ein leeres Credential.
	if d, ok := target.Describe(req.System); ok && d.NoCredentials {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
			map[string]any{"system": req.System, "granted": true, "local": true})
		return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
			Granted: true, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
	}
	// MCP-Server tragen ihren Endpoint in der Config; Auth ist optional. Ein
	// fehlendes Token verweigert daher NICHT — der Server kann ohne Auth
	// erreichbar sein. URL-Secret bleibt optionaler Override.
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
		return deny("kein Secret hinterlegt oder zugewiesen: " + req.System + "_token")
	}
	baseURL, err := o.Secrets.Resolve(ctx, agent.OrgID, agent.ID, req.System+"_url")
	if err != nil {
		return deny("kein Secret hinterlegt oder zugewiesen: " + req.System + "_url")
	}
	_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindCredential,
		map[string]any{"system": req.System, "granted": true, "ttl_secs": int(o.DaemonTokenTTL.Seconds())})
	return daemon.InjectCredentials{RequestID: req.RequestID, System: req.System,
		Granted: true, Token: token, BaseURL: baseURL, TTLSecs: int(o.DaemonTokenTTL.Seconds())}
}

// brokerTarget reicht die Definition eines Manifest-Plugins in die Sandbox —
// nur für aktivierte Custom-Systeme der Organisation (fail-closed). Kompilierte
// Plugins kennt der Daemon selbst und fragt hier gar nicht erst an.
func (o *Orchestrator) brokerTarget(ctx context.Context, agent agents.Agent, req daemon.RequestTarget) daemon.InjectTarget {
	deny := func(reason string) daemon.InjectTarget {
		return daemon.InjectTarget{RequestID: req.RequestID, System: req.System, Granted: false, Reason: reason}
	}
	if o.Targets == nil {
		return deny("kein zielsystem-store konfiguriert")
	}
	kind, raw, err := o.Targets.BrokeredDefinition(ctx, agent.OrgID, req.System)
	if err != nil {
		return deny("zielsystem nicht verfügbar: " + err.Error())
	}
	return daemon.InjectTarget{RequestID: req.RequestID, System: req.System, Granted: true,
		Kind: kind, Manifest: raw}
}

// decideAction ist die zentrale Policy-Entscheidung für eine Aktion:
// allow (auto), deny (Guard-Rail) oder pending (Approval-Gate, spec/06).
// Eine bereits erteilte, unverbrauchte Freigabe wird konsumiert.
func (o *Orchestrator) decideAction(ctx context.Context, agent agents.Agent, taskID uuid.UUID, req daemon.RequestApproval) daemon.ApprovalDecision {
	// Per-Agent-Tool-Zuweisung (fail-closed, sobald eine Allowlist existiert):
	// das Subjekt ist system:tool. Ohne Zuweisung für das System greift dies
	// nicht — Built-ins/Manifeste bleiben unberührt.
	if o.Targets != nil {
		if system, tool, ok := strings.Cut(req.Action, ":"); ok {
			allowed, err := o.Targets.AgentToolAllowed(ctx, agent.ID, system, tool)
			if err != nil {
				return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
					Reason: "tool-zuweisung nicht lesbar (fail-closed)"}
			}
			if !allowed {
				_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, observability.KindGuardrail,
					map[string]any{"rule": "tool_not_assigned", "action": req.Action, "decision": "denied"})
				o.events.Publish(Event{Type: "guardrail", AgentID: agent.ID.String(),
					Data: map[string]string{"action": req.Action, "decision": "denied"}})
				return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
					Reason: "tool " + tool + " ist diesem Agenten nicht zugewiesen"}
			}
		}
	}
	rules, err := o.Rails.List(ctx, agent.OrgID)
	if err != nil {
		return daemon.ApprovalDecision{RequestID: req.RequestID, Status: "denied",
			Reason: "guard-rails nicht lesbar (fail-closed)"}
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
			Reason: "verboten durch Guard-Rail " + verdict.Rule.Pattern}

	case guardrails.RequireApproval:
		// Unverbrauchte Freigabe für genau diese Aktion? Dann konsumieren.
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

// OnApprovalDecided schließt den Kreis des Approval-Gates: die Entscheidung
// weckt die auf "approval:<id>" geblockte Aufgabe (Wake-on-correlation).
func (o *Orchestrator) OnApprovalDecided(ctx context.Context, appr observability.Approval) {
	text := fmt.Sprintf("Die Freigabe für %q wurde erteilt. Führe die Aktion jetzt erneut über den Action-Proxy aus und schließe die Aufgabe ab.", appr.Action)
	if appr.Status == "denied" {
		text = fmt.Sprintf("Die Freigabe für %q wurde ABGELEHNT. Führe die Aktion nicht aus; wähle einen anderen Weg oder eskaliere.", appr.Action)
	}
	if _, err := o.Backlog.CorrelateWake(ctx, "approval:"+appr.ID.String(), text); err != nil &&
		!errors.Is(err, backlog.ErrNotFound) {
		o.Log.Warn("approval-korrelation", "approval", appr.ID, "err", err)
	}
}

// Kill ist der Kill-Switch für einen Agenten: Flag setzen, laufende Session
// sofort abbrechen, angefangene Aufgabe zurück ins Backlog.
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
	// Eine geparkte warme Sandbox (kein aktiver Lauf) sofort abbauen — ein
	// gekillter Agent darf keinen laufenden Container behalten.
	o.evictWarm(ctx, agentID, true)
	// Angefangene Aufgabe wieder öffnen, damit nichts verloren geht.
	_, _ = o.Pool.Exec(ctx, `UPDATE backlog_tasks SET state='open', updated_at=now()
		WHERE agent_id=$1 AND state='in_progress'`, agentID)
	if agent, err := o.Registry.Get(ctx, agentID); err == nil {
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, nil, observability.KindLifecycle,
			map[string]string{"status": "killed"})
		o.events.Publish(Event{Type: "agent_status", AgentID: agentID.String(), Data: map[string]string{"status": "killed"}})
	}
	return nil
}

// KillFleet ist der flottenweite Notaus (spec/06).
func (o *Orchestrator) KillFleet(ctx context.Context, orgID uuid.UUID) error {
	if err := o.Registry.SetFleetKilled(ctx, orgID, true); err != nil {
		return err
	}
	rows, err := o.Pool.Query(ctx, "SELECT id FROM agents WHERE org_id=$1", orgID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
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
	// Geparkte warme Sandboxen (kein aktiver Lauf) einsammeln und abbauen.
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

// HandleWebhook verarbeitet ein eingehendes Zielsystem-Event (M3/M4):
// idempotent; korreliert zuerst gegen geblockte Aufgaben (dann ist
// ResumeInput die Fortsetzungs-Eingabe), sonst neue Backlog-Aufgabe (TaskBody).
func (o *Orchestrator) HandleWebhook(ctx context.Context, agent agents.Agent, source string, ev target.WebhookEvent) (string, error) {
	tag, err := o.Pool.Exec(ctx, `INSERT INTO webhook_events (dedup_key, source)
		VALUES ($1,$2) ON CONFLICT (dedup_key) DO NOTHING`, ev.DedupKey, source)
	if err != nil {
		return "", err
	}
	if tag.RowsAffected() == 0 {
		return "duplicate", nil // Retry des Zielsystems — schon verarbeitet
	}
	if !ev.Wake {
		return "ignored", nil // z. B. das Echo der eigenen Agent-Antwort
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
		return "ignored", nil // wartet niemand auf das Event, entsteht keine Arbeit
	}
	task, err := o.Backlog.Create(ctx, agent.OrgID, agent.ID, ev.Title, ev.TaskBody, "webhook:"+source, 3)
	if err != nil {
		return "", err
	}
	o.publishTask(task.ID, agent.ID)
	return "created", nil
}

// HandleAgentTrigger verarbeitet den generischen, per Token authentifizierten
// Webhook-Trigger eines Agenten (spec/03, Wake-Quelle Event): optional
// idempotent über einen dedup_key, legt eine Backlog-Aufgabe an und stößt den
// Dispatch sofort an. Anders als HandleWebhook gibt es kein Zielsystem-Plugin
// und keine Korrelation — der Absender ist ein beliebiges Fremdsystem.
func (o *Orchestrator) HandleAgentTrigger(ctx context.Context, agent agents.Agent, title, body string, priority int, dedupKey string) (string, error) {
	if dedupKey != "" {
		// Global eindeutige Tabelle — je Agent scopen, damit sich Fremdsysteme
		// verschiedener Agenten nicht gegenseitig deduplizieren.
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
