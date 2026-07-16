// Package orchestrator ist die Control-Plane-Seite des Daemon-Protokolls:
// Dispatch-Loop (LISTEN/NOTIFY + Tick), Agent-Sessions (seriell, eine Aufgabe
// zur Zeit), Secrets-Broker-Entscheidungen, Guard-Rail-Enforcement, Kill-Switch.
package orchestrator

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
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
	ReadyTimeout   time.Duration
	Log            *slog.Logger
}

type Orchestrator struct {
	Options

	mu       sync.Mutex
	sessions map[uuid.UUID]*session
	waiting  map[uuid.UUID]chan DaemonLink

	events *Broadcaster
}

func New(opts Options) *Orchestrator {
	if opts.ReadyTimeout == 0 {
		opts.ReadyTimeout = 60 * time.Second
	}
	if opts.TickInterval == 0 {
		opts.TickInterval = 30 * time.Second
	}
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	return &Orchestrator{
		Options:  opts,
		sessions: map[uuid.UUID]*session{},
		waiting:  map[uuid.UUID]chan DaemonLink{},
		events:   NewBroadcaster(),
	}
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

	// Wake: Sandbox starten und auf ready warten.
	link, sandbox, err := o.wake(ctx, agent)
	if err != nil {
		o.setStatus(ctx, agent, nil, agents.StatusSleeping)
		return fmt.Errorf("wake: %w", err)
	}
	s.link = link
	defer func() {
		link.Close()
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		sandbox.Stop(stopCtx)
		final := agents.StatusSleeping
		if s.killed {
			final = agents.StatusKilled
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
			_ = o.sendMsg(ctx, link, daemon.TypeSleep, map[string]string{})
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

func (o *Orchestrator) publishTask(taskID, agentID uuid.UUID) {
	o.events.Publish(Event{Type: "task", AgentID: agentID.String(), Data: map[string]string{"task_id": taskID.String()}})
}

// processTask fährt eine Aufgabe durch triage → working → done/blocked/failed.
func (o *Orchestrator) processTask(ctx context.Context, agent agents.Agent, link DaemonLink, task backlog.Task, s *session) error {
	taskID := task.ID
	o.setStatus(ctx, agent, &taskID, agents.StatusTriage)
	o.publishTask(taskID, agent.ID)

	// Triage: Gedächtnis prüfen (M7) und Config kompilieren (M2).
	memCtx := ""
	if entries, err := o.Memory.Query(ctx, agent.ID, task.Title+" "+task.Body, 5); err == nil {
		memCtx = memory.FormatForPrompt(entries)
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

	if err := o.sendMsg(ctx, link, daemon.TypeInjectConfig, daemon.InjectConfig{
		SystemPrompt: compiled,
		Runtime:      agent.Runtime,
		AllowedTools: []string{"Bash", "Read", "Write"},
		MaxTurns:     30,
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
		if ev.Kind == "action" {
			kind = observability.KindAction
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &taskID, kind, json.RawMessage(ev.Payload))
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
		// done-Schritt: Gelerntes ins Gedächtnis einspeisen (M7).
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
		stage, err := o.Backlog.EnsureStage(ctx, agent.ID, ss.Stage)
		if err != nil {
			o.Log.Warn("set_stage: stage konnte nicht sichergestellt werden", "err", err)
			return false, nil
		}
		if _, err := o.Backlog.SetTaskStage(ctx, target, &stage.ID); err != nil {
			o.Log.Warn("set_stage: task konnte nicht verschoben werden", "err", err)
			return false, nil
		}
		_ = o.Obs.Record(ctx, agent.OrgID, agent.ID, &target, observability.KindLifecycle,
			map[string]string{"status": "stage", "stage": stage.Name})
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
	defer o.mu.Unlock()
	for _, s := range o.sessions {
		s.cancel()
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
	task, err := o.Backlog.Create(ctx, agent.OrgID, agent.ID, ev.Title, ev.TaskBody, "webhook:"+source, 3)
	if err != nil {
		return "", err
	}
	o.publishTask(task.ID, agent.ID)
	return "created", nil
}
