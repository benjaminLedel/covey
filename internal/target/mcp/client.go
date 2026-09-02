// Package mcp binds MCP servers (Model Context Protocol) as target-system
// plugins — the third plugin type alongside compiled built-ins and declarative
// manifests (see internal/target). An MCP server exposes a tool list; covey
// discovers it (control plane, tools/list for the UI) and executes individual
// tools through the daemon's action proxy (tools/call). All central enforcement
// points (broker, guard-rails, recording) apply unchanged, because an MCP
// system satisfies the same target.System interface as any other.
//
// Transport: streamable HTTP (one endpoint, JSON-RPC 2.0 via POST). The
// response may be application/json OR text/event-stream (SSE) — both are read
// here. stdio servers are deliberately left out: a target system is a reachable
// endpoint, not a process started inside the sandbox.
package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"covey/internal/reqlog"
)

// protocolVersion is the MCP version covey offers as a client.
const protocolVersion = "2025-06-18"

// Tool is a tool offered by the MCP server — the unit that appears in the UI,
// is assigned to an agent and is called through the action proxy.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// rpcRequest / rpcResponse are the JSON-RPC 2.0 envelope.
type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *rpcError) Error() string { return fmt.Sprintf("mcp error %d: %s", e.Code, e.Message) }

// Conn is a short-lived MCP session: it initializes, performs a handful of
// calls, and is closed. It is built freshly per discovery (control plane) and
// per tool call (daemon) — covey keeps no state.
type Conn struct {
	url       string
	authHdr   string // header name for the token (empty = no auth)
	authVal   string // ready-made header value
	http      *http.Client
	sessionID string
	nextID    int
}

// Dial opens a connection and performs the initialize handshake.
// authHeader/authValue are optional (empty = no auth header).
func Dial(ctx context.Context, url, authHeader, authValue string, hc *http.Client) (*Conn, error) {
	if hc == nil {
		hc = reqlog.Client("mcp", 20*time.Second)
	}
	c := &Conn{url: strings.TrimRight(url, "/"), authHdr: authHeader, authVal: authValue, http: hc, nextID: 1}
	if err := c.initialize(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Conn) initialize(ctx context.Context) error {
	res, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "covey", "version": "1.0"},
	})
	if err != nil {
		return fmt.Errorf("initialize: %w", err)
	}
	_ = res
	// notifications/initialized: a notification (no id) — the result does not matter.
	_ = c.notify(ctx, "notifications/initialized", map[string]any{})
	return nil
}

// ListTools calls tools/list and returns the server's tools.
func (c *Conn) ListTools(ctx context.Context) ([]Tool, error) {
	res, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, err
	}
	var out struct {
		Tools []struct {
			Name        string          `json:"name"`
			Description string          `json:"description"`
			InputSchema json.RawMessage `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(res, &out); err != nil {
		return nil, fmt.Errorf("tools/list: %w", err)
	}
	tools := make([]Tool, 0, len(out.Tools))
	for _, t := range out.Tools {
		tools = append(tools, Tool{Name: t.Name, Description: t.Description, InputSchema: t.InputSchema})
	}
	return tools, nil
}

// CallTool calls tools/call with the given arguments and returns the raw
// result part (content/structuredContent).
func (c *Conn) CallTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage(`{}`)
	}
	return c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
}

// notify sends a JSON-RPC notification (no id, no response expected).
func (c *Conn) notify(ctx context.Context, method string, params any) error {
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", Method: method, Params: params})
	if err != nil {
		return err
	}
	resp, err := c.do(ctx, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// call sends a JSON-RPC request and returns the response's result field.
func (c *Conn) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	id := c.nextID
	c.nextID++
	body, err := json.Marshal(rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params})
	if err != nil {
		return nil, err
	}
	resp, err := c.do(ctx, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		return nil, fmt.Errorf("%s: HTTP %d: %.300s", method, resp.StatusCode, data)
	}
	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		c.sessionID = sid
	}
	rpc, err := decodeRPC(resp)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", method, err)
	}
	if rpc.Error != nil {
		return nil, rpc.Error
	}
	return rpc.Result, nil
}

func (c *Conn) do(ctx context.Context, body []byte) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", protocolVersion)
	if c.authHdr != "" && c.authVal != "" {
		req.Header.Set(c.authHdr, c.authVal)
	}
	if c.sessionID != "" {
		req.Header.Set("Mcp-Session-Id", c.sessionID)
	}
	return c.http.Do(req)
}

// decodeRPC reads the response — application/json directly, text/event-stream
// line by line (the first data: line carrying a JSON-RPC response wins).
func decodeRPC(resp *http.Response) (rpcResponse, error) {
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		return decodeSSE(resp.Body)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return rpcResponse{}, err
	}
	var rpc rpcResponse
	if err := json.Unmarshal(data, &rpc); err != nil {
		return rpcResponse{}, fmt.Errorf("response not decodable: %w (%.200s)", err, data)
	}
	return rpc, nil
}

func decodeSSE(r io.Reader) (rpcResponse, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	var payload strings.Builder
	flush := func() (rpcResponse, bool) {
		if payload.Len() == 0 {
			return rpcResponse{}, false
		}
		var rpc rpcResponse
		if json.Unmarshal([]byte(payload.String()), &rpc) == nil && (rpc.Result != nil || rpc.Error != nil) {
			return rpc, true
		}
		payload.Reset()
		return rpcResponse{}, false
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "data:"):
			payload.WriteString(strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		case line == "": // end of event
			if rpc, ok := flush(); ok {
				return rpc, nil
			}
		}
	}
	if err := sc.Err(); err != nil {
		return rpcResponse{}, err
	}
	if rpc, ok := flush(); ok {
		return rpc, nil
	}
	return rpcResponse{}, fmt.Errorf("SSE stream without a JSON-RPC response")
}
