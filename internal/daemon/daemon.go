package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"

	"covey/internal/target"
	"covey/internal/target/mcp"
)

// Client is the daemon side of the protocol: it connects to the control plane,
// bootstraps the runtime and enforces the tool layer locally (action proxy).
// The binding policy decision is made centrally — the daemon executes, it does
// not decide (spec/01).
type Client struct {
	wsURL    string
	token    string
	agentID  string
	homeDir  string
	runtimes map[string]Runtime

	// conn lives under writeMu — the mutex that serializes every write anyway.
	// It is set in Run() and read from every goroutine that sends.
	conn    *websocket.Conn
	writeMu sync.Mutex

	// subRuns counts this daemon's sub-runs and gives each one an identifier by
	// which the timeline recognizes its lines (see subagent.go).
	subRuns atomic.Uint64

	mu           sync.Mutex
	cfg          InjectConfig
	creds        map[string]InjectCredentials // system → brokered credential (RAM only)
	secrets      map[string]InjectSecret      // key → brokered custom secret (RAM only)
	targets      map[string]target.System     // system → brokered manifest plugin (RAM only)
	pending      map[string]chan Message      // request_id → response channel
	subAgentDirs map[string]bool              // directories with a running sub-agent
	cancelRun    context.CancelFunc

	// ErrKilled signals the kill path to the process exit.
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
		secrets:  map[string]InjectSecret{},
		targets:  map[string]target.System{},
		pending:  map[string]chan Message{},
		log:      log,
	}
}

func (c *Client) send(msgType string, payload any) error {
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
	// Before Run(), or after the connection dropped, there is no connection.
	// The check sits under writeMu because conn is written there.
	if c.conn == nil {
		return errors.New("no connection to the control plane")
	}
	// An own context instead of a passed-through one: a write on the control
	// plane connection must not depend on whether the TASK is still running —
	// the completion message in particular is produced when its context ends.
	// The 10 seconds bound it instead.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return c.conn.Write(ctx, websocket.MessageText, raw)
}

// request sends a request carrying a request_id and waits for the answer.
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

// Run connects, reports ready and processes messages until sleep/kill.
func (c *Client) Run(ctx context.Context) error {
	// Proxy-aware: in hard egress mode (COVEY_EGRESS_ISOLATION=network) the
	// sandbox has no direct route to the control plane — the WS runs through the
	// egress proxy via HTTP CONNECT (which requires wss/TLS). In cooperative
	// mode the control plane sits in NO_PROXY, ProxyFromEnvironment then returns
	// nil and the connection goes out directly — no behavior changes.
	conn, _, err := websocket.Dial(ctx, c.wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.token}},
		HTTPClient: &http.Client{Transport: &http.Transport{Proxy: http.ProxyFromEnvironment}},
	})
	if err != nil {
		return fmt.Errorf("control plane unreachable: %w", err)
	}
	c.writeMu.Lock()
	c.conn = conn
	c.writeMu.Unlock()
	defer conn.Close(websocket.StatusNormalClosure, "bye")
	conn.SetReadLimit(16 << 20)

	if err := c.send(TypeReady, Ready{AgentID: c.agentID}); err != nil {
		return err
	}

	// Heartbeat: sign of life every 20s.
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
			return fmt.Errorf("connection lost: %w", err)
		}
		var msg Message
		if err := json.Unmarshal(raw, &msg); err != nil {
			c.log.Warn("unreadable message", "err", err)
			continue
		}
		// Answers to our own requests first: they carry the request_id of the
		// waiting caller and go to its channel. Without a request_id the same
		// message type is a proactive push (inject_credentials) and falls
		// through into the switch.
		if c.deliverIfResponse(msg) {
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
			// Without a request_id this is not a response frame but a proactive
			// push (e.g. the anthropic key) → cache in RAM only.
			cred, err := DecodePayload[InjectCredentials](msg)
			if err != nil {
				continue
			}
			c.mu.Lock()
			c.creds[cred.System] = cred
			c.mu.Unlock()
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
		case TypeRequestUsage:
			req, err := DecodePayload[RequestUsage](msg)
			if err != nil {
				return err
			}
			go c.reportUsage(ctx, req)
		case TypeSleep:
			return nil
		}
	}
}

// routedInjectTypes are the message types with which the control plane answers
// a REQUEST from the daemon. This map is the routing — it replaces the former,
// character-identical switch branches per type.
//
// Why a list and not branches: whoever introduces a new response type and
// forgets it here leaves its caller without an answer, running into its
// timeout. If the request sits in the critical path before the run, the whole
// task stalls afterwards — and the error looks like a timeout somewhere else.
// That is exactly how inject_skills slipped through when it was added and made
// every integration test run into a 15-second timeout.
var routedInjectTypes = map[string]bool{
	TypeInjectCredentials: true, // also pushed proactively — then without request_id
	TypeApprovalDecision:  true,
	TypeInjectTarget:      true,
	TypeInjectOrgChart:    true,
	TypeInjectWiki:        true,
	TypeInjectSkills:      true,
	TypeInjectCreateTask:  true,
	TypeInjectHiring:      true,
	TypeInjectSecret:      true,
}

// deliverIfResponse hands an answer to one of our own requests to its waiting
// caller and reports whether the message is thereby dealt with.
//
// A function of its own instead of a condition in the middle of the read loop,
// so delivery is testable without a WebSocket: the failure it guards against (a
// response type missing from the routing, the caller running into its timeout)
// would otherwise only show up in an integration test — after 15 seconds of
// waiting.
func (c *Client) deliverIfResponse(msg Message) bool {
	if !routedInjectTypes[msg.Type] {
		return false
	}
	id := requestIDOf(msg)
	if id == "" {
		// Without a request_id the same type is a proactive push
		// (inject_credentials) and belongs in the read loop's switch.
		return false
	}
	c.route(id, msg)
	return true
}

// requestIDOf reads the correlation ID out of any response payload.
// Deliberately through a minimal struct instead of the concrete type: the
// routing needs only this one field, and that way a new response type costs
// exactly one entry in routedInjectTypes instead of another decode branch.
func requestIDOf(msg Message) string {
	var probe struct {
		RequestID string `json:"request_id"`
	}
	if len(msg.Payload) == 0 {
		return ""
	}
	_ = json.Unmarshal(msg.Payload, &probe)
	return probe.RequestID
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

// credential fetches a brokered credential (RAM cache per connection).
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
		return cred, fmt.Errorf("credential for %s denied: %s", system, cred.Reason)
	}
	c.mu.Lock()
	c.creds[system] = cred
	c.mu.Unlock()
	return cred, nil
}

// secret fetches a custom, agent-scoped secret by key (RAM cache per
// connection) for the {{secret:<key>}} placeholder substitution in action
// params. Unlike credential this is not keyed by target system — the key is
// whatever name the secret was stored under.
func (c *Client) secret(ctx context.Context, key, taskID string) (InjectSecret, error) {
	c.mu.Lock()
	if sec, ok := c.secrets[key]; ok && sec.Granted {
		c.mu.Unlock()
		return sec, nil
	}
	c.mu.Unlock()

	reqID := uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestSecret, reqID,
		RequestSecret{RequestID: reqID, Key: key, TaskID: taskID})
	if err != nil {
		return InjectSecret{}, err
	}
	sec, err := DecodePayload[InjectSecret](msg)
	if err != nil {
		return InjectSecret{}, err
	}
	if !sec.Granted {
		return sec, fmt.Errorf("secret %q denied: %s", key, sec.Reason)
	}
	c.mu.Lock()
	c.secrets[key] = sec
	c.mu.Unlock()
	return sec, nil
}

// manifestSystem fetches the definition of a manifest plugin from the control
// plane (RAM cache per connection). A refusal (unknown/disabled) is NOT cached
// — activation can change during the session.
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
			c.log.Warn("brokered mcp config unreadable", "system", system, "err", err)
			return nil, false
		}
		sys = mcp.NewSystem(cfg)
	default:
		m, err := target.ParseManifest(inj.Manifest)
		if err != nil {
			c.log.Warn("brokered manifest unreadable", "system", system, "err", err)
			return nil, false
		}
		sys = target.NewManifestSystem(m)
	}
	c.mu.Lock()
	c.targets[system] = sys
	c.mu.Unlock()
	return sys, true
}

// orgChart fetches the organization's org chart from the control plane.
// Deliberately uncached — profiles and assignments can change during the
// session, and the agent should always see the current state.
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

// wiki brokers a wiki tool (search/read/write) to the control plane and returns
// the answer raw. Uncached — the wiki changes during the session.
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

// createTask has the control plane create a task (covey/create_task): a subtask
// for the agent itself or a delegation to a colleague.
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

// hiring brokers a hiring meta action (covey/create_agent & co., spec/20) to
// the control plane. Every one of them is decided and executed there — the
// registry is there, and so are the four rules that keep an agent from
// employing anybody.
func (c *Client) hiring(ctx context.Context, req RequestHiring) (InjectHiring, error) {
	req.RequestID = uuid.NewString()
	reqCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	msg, err := c.request(reqCtx, TypeRequestHiring, req.RequestID, req)
	if err != nil {
		return InjectHiring{}, err
	}
	return DecodePayload[InjectHiring](msg)
}

// checkAction fetches the central policy decision for an action.
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

// runtimeKeyEnv provides the brokered LLM key as an ENV assignment. The key is
// itself a brokered secret (spec/12 Auth): pushed proactively, never persisted
// in the sandbox. Empty as long as nothing has been brokered.
func (c *Client) runtimeKeyEnv() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	cred, ok := c.creds["anthropic"]
	if !ok || !cred.Granted {
		return nil
	}
	token := strings.TrimSpace(cred.Token)
	// Delivered as a FILE rather than as a variable (spec/19): then there is no
	// environment to add. The file itself is written by writeCredentialFile
	// before the run and removed after it.
	if cred.Path != "" {
		return nil
	}
	// The control plane names the target env var (from the secret's name). If it
	// is missing, we guess from the prefix: subscription accounts yield OAuth
	// tokens (`claude setup-token`, sk-ant-oat…), which Claude Code only uses
	// through CLAUDE_CODE_OAUTH_TOKEN.
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

// runTask drives action proxy + runtime and reports blocked/task_done + cost.
func (c *Client) runTask(ctx context.Context, task AssignTask) {
	runCtx, cancel := context.WithCancel(ctx)
	c.mu.Lock()
	c.cancelRun = cancel
	cfg := c.cfg
	c.mu.Unlock()
	defer cancel()

	proxy, err := c.startActionProxy(runCtx, task.TaskID, cfg.ActionTools)
	if err != nil {
		_ = c.send(TypeTaskDone, TaskDone{TaskID: task.TaskID, Status: "failed", Error: err.Error()})
		return
	}
	defer proxy.Close()

	env := append([]string{"COVEY_ACTION_PORT=" + proxy.Port()}, c.runtimeKeyEnv()...)
	// Engines that take their credential as a file get it for this run only.
	defer c.writeCredentialFile()()

	runtime := c.runtimes[cfg.Runtime]
	if runtime == nil {
		_ = c.send(TypeTaskDone, TaskDone{TaskID: task.TaskID, Status: "failed",
			Error: fmt.Sprintf("unknown runtime %q", cfg.Runtime)})
		return
	}

	spec := RunSpec{
		TaskID:          task.TaskID,
		Title:           task.Title,
		Body:            task.Body,
		SystemPrompt:    cfg.SystemPrompt,
		Model:           cfg.Model,
		Effort:          cfg.Effort,
		MemoryContext:   task.MemoryContext,
		AllowedTools:    cfg.AllowedTools,
		MaxTurns:        cfg.MaxTurns,
		MaxBudgetUSD:    cfg.MaxBudgetUSD,
		ResumeSessionID: task.ResumeSessionID,
		ResumeInput:     task.ResumeInput,
		HomeDir:         c.homeDir,
		Env:             env,
		// The action proxy as an MCP server: with it the runtime calls a target
		// action as a typed tool instead of assembling a curl in the shell
		// (actionmcp.go). Empty = the shell route as before.
		MCPConfig: proxy.mcpConfig(cfg.Runtime == "codex"),
	}
	// Materialize the wiki working copy into the home (spec/05) so the agent can
	// read and edit it with ordinary file tools.
	wikiSnap := c.materializeWiki(runCtx)
	// Materialize skills into the home: the runtime finds them under
	// ~/.claude/skills/ and loads their body only when one applies. Must happen
	// BEFORE the run — afterwards it would have no effect. The count decides
	// whether the Skill tool belongs in the run's loading scope: without skills
	// it would only drag the built-in ones' descriptions into every turn.
	spec.Skills = c.materializeSkills(runCtx, cfg.Runtime) > 0

	res, err := runtime.Run(runCtx, spec, func(kind string, payload json.RawMessage) {
		_ = c.send(TypeEvent, Event{TaskID: task.TaskID, Kind: kind, Payload: payload})
	})
	if runCtx.Err() != nil {
		return // kill path: the control plane has taken over
	}
	// Directly edited/newly created pages go back into the control plane.
	c.syncWikiBack(runCtx, wikiSnap)
	if err != nil {
		res.Status = "failed"
		res.Error = err.Error()
	}
	if res.CostUSD > 0 || res.TotalInputTokens() > 0 {
		_ = c.send(TypeCost, Cost{TaskID: task.TaskID, USD: res.CostUSD,
			InputTokens: res.InputTokens, OutputTokens: res.OutputTokens,
			CacheReadTokens: res.CacheReadTokens, CacheCreationTokens: res.CacheCreationTokens,
			Model: res.Model})
	}
	if res.Status == "blocked" {
		_ = c.send(TypeBlocked, Blocked{TaskID: task.TaskID, CorrelationKey: res.CorrelationKey,
			Question: res.Question, SessionID: res.SessionID})
		return
	}
	_ = c.send(TypeTaskDone, TaskDone{TaskID: task.TaskID, Status: res.Status,
		Result: res.Result, Error: res.Error, Memory: res.Memory, SessionID: res.SessionID})
}

// reportUsage answers the control plane's question about the engine's own
// utilisation figure.
//
// Best effort throughout: an engine that cannot ask, a binary that fails, a
// text that no longer parses — all of it answers "not supported" rather than an
// error the caller has to handle. The figure is a nicety on top of the
// platform's own estimate, never a precondition for anything.
func (c *Client) reportUsage(ctx context.Context, req RequestUsage) {
	out := UsageReport{RequestID: req.RequestID}
	c.mu.Lock()
	engine := c.cfg.Runtime
	c.mu.Unlock()

	rt := c.runtimes[engine]
	reporter, ok := rt.(UsageReporter)
	if !ok || rt == nil {
		_ = c.send(TypeUsageReport, out)
		return
	}
	u, err := reporter.Usage(ctx, c.runtimeKeyEnv())
	if err != nil {
		out.Error = err.Error()
		_ = c.send(TypeUsageReport, out)
		return
	}
	out.Supported, out.Usage = u.Reported(), u
	_ = c.send(TypeUsageReport, out)
}

// writeCredentialFile puts a brokered credential into the agent home for the
// duration of one run, for engines that read it from a file instead of the
// environment (spec/19). Returns the cleanup, which the caller defers.
//
// The rule is the same as for the environment form and it is the reason this
// cleanup is not optional: a credential is brokered per waking phase and must
// not survive it. The home is persistent — a file left behind would be exactly
// the long-lived secret in the sandbox that spec/04 forbids.
func (c *Client) writeCredentialFile() func() {
	c.mu.Lock()
	cred, ok := c.creds["anthropic"]
	c.mu.Unlock()
	if !ok || !cred.Granted || cred.Path == "" || strings.TrimSpace(cred.Token) == "" {
		return func() {}
	}
	if !safeCredentialPath(cred.Path) {
		c.log.Warn("credential path refused", "path", cred.Path)
		return func() {}
	}
	target := filepath.Join(c.homeDir, filepath.FromSlash(cred.Path))
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		c.log.Warn("credential directory could not be created", "err", err)
		return func() {}
	}
	if err := os.WriteFile(target, []byte(cred.Token), 0o600); err != nil {
		c.log.Warn("credential file could not be written", "err", err)
		return func() {}
	}
	return func() {
		if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
			// Loud on purpose: a credential left lying in a persistent home is
			// the failure this whole mechanism exists to prevent.
			c.log.Error("credential file could not be removed — it stays in the home",
				"path", cred.Path, "err", err)
		}
	}
}

// safeCredentialPath keeps the engine's declared path inside the home. The
// declaration comes from our own code, not from a request — this is the second
// door, on the principle that whoever writes to a file system checks.
func safeCredentialPath(p string) bool {
	if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}
