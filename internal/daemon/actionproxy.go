package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"covey/internal/reqlog"
	"covey/internal/target"
)

// actionProxy is the sandbox's local tool layer: the runtime talks to it and
// nothing else (http://127.0.0.1:<port>/actions/<system>/<action>). Every action
// is decided centrally against the guard-rails, executed with brokered
// credentials and recorded — secrets never reach the runtime.
type actionProxy struct {
	client *Client
	taskID string
	srv    *http.Server
	ln     net.Listener
	// tools are the target systems granted to the agent — the tool list of the
	// MCP route (actionmcp.go). Empty means: only the shell route exists.
	tools []ActionTool
}

func (c *Client) startActionProxy(ctx context.Context, taskID string, tools []ActionTool) (*actionProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &actionProxy{client: c, taskID: taskID, ln: ln, tools: tools}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /actions/{system}/{action}", p.handle)
	// The same actions once more as MCP tools — one door onto the same room
	// (see actionmcp.go for why).
	mux.HandleFunc("POST /mcp", p.handleMCP)
	p.srv = &http.Server{
		Handler:     mux,
		BaseContext: func(net.Listener) context.Context { return ctx },
		// The action proxy listens on loopback INSIDE the sandbox — exactly
		// where the runtime runs. A hanging call must not occupy the connection
		// indefinitely.
		ReadHeaderTimeout: 20 * time.Second,
	}
	go p.srv.Serve(ln)
	return p, nil
}

func (p *actionProxy) Port() string {
	return strconv.Itoa(p.ln.Addr().(*net.TCPAddr).Port)
}

func (p *actionProxy) Close() error { return p.srv.Close() }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func (p *actionProxy) handle(w http.ResponseWriter, r *http.Request) {
	var params json.RawMessage
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&params); err != nil {
		params = json.RawMessage(`{}`)
	}
	writeJSON(w, p.run(r.Context(), r.PathValue("system"), r.PathValue("action"), params))
}

// run is the one path an action takes, whichever door it came through (shell or
// MCP): guard-rail decision, brokered execution, recording. Returns what the
// caller sends back — a map, so a plugin's arbitrary result travels unchanged.
func (p *actionProxy) run(ctx context.Context, system, action string, params json.RawMessage) any {
	// "covey" is not an external target system but a meta action towards the
	// control plane itself (no credentials, no guard-rail check).
	if system == "covey" {
		return p.controlPlane(ctx, action, params)
	}

	subject := p.actionSubject(ctx, system, action, params)

	dec, err := p.client.checkAction(ctx, p.taskID, subject, params)
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	switch dec.Status {
	case "denied":
		return map[string]string{"status": "denied", "reason": dec.Reason}
	case "pending":
		return map[string]string{
			"status":          "pending_approval",
			"approval_id":     dec.ApprovalID,
			"correlation_key": dec.CorrelationKey,
		}
	}

	// Artifact sink: a plugin (e.g. browser screenshot) can pass an image
	// through for the recording without putting it into the runtime result.
	var artifact *target.Artifact
	ctx = target.WithArtifactSink(ctx, func(a target.Artifact) { artifact = &a })
	// Request sink: the HTTP requests the plugin makes along the way go to the
	// control plane as separate events (request log). The sink hangs off the
	// context so it applies to this action only.
	ctx = reqlog.WithSink(ctx, p.httpSink(system))

	data, err := p.execute(ctx, system, action, params)
	result := map[string]any{"status": "ok", "data": data}
	if err != nil {
		result = map[string]any{"status": "error", "error": err.Error()}
	}
	// Action + outcome into the recording (kind=action). An artifact travels
	// along base64-encoded; the orchestrator stores it as a blob and replaces it
	// with a reference (the bytes never land in the JSONB).
	auditMap := map[string]any{"action": subject, "params": params, "ok": err == nil}
	if artifact != nil && len(artifact.Bytes) > 0 {
		auditMap["image_b64"] = base64.StdEncoding.EncodeToString(artifact.Bytes)
		auditMap["image_mime"] = artifact.MIME
	}
	audit, _ := json.Marshal(auditMap)
	_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
	return result
}

// httpSink sends every HTTP request of a plugin as event(kind=http) to the
// control plane — where it lands in the request log. The call must not hold up
// the action path: send() writes buffered onto the WebSocket, errors are
// swallowed (diagnostic data, not an audit trail). The plugin does not always
// know the system from the URL — the proxy fills it in from the path.
func (p *actionProxy) httpSink(system string) reqlog.Sink {
	return func(e reqlog.Entry) {
		if e.System == "" {
			e.System = system
		}
		e.TaskID = p.taskID
		payload, err := json.Marshal(e)
		if err != nil {
			return
		}
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: EventKindHTTP, Payload: payload})
	}
}

// handleControlPlane serves meta actions (system="covey") that concern the
// control plane instead of a target system: set_stage (move the task into a
// possibly new stage on the board), add_note (note on the task), remember
// (insight straight into the memory), org_chart (query the org chart),
// create_task (subtask/delegation) and the wiki tools
// wiki_search/wiki_read/wiki_write/wiki_delete (spec/05).
func (p *actionProxy) controlPlane(ctx context.Context, action string, params json.RawMessage) any {
	switch action {
	case "create_task":
		return p.createTask(ctx, params)
	case "org_chart":
		chart, err := p.client.orgChart(ctx)
		if err != nil {
			return map[string]string{"status": "error", "error": err.Error()}
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:org_chart"})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		return map[string]any{"status": "ok", "data": chart}
	case "add_note", "remember":
		var in struct {
			Content string `json:"content"`
			TaskID  string `json:"task_id"`
			Page    string `json:"page"`
		}
		if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Content) == "" {
			return map[string]string{"status": "error", "error": "content missing"}
		}
		// remember with a page reference appends to exactly that page. Without a
		// reference the platform has to guess which page is meant — and that is
		// what produces the sentence-titled scatter pages that bloat the wiki
		// (spec/05).
		if action == "remember" && strings.TrimSpace(in.Page) != "" {
			resp, err := p.client.wiki(ctx, RequestWiki{Op: "append", Slug: in.Page, Text: in.Content})
			if err != nil {
				return map[string]string{"status": "error", "error": err.Error()}
			}
			if !resp.OK {
				return map[string]string{"status": "error", "error": resp.Error}
			}
			audit, _ := json.Marshal(map[string]any{"action": "covey:remember", "page": in.Page, "content": in.Content})
			_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
			return map[string]any{"status": "ok", "data": resp.Data}
		}
		taskID := in.TaskID
		if taskID == "" {
			taskID = p.taskID
		}
		scope := "task"
		if action == "remember" {
			scope = "memory"
		}
		if err := p.client.send(TypeNote, Note{TaskID: taskID, Scope: scope, Content: in.Content}); err != nil {
			return map[string]string{"status": "error", "error": err.Error()}
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:" + action, "scope": scope, "content": in.Content})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		return map[string]string{"status": "ok", "scope": scope}
	case "set_stage":
		var in struct {
			Stage  string `json:"stage"`
			TaskID string `json:"task_id"`
		}
		if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Stage) == "" {
			return map[string]string{"status": "error", "error": "stage missing"}
		}
		taskID := in.TaskID
		if taskID == "" {
			taskID = p.taskID
		}
		if err := p.client.send(TypeSetStage, SetStage{TaskID: taskID, Stage: in.Stage}); err != nil {
			return map[string]string{"status": "error", "error": err.Error()}
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:set_stage", "stage": in.Stage})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		return map[string]string{"status": "ok", "stage": in.Stage}
	case "wiki_search", "wiki_read", "wiki_write", "wiki_append", "wiki_delete":
		var in struct {
			Query string   `json:"query"`
			Slug  string   `json:"slug"`
			Title string   `json:"title"`
			Body  string   `json:"body"`
			Type  string   `json:"type"`
			Tags  []string `json:"tags"`
			Text  string   `json:"text"`
		}
		_ = json.Unmarshal(params, &in)
		op := strings.TrimPrefix(action, "wiki_")
		resp, err := p.client.wiki(ctx, RequestWiki{Op: op, Query: in.Query, Slug: in.Slug,
			Title: in.Title, Body: in.Body, Type: in.Type, Tags: in.Tags, Text: in.Text})
		if err != nil {
			return map[string]string{"status": "error", "error": err.Error()}
		}
		if !resp.OK {
			return map[string]string{"status": "error", "error": resp.Error}
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:" + action, "slug": in.Slug, "query": in.Query})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		return map[string]any{"status": "ok", "data": resp.Data}
	default:
		return map[string]string{"status": "error", "error": fmt.Sprintf("unknown covey action %q", action)}
	}
}

// handleCreateTask creates a task: without "agent" a subtask of the running
// agent, with "agent" a delegation to the colleague with that slug. Unlike the
// other covey actions it goes through the guard-rails — a task produces work
// and cost, and delegation (subject covey:create_task:foreign) must be
// forbiddable separately.
func (p *actionProxy) createTask(ctx context.Context, params json.RawMessage) any {
	var in struct {
		Title    string `json:"title"`
		Body     string `json:"body"`
		Agent    string `json:"agent"`
		Priority int    `json:"priority"`
	}
	if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Title) == "" {
		return map[string]string{"status": "error", "error": "title missing"}
	}
	subject := "covey:create_task"
	if strings.TrimSpace(in.Agent) != "" {
		subject += ":foreign"
	}
	dec, err := p.client.checkAction(ctx, p.taskID, subject, params)
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	switch dec.Status {
	case "denied":
		return map[string]string{"status": "denied", "reason": dec.Reason}
	case "pending":
		return map[string]string{
			"status":          "pending_approval",
			"approval_id":     dec.ApprovalID,
			"correlation_key": dec.CorrelationKey,
		}
	}

	resp, err := p.client.createTask(ctx, RequestCreateTask{
		TaskID: p.taskID, Agent: in.Agent, Title: in.Title, Body: in.Body, Priority: in.Priority,
	})
	if err != nil {
		return map[string]string{"status": "error", "error": err.Error()}
	}
	audit, _ := json.Marshal(map[string]any{"action": subject, "params": params,
		"ok": resp.OK, "created_task": resp.TaskID, "agent": resp.Agent})
	_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
	if !resp.OK {
		return map[string]string{"status": "error", "error": resp.Error}
	}
	return map[string]any{"status": "ok",
		"data": map[string]string{"task_id": resp.TaskID, "agent": resp.Agent}}
}

// actionSubject maps the action onto the guard-rail subject — each plugin knows
// its own special cases that can be governed more sharply (e.g.
// zammad:reply_external). Unknown systems fall back to system:action.
func (p *actionProxy) actionSubject(ctx context.Context, system, action string, params json.RawMessage) string {
	if sys, ok := p.resolveSystem(ctx, system); ok {
		return sys.ActionSubject(action, params)
	}
	return system + ":" + action
}

// resolveSystem finds the target system: first the compiled plugin registry,
// then the manifest plugins brokered by the control plane (custom).
func (p *actionProxy) resolveSystem(ctx context.Context, system string) (target.System, bool) {
	if sys, ok := target.Get(system); ok {
		return sys, true
	}
	return p.client.manifestSystem(ctx, system)
}

// execute runs the action with brokered credentials. Which systems exist is
// decided by the plugin registry (or the manifest) — not by this code.
func (p *actionProxy) execute(ctx context.Context, system, action string, params json.RawMessage) (any, error) {
	sys, ok := p.resolveSystem(ctx, system)
	if !ok {
		return nil, fmt.Errorf("unknown target system %q", system)
	}
	cred, err := p.client.credential(ctx, system, p.taskID)
	if err != nil {
		return nil, err
	}
	// Workdir for actions that materialize files into the sandbox (e.g. gitlab
	// checkout) — the credential itself stays in the daemon.
	ctx = target.WithWorkdir(ctx, p.client.homeDir)
	// Sub-agent runner: lets a plugin (dev:agent) start a nested runtime run in
	// the project checkout without knowing the daemon.
	ctx = target.WithSubAgent(ctx, p.client.subAgentRunner(p.taskID))
	return sys.Execute(ctx, action, params, target.Credential{BaseURL: cred.BaseURL, Token: cred.Token})
}
