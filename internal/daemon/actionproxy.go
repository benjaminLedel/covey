package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"covey/internal/reqlog"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
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
	// fetchSecret resolves a {{secret:<key>}} placeholder. Defaults to
	// client.secret (a real, uncached control-plane round-trip); tests set
	// this directly to a stub instead of standing up a fake connection.
	fetchSecret func(ctx context.Context, key, taskID string) (InjectSecret, error)
}

func (c *Client) startActionProxy(ctx context.Context, taskID string, tools []ActionTool) (*actionProxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &actionProxy{client: c, taskID: taskID, ln: ln, tools: tools, fetchSecret: c.secret}
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

// CredentialRejected reads an action's error for the one thing worth telling
// the secret store: the target system refused the credential itself. A plugin
// built against the SDK says so in the error's type; one that only formats
// what it saw is read by its text — a 401 is that statement in any plugin's
// words, a 403 is not (that is a permission, and marking the secret for it
// would be wrong in the way that matters).
func CredentialRejected(err error) bool {
	if err == nil {
		return false
	}
	if target.IsCredentialRejected(err) {
		return true
	}
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "http 401") || strings.Contains(t, "401 unauthorized") ||
		strings.Contains(t, "status 401") || strings.Contains(t, "(401)")
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
	if err != nil && CredentialRejected(err) {
		// The target system refused the credential itself. Said in the event,
		// the control plane marks the stored secret — and the cached copy
		// here is forgotten, so the next action asks again rather than
		// walking into the same wall for the rest of the TTL.
		auditMap["credential_rejected"] = true
		auditMap["error"] = err.Error()
		p.client.forgetCredential(system)
	}
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
	case "request_tool":
		// Die Bitte um ein Werkzeug. Jeder Agent darf sie stellen — sie liegt
		// deshalb hier und nicht bei den Meta-Actions der Registry, die einen
		// Scope brauchen: Ein Mitarbeiter, der Software braucht, stellt einen
		// Antrag, und dafür braucht er keine Personalbefugnis.
		var in struct {
			Tool string `json:"tool"`
			Why  string `json:"why"`
		}
		if err := json.Unmarshal(params, &in); err != nil || strings.TrimSpace(in.Tool) == "" {
			return map[string]string{"status": "error", "error": "tool missing"}
		}
		resp, err := p.client.requestTool(ctx, RequestTool{TaskID: p.taskID, Tool: in.Tool, Why: in.Why})
		if err != nil {
			return map[string]string{"status": "error", "error": err.Error()}
		}
		if !resp.OK {
			return map[string]string{"status": "error", "error": resp.Error}
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:request_tool", "tool": in.Tool})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		return map[string]any{"status": "ok", "id": resp.ID,
			"hint": "Filed. Nobody will install it during this run — work with what is here, " +
				"or say in your result that the task needs the tool."}

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
	case "list_targets", "get_agent_config", "create_agent", "set_agent_config",
		"work_record", "read_recording", "propose_agent_config", "write_review",
		"start_services":
		// Die Meta-Actions an der Registry der Plattform: Entwerfen (spec/20)
		// und Begutachten (spec/21). Alles wird in der Control Plane
		// entschieden — Scope, Guard-Rails, Freigaben —, der Proxy trägt die
		// Anfrage nur hinüber.
		var in struct {
			Agent       string            `json:"agent"`
			Slug        string            `json:"slug"`
			DisplayName string            `json:"display_name"`
			Runtime     string            `json:"runtime"`
			JobTitle    string            `json:"job_title"`
			Department  string            `json:"department"`
			Supervisor  string            `json:"supervisor"`
			Files       map[string]string `json:"files"`
			Task        string            `json:"task"`
			Days        int               `json:"days"`
			Title       string            `json:"title"`
			Rationale   string            `json:"rationale"`
			Summary     string            `json:"summary"`
			Findings    []ReviewNote      `json:"findings"`
			Issues      []ReviewNote      `json:"issues"`
			// Die Compose-Datei des Projekts (spec/16): Der Agent liest sie in
			// seinem Checkout und schickt den INHALT — der Pfad wäre für die
			// Steuerebene nicht auflösbar, sie sieht nicht in die Sandbox.
			Compose string   `json:"compose"`
			Only    []string `json:"only"`
		}
		_ = json.Unmarshal(params, &in)
		resp, err := p.client.hiring(ctx, RequestHiring{
			Op: action, TaskID: p.taskID, Agent: in.Agent, Slug: in.Slug,
			DisplayName: in.DisplayName, Runtime: in.Runtime, JobTitle: in.JobTitle,
			Department: in.Department, Supervisor: in.Supervisor, Files: in.Files,
			Task: in.Task, Days: in.Days, Title: in.Title, Rationale: in.Rationale,
			Summary: in.Summary, Findings: in.Findings, Issues: in.Issues,
			Compose: in.Compose, Only: in.Only,
		})
		if err != nil {
			return map[string]string{"status": "error", "error": err.Error()}
		}
		// Vor der OK-Prüfung: die Aktion ist nicht fehlgeschlagen, sie hat
		// nicht stattgefunden. Dieselbe Antwort wie bei einer Zielsystem-
		// Aktion, damit die Runtime nur EIN Muster kennen muss.
		if resp.Pending {
			return map[string]string{
				"status":          "pending_approval",
				"approval_id":     resp.ApprovalID,
				"correlation_key": resp.CorrelationKey,
			}
		}
		if !resp.OK {
			return map[string]string{"status": "error", "error": resp.Error}
		}
		audit, _ := json.Marshal(map[string]any{"action": "covey:" + action,
			"agent": in.Agent, "slug": in.Slug, "display_name": in.DisplayName})
		_ = p.client.send(TypeEvent, Event{TaskID: p.taskID, Kind: "action", Payload: audit})
		return map[string]any{"status": "ok", "data": resp.Data}

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
		// The working copy in the home follows along. The wiki is materialised
		// into ~/wiki before every run so the agent can read and edit it with
		// ordinary file tools — but a deletion used to reach only the control
		// plane, and the file stayed. Within the same run the agent then found
		// its own deleted page again through Grep and Read and reported it as
		// "wiki_search still returns hits from a stale index". The index was
		// fine; its own working copy was not.
		if op == "delete" {
			p.removeLocalWikiFile(in.Slug)
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
	// {{secret:<key>}} inside a string parameter (e.g. the text of a browser
	// "type" action, so an agent can fill a login form without a password ever
	// sitting in its own prompt/context) — substituted here, after the model has
	// already committed to the call, before the plugin ever sees the params. A
	// no-op (no RPC) when the params contain no placeholder.
	params, err = p.substituteSecrets(ctx, params)
	if err != nil {
		return nil, err
	}
	// Workdir for actions that materialize files into the sandbox (e.g. gitlab
	// checkout) — the credential itself stays in the daemon.
	ctx = target.WithWorkdir(ctx, p.client.homeDir)
	// Sub-agent runner: lets a plugin (dev:agent) start a nested runtime run in
	// the project checkout without knowing the daemon.
	ctx = target.WithSubAgent(ctx, p.client.subAgentRunner(p.taskID))
	return sys.Execute(ctx, action, params,
		target.Credential{BaseURL: cred.BaseURL, Token: cred.Token, CA: cred.CA})
}

// secretPlaceholder matches {{secret:<key>}} — key restricted to the charset
// secret keys are created with (PUT /api/v1/agents/{id}/secrets/{key}), so a
// stray "{{" in unrelated text (e.g. a Handlebars-looking snippet an agent
// quotes) can never be mistaken for one.
var secretPlaceholder = regexp.MustCompile(`\{\{secret:([A-Za-z0-9_.-]+)\}\}`)

// substituteSecrets resolves every distinct {{secret:<key>}} placeholder in
// params and replaces it with the real value, JSON-string-escaped so it slots
// back into the surrounding string literal unchanged. Deliberately operates on
// the raw JSON text, not a decoded map: the plugin-specific param struct is
// unknown here, and a placeholder can sit in any string field of it.
//
// This is what keeps a secret like a staging login out of the model's own
// context: the model writes the placeholder into its tool call, this
// substitution happens locally in the sandbox right before execution, and the
// audit log (actionProxy.run) still records the params as the model wrote
// them — with the placeholder, never the value.
func (p *actionProxy) substituteSecrets(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	matches := secretPlaceholder.FindAllStringSubmatch(string(params), -1)
	if len(matches) == 0 {
		return params, nil
	}
	out := string(params)
	seen := map[string]bool{}
	for _, m := range matches {
		key := m[1]
		if seen[key] {
			continue
		}
		seen[key] = true
		sec, err := p.fetchSecret(ctx, key, p.taskID)
		if err != nil {
			return nil, fmt.Errorf("secret %q: %w", key, err)
		}
		out = strings.ReplaceAll(out, "{{secret:"+key+"}}", jsonStringEscape(sec.Value))
	}
	return json.RawMessage(out), nil
}

// jsonStringEscape renders s the way it would appear INSIDE a JSON string
// literal (quotes/backslashes/control characters escaped, but without the
// literal's own surrounding quotes — those already sit in params around the
// placeholder).
func jsonStringEscape(s string) string {
	b, _ := json.Marshal(s)
	if len(b) < 2 {
		return ""
	}
	return string(b[1 : len(b)-1])
}
