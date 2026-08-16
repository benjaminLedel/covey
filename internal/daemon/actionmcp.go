package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// The action proxy as an MCP server.
//
// Every target-system action used to be issued as a shell command: the runtime
// assembled `curl -s -X POST http://localhost:$COVEY_ACTION_PORT/actions/<sys>/<act> -d '<json>'`
// and read the answer off stdout. That works, and it costs more than it looks:
// the model writes JSON into a single-quoted shell string, so every quote,
// every brace and every redirection in a parameter is a chance to get it wrong
// — and a wrong call is a wasted turn against the runaway guard. Measured on a
// QA agent: 77 Bash calls in three runs, all of them shell-wrapped actions,
// every one of them a turn.
//
// As an MCP tool the same action is one typed call. The runtime hands over
// arguments as JSON, the answer comes back as a result — no shell in between.
//
// Deliberately ONE tool per target system rather than one per action: the
// plugins describe their actions in prose (PromptDoc), not as schemas, and
// inventing a JSON schema for each of some two hundred actions would be a
// second source of truth that drifts from the first. The tool takes the action
// name and its parameters; the description is the plugin's own doc.
//
// The curl route stays open unchanged. A daemon of an older build, a runtime
// without MCP and any agent that has learnt the shell form keep working.

// mcpProtocolVersion is the revision we answer initialize with.
const mcpProtocolVersion = "2025-06-18"

// actionMCPEnabled decides whether the runtime is offered the MCP route.
// Deliberately opt-in for now (COVEY_ACTION_MCP=1): the shell route is what
// every existing agent config describes, and a handshake that fails would take
// all target actions with it. Switch it on per instance, watch one agent, then
// make it the default.
func actionMCPEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("COVEY_ACTION_MCP"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// jsonRPC is one request on the MCP endpoint. ID is absent for notifications —
// those get no answer.
type jsonRPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// handleMCP serves the JSON-RPC endpoint. Only the four methods a tool server
// needs; anything else gets "method not found" rather than silence.
func (p *actionProxy) handleMCP(w http.ResponseWriter, r *http.Request) {
	var req jsonRPC
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	// A notification (no id) is acknowledged with 202 and no body — answering it
	// is a protocol error on our side.
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = map[string]any{
			"protocolVersion": mcpProtocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "covey-actions", "version": "1"},
		}
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": p.mcpTools()}
	case "tools/call":
		resp.Result = p.mcpCall(w, r, req.Params)
		if resp.Result == nil {
			return // the call answered by itself (parameter error)
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	writeJSON(w, resp)
}

// mcpTools is the tool list: one per granted target system, plus "covey" for
// the platform's own meta actions (board, notes, wiki, tasks). The list comes
// from the control plane (InjectConfig.ActionTools) — the daemon decides
// nothing about access here.
func (p *actionProxy) mcpTools() []map[string]any {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "The action's name, exactly as it stands in the description.",
			},
			"params": map[string]any{
				"type":        "object",
				"description": "The action's parameters as an object.",
			},
		},
		"required":             []string{"action"},
		"additionalProperties": false,
	}
	tools := make([]map[string]any, 0, len(p.tools))
	for _, t := range p.tools {
		tools = append(tools, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": schema,
		})
	}
	return tools
}

// mcpCall executes one action and packs the answer into an MCP result. The path
// is deliberately the same as the HTTP one: guard-rails, credential broker,
// recording and artifact sink stay in ONE place — the MCP route is a second
// door onto the same room, not a second room.
func (p *actionProxy) mcpCall(w http.ResponseWriter, r *http.Request, raw json.RawMessage) any {
	var in struct {
		Name      string `json:"name"`
		Arguments struct {
			Action string          `json:"action"`
			Params json.RawMessage `json:"params"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return mcpText(fmt.Sprintf("arguments could not be read: %v", err), true)
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Arguments.Action) == "" {
		return mcpText("tool name or action missing", true)
	}
	params := in.Arguments.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}

	result := p.run(r.Context(), in.Name, in.Arguments.Action, params)
	body, err := json.Marshal(result)
	if err != nil {
		return mcpText(fmt.Sprintf("result could not be serialised: %v", err), true)
	}
	// The error stays IN the result (status:"error"), as on the HTTP route —
	// isError would make the runtime treat a denied action as a broken tool.
	return mcpText(string(body), false)
}

func mcpText(text string, isErr bool) map[string]any {
	out := map[string]any{"content": []map[string]any{{"type": "text", "text": text}}}
	if isErr {
		out["isError"] = true
	}
	return out
}

// mcpConfig is the --mcp-config document for the runtime. Empty when there is
// nothing to offer — then the runtime starts without an MCP server rather than
// with an empty one.
func (p *actionProxy) mcpConfig(force bool) string {
	if (!force && !actionMCPEnabled()) || len(p.tools) == 0 {
		return ""
	}
	cfg := map[string]any{"mcpServers": map[string]any{
		"covey": map[string]any{
			"type": "http",
			"url":  "http://127.0.0.1:" + p.Port() + "/mcp",
		},
	}}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return ""
	}
	return string(raw)
}
