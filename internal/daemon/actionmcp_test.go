package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// rpc sends one JSON-RPC request to the proxy's MCP endpoint and returns the
// decoded answer.
func rpc(t *testing.T, p *actionProxy, body string) (int, rpcResponse) {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewBufferString(body))
	w := httptest.NewRecorder()
	p.handleMCP(w, r)
	var resp rpcResponse
	if w.Body.Len() > 0 {
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("answer unreadable: %v (%s)", err, w.Body.String())
		}
	}
	return w.Code, resp
}

func testProxy() *actionProxy {
	return &actionProxy{tools: []ActionTool{
		{Name: "gitlab", Description: "Available GitLab actions: checkout {...}"},
		{Name: "covey", Description: "The platform's own actions"},
	}}
}

// The handshake: a runtime that speaks MCP asks initialize first and gets the
// tool list from tools/list. Without the two the action proxy is invisible as a
// tool server and the run falls back to the shell route.
func TestActionMCPHandshakeAndToolList(t *testing.T) {
	p := testProxy()

	_, init := rpc(t, p, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)
	if init.Error != nil {
		t.Fatalf("initialize failed: %+v", init.Error)
	}
	res, _ := init.Result.(map[string]any)
	if res["protocolVersion"] != mcpProtocolVersion {
		t.Fatalf("protocol version missing: %+v", res)
	}
	if _, ok := res["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatalf("without the tools capability nobody asks for tools: %+v", res)
	}

	_, list := rpc(t, p, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	raw, _ := list.Result.(map[string]any)["tools"].([]any)
	if len(raw) != 2 {
		t.Fatalf("expected 2 tools, got %+v", list.Result)
	}
	first := raw[0].(map[string]any)
	if first["name"] != "gitlab" || !strings.Contains(first["description"].(string), "checkout") {
		t.Fatalf("the tool carries the plugin's own doc as its description: %+v", first)
	}
	// The schema takes action + params — one tool per system, not one per
	// action: the plugins describe their actions in prose, and a JSON schema per
	// action would be a second source of truth that drifts from the first.
	schema := first["inputSchema"].(map[string]any)
	props := schema["properties"].(map[string]any)
	if _, ok := props["action"]; !ok {
		t.Fatalf("the action name has to be passable: %+v", schema)
	}
	if _, ok := props["params"]; !ok {
		t.Fatalf("the parameters have to be passable: %+v", schema)
	}
}

// A notification (no id) gets no answer — answering it is a protocol error.
func TestActionMCPNotificationStaysSilent(t *testing.T) {
	code, resp := rpc(t, testProxy(), `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if code != http.StatusAccepted {
		t.Fatalf("a notification is acknowledged with 202, got %d", code)
	}
	if resp.Result != nil || resp.Error != nil {
		t.Fatalf("a notification gets no answer: %+v", resp)
	}
}

// An unknown method is refused clearly instead of silently.
func TestActionMCPUnknownMethod(t *testing.T) {
	_, resp := rpc(t, testProxy(), `{"jsonrpc":"2.0","id":9,"method":"resources/list"}`)
	if resp.Error == nil || resp.Error.Code != -32601 {
		t.Fatalf("expected method-not-found, got %+v", resp)
	}
}

// A call without an action name says so instead of running an empty action.
func TestActionMCPCallNeedsAction(t *testing.T) {
	_, resp := rpc(t, testProxy(),
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"gitlab","arguments":{}}}`)
	out, _ := resp.Result.(map[string]any)
	if out["isError"] != true {
		t.Fatalf("a call without an action is an error: %+v", resp.Result)
	}
}

// The MCP route is opt-in and stays silent as long as it is not switched on —
// the shell route is what every existing agent config describes, and a failed
// handshake would take all target actions with it.
func TestActionMCPConfigIsOptIn(t *testing.T) {
	p := testProxy()
	if cfg := p.mcpConfig(); cfg != "" {
		t.Fatalf("without COVEY_ACTION_MCP no MCP server is offered: %q", cfg)
	}
	t.Setenv("COVEY_ACTION_MCP", "1")
	// Without a listener there is no port — the tool list alone does not make a
	// config either.
	empty := &actionProxy{}
	if cfg := empty.mcpConfig(); cfg != "" {
		t.Fatalf("without tools no MCP server: %q", cfg)
	}
}
