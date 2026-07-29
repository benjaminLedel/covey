package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"covey/internal/target"
	"covey/internal/target/mcp"
)

// Client ist die Daemon-Seite des Protokolls: verbindet sich zur Control
// Plane, bootstrappt die Runtime und setzt lokal den Tool-Layer durch
// (Action-Proxy). Die verbindliche Policy-Entscheidung liegt zentral —
// der Daemon ist ausführendes Organ, nicht Entscheider (spec/01).
type Client struct {
	wsURL    string
	token    string
	agentID  string
	homeDir  string
	runtimes map[string]Runtime

	conn    *websocket.Conn
	writeMu sync.Mutex

	mu        sync.Mutex
	cfg       InjectConfig
	creds     map[string]InjectCredentials // System → gebrokertes Credential (nur RAM)
	targets   map[string]target.System     // System → gebrokertes Manifest-Plugin (nur RAM)
	pending   map[string]chan Message      // request_id → Antwortkanal
	cancelRun context.CancelFunc

	// ErrKilled signalisiert dem Prozess-Exit den Kill-Pfad.
	log *slog.Logger
}

var ErrKilled = errors.New("kill-switch")

func NewClient(wsURL, token, agentID, homeDir string, log *slog.Logger) *Client {
	return &Client{
		wsURL:    wsURL,
		token:    token,
		agentID:  agentID,
		homeDir:  homeDir,
		runtimes: newRuntimes(),
		creds:    map[string]InjectCredentials{},
		targets:  map[string]target.System{},
		pending:  map[string]chan Message{},
		log:      log,
	}
}

func (c *Client) send(msgType string, payload any) error {
	if c.conn == nil {
		return errors.New("keine verbindung zur control plane")
	}
	msg, err := Encode(msgType, payload)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

// request sendet eine Anfrage mit request_id und wartet auf die Antwort.
func (c *Client) request(ctx context.Context, msgType string, requestID string, payload any) (Message, error) {
	ch := make(chan Message, 1)
	c.mu.Lock()
	c.pending[requestID] = ch
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, requestID)
		c.mu.Unlock()
	}()
	if err := c.send(msgType, payload); err != nil {
		return Message{}, err
	}
	select {
	case m := <-ch:
		return m, nil
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

// Run verbindet, meldet ready und verarbeitet Nachrichten bis sleep/kill.
func (c *Client) Run(ctx context.Context) error {
	// Proxy-bewusst: im harten Egress-Modus (COVEY_EGRESS_ISOLATION=network) hat
	// die Sandbox keinen direkten Weg zur Control Plane — die WS läuft per
	// HTTP-CONNECT durch den Egress-Proxy (setzt wss/TLS voraus). Im kooperativen
	// Modus steht die Control Plane in NO_PROXY, ProxyFromEnvironment liefert dann
	// nil und die Verbindung geht direkt — kein Verhalten ändert sich.
	conn, _, err := websocket.Dial(ctx, c.wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
		HTTPClient: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	})
	if err != nil {
		return fmt.Errorf("control plane nicht erreichbar: %w", err)
	}
	c.conn = conn
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(16 << 20)

	if err := c.send(TypeReady, Ready{AgentID: c.agentID}); err != nil {
		return err
	}

	// Heartbeat: Lebenszeichen alle 20s.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-t.C:
				_ = c.send(TypeHeartbeat, map[string]string{"agent_id": c.agentID})
			}
		}
	}()

	for {
		_, raw, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("verbindung verloren: %w", err)
		}
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.log.Warn("unlesbare nachricht", "err", err)
			continue
		}
		switch msg.Type {
		case TypeInjectConfig:
			cfg, err := DecodePayload[InjectConfig](msg)
			if err != nil {
				return err
			}
			c.mu.Lock()
			c.cfg = cfg
			c.mu.Unlock()
		case TypeInjectCredentials:
			cred, err := DecodePayload[InjectCredentials](msg)
			if err != nil {
				continue
			}
			if cred.RequestID != "" {
				c.route(cred.RequestID, msg)
				continue
			}
			// Proaktiv gepusht (z. B. anthropic-Key) → nur im RAM cachen.
			c.mu.Lock()
			c.creds[cred.System] = cred
			c.mu.Unlock()
		case TypeApprovalDecision:
			dec, err := DecodePayload[ApprovalDecision](msg)
			if err != nil {
				continue
			}
			c.route(dec.RequestID, msg)
		case TypeInjectTarget:
			inj, err := DecodePayload[InjectTarget](msg)
			if err != nil {
				continue
			}
			c.route(inj.RequestID, msg)
		case TypeInjectOrgChart:
			inj, err := DecodePayload[InjectOrgChart](msg)
			if err != nil {
				continue
			}
			c.route(inj.RequestID, msg)
		case TypeInjectWiki:
			inj, err := DecodePayload[InjectWiki](msg)
			if err != nil {
				continue
			}
			c.route(inj.RequestID, msg)
		case TypeInjectCreateTask:
			inj, err := DecodePayload[InjectCreateTask](msg)
			if err != nil {
				continue
			}
			c.route(inj.RequestID, msg)
		case TypeAssignTask:
			task, err := DecodePayload[AssignTask](msg)
			if err != nil {
				return err
			}
			go c.runTask(ctx, task)
		case TypeKill:
			c.mu.Lock()
			if c.cancelRun != nil {
				c.cancelRun()
			}
			c.mu.Unlock()
			return ErrKilled
		case TypeSleep:
			return nil
		}
	}
}

func (c *Client) route(requestID string, msg Message) {
	c.mu.Lock()
	ch := c.pending[requestID]
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

// credential holt ein gebrokertes Credential (RAM-Cache pro Verbindung).
func (c *Client) credential(ctx context.Context, system, taskID string) (InjectCredentials, error) {
	c.mu.Lock()
	if cred, ok := c.creds[system]; ok && cred.Granted {
		c.mu.Unlock()
		return cred, nil
	}
	c.mu.Unlock()

	reqID := uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestCredential, reqID,
		RequestCredential{RequestID: reqID, System: system, TaskID: taskID})
	if err != nil {
		return InjectCredentials{}, err
	}
	cred, err := DecodePayload[InjectCredentials](msg)
	if err != nil {
		return InjectCredentials{}, err
	}
	if !cred.Granted {
		return cred, fmt.Errorf("credential für %s verweigert: %s", system, cred.Reason)
	}
	c.mu.Lock()
	c.creds[system] = cred
	c.mu.Unlock()
	return cred, nil
}

// manifestSystem holt die Definition eines Manifest-Plugins von der Control
// Plane (RAM-Cache pro Verbindung). Nicht gewährt (unbekannt/deaktiviert)
// wird NICHT gecacht — die Aktivierung kann sich während der Session ändern.
func (c *Client) manifestSystem(ctx context.Context, system string) (target.System, bool) {
	c.mu.Lock()
	if sys, ok := c.targets[system]; ok {
		c.mu.Unlock()
		return sys, true
	}
	c.mu.Unlock()

	reqID := uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestTarget, reqID,
		RequestTarget{RequestID: reqID, System: system})
	if err != nil {
		return nil, false
	}
	inj, err := DecodePayload[InjectTarget](msg)
	if err != nil || !inj.Granted {
		return nil, false
	}
	var sys target.System
	switch inj.Kind {
	case "mcp":
		cfg, err := mcp.ParseConfig(inj.Manifest)
		if err != nil {
			c.log.Warn("gebrokerte mcp-config unlesbar", "system", system, "err", err)
			return nil, false
		}
		sys = mcp.NewSystem(cfg)
	default:
		m, err := target.ParseManifest(inj.Manifest)
		if err != nil {
			c.log.Warn("gebrokertes manifest unlesbar", "system", system, "err", err)
			return nil, false
		}
		sys = target.NewManifestSystem(m)
	}
	c.mu.Lock()
	c.targets[system] = sys
	c.mu.Unlock()
	return sys, true
}

// orgChart holt das Organigramm der Organisation von der Control Plane.
// Bewusst ungecacht — Profile und Zuordnungen können sich während der
// Session ändern, der Agent soll immer den aktuellen Stand sehen.
func (c *Client) orgChart(ctx context.Context) (json.RawMessage, error) {
	reqID := uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestOrgChart, reqID, RequestOrgChart{RequestID: reqID})
	if err != nil {
		return nil, err
	}
	inj, err := DecodePayload[InjectOrgChart](msg)
	if err != nil {
		return nil, err
	}
	return inj.Chart, nil
}

// wiki brokert ein Wiki-Tool (search/read/write) an die Control Plane und gibt
// die Antwort roh zurück. Ungecacht — das Wiki ändert sich während der Session.
func (c *Client) wiki(ctx context.Context, req RequestWiki) (InjectWiki, error) {
	req.RequestID = uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestWiki, req.RequestID, req)
	if err != nil {
		return InjectWiki{}, err
	}
	return DecodePayload[InjectWiki](msg)
}

// createTask lässt die Control Plane eine Aufgabe anlegen (covey/create_task):
// Teilaufgabe für den Agenten selbst oder Delegation an einen Kollegen.
func (c *Client) createTask(ctx context.Context, req RequestCreateTask) (InjectCreateTask, error) {
	req.RequestID = uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestCreateTask, req.RequestID, req)
	if err != nil {
		return InjectCreateTask{}, err
	}
	return DecodePayload[InjectCreateTask](msg)
}

// checkAction holt die zentrale Policy-Entscheidung für eine Aktion.
func (c *Client) checkAction(ctx context.Context, taskID, action string, params json.RawMessage) (ApprovalDecision, error) {
	reqID := uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestApproval, reqID,
		RequestApproval{RequestID: reqID, TaskID: taskID, Action: action, Params: params})
	if err != nil {
		return ApprovalDecision{}, err
	}
	return DecodePayload[ApprovalDecision](msg)
}

// runtimeKeyEnv liefert den gebrokerten LLM-Key als ENV-Zuweisung. Der Key ist
// selbst ein gebrokertes Secret (spec/12 Auth): proaktiv injiziert, nie
// dauerhaft in der Sandbox. Leer, solange nichts gebrokert wurde.
func (c *Client) runtimeKeyEnv() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	cred, ok := c.creds["anthropic"]
	if !ok || !cred.Granted {
		return nil
	}
	token := strings.TrimSpace(cred.Token)
	// Die Control Plane nennt die Ziel-Env (aus dem Secret-Namen). Fehlt sie,
	// raten wir am Präfix: Abo-Accounts liefern OAuth-Tokens (`claude
	// setup-token`, sk-ant-oat…), die Claude Code nur über
	// CLAUDE_CODE_OAUTH_TOKEN nutzt.
	envVar := cred.EnvVar
	if envVar == "" {
		if strings.HasPrefix(token, "sk-ant-oat") {
			envVar = "CLAUDE_CODE_OAUTH_TOKEN"
		} else {
			envVar = "ANTHROPIC_API_KEY"
		}
	}
	return []string{envVar + "=" + token}
}

// runTask fährt Action-Proxy + Runtime und meldet blocked/task_done + cost.
func (c *Client) runTask(ctx context.Context, task AssignTask) {
	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancelRun = cancel
	cfg := c.cfg
	c.mu.Unlock()
	defer cancel()

	proxy, err := c.startActionProxy(runCtx, task.TaskID)
	if err != nil {
		_ = c.send(TypeTaskDone, TaskDone{TaskID: task.TaskID, Status: "failed", Error: err.Error()})
		return
	}
	defer proxy.Close()

	env := append([]string{"COVEY_ACTION_PORT=" + proxy.Port()}, c.runtimeKeyEnv()...)

	runtime := c.runtimes[cfg.Runtime]
	if runtime == nil {
		_ = c.send(TypeTaskDone, TaskDone{TaskID: task.TaskID, Status: "failed",
			Error: fmt.Sprintf("unbekannte runtime %q", cfg.Runtime)})
		return
	}

	spec := RunSpec{
		TaskID:          task.TaskID,
		Title:           task.Title,
		Body:            task.Body,
		SystemPrompt:    cfg.SystemPrompt,
		Model:           cfg.Model,
		MemoryContext:   task.MemoryContext,
		AllowedTools:    cfg.AllowedTools,
		MaxTurns:        cfg.MaxTurns,
		MaxBudgetUSD:    cfg.MaxBudgetUSD,
		ResumeSessionID: task.ResumeSessionID,
		ResumeInput:     task.ResumeInput,
		HomeDir:         c.homeDir,
		Env:             env,
	}
	// Wiki-Arbeitskopie ins Home materialisieren (spec/05), damit der Agent es
	// mit normalen Datei-Tools lesen/bearbeiten kann.
	wikiSnap := c.materializeWiki(runCtx)

	res, err := runtime.Run(runCtx, spec, func(kind string, payload json.RawMessage) {
		_ = c.send(TypeEvent, Event{TaskID: task.TaskID, Kind: kind, Payload: payload})
	})
	if runCtx.Err() != nil {
		return // Kill-Pfad: die Control Plane hat übernommen
	}
	// Direkt bearbeitete/neu angelegte Seiten zurück in die Control Plane.
	c.syncWikiBack(runCtx, wikiSnap)
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
	}
	if res.CostUSD > 0 || res.InputTokens > 0 {
		_ = c.send(TypeCost, Cost{TaskID: task.TaskID, USD: res.CostUSD,
			InputTokens: res.InputTokens, OutputTokens: res.OutputTokens, Model: res.Model})
	}
	if res.Status == "blocked" {
		_ = c.send(TypeBlocked, Blocked{TaskID: task.TaskID, CorrelationKey: res.CorrelationKey,
			Question: res.Question, SessionID: res.SessionID})
		return
	}
	_ = c.send(TypeTaskDone, TaskDone{TaskID: task.TaskID, Status: res.Status,
		Result: res.Result, Error: res.Error, Memory: res.Memory, SessionID: res.SessionID})
}
