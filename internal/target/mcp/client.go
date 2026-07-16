// Package mcp bindet MCP-Server (Model Context Protocol) als Zielsystem-Plugins
// an — der dritte Plugin-Typ neben kompilierten Built-ins und deklarativen
// Manifesten (siehe internal/target). Ein MCP-Server exponiert eine Tool-Liste;
// Covey entdeckt sie (Control Plane, tools/list für die UI) und führt einzelne
// Tools über den Action-Proxy des Daemons aus (tools/call). Alle zentralen
// Enforcement-Punkte (Broker, Guard-Rails, Recording) greifen unverändert, weil
// ein MCP-System dieselbe target.System-Schnittstelle erfüllt wie jedes andere.
//
// Transport: Streamable HTTP (ein Endpoint, JSON-RPC 2.0 per POST). Die Antwort
// darf application/json ODER text/event-stream (SSE) sein — beide werden hier
// gelesen. stdio-Server sind bewusst ausgeklammert: ein Zielsystem ist ein
// erreichbarer Endpoint, kein in der Sandbox gestarteter Prozess.
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
)

// protocolVersion ist die MCP-Version, die Covey als Client anbietet.
const protocolVersion = "2025-06-18"

// Tool ist ein vom MCP-Server angebotenes Werkzeug — die Einheit, die im UI
// erscheint, einem Agenten zugewiesen und über den Action-Proxy aufgerufen wird.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// rpcRequest / rpcResponse sind der JSON-RPC-2.0-Rahmen.
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

func (e *rpcError) Error() string { return fmt.Sprintf("mcp fehler %d: %s", e.Code, e.Message) }

// Conn ist eine kurzlebige MCP-Sitzung: initialisiert, führt eine Handvoll
// Aufrufe aus, wird geschlossen. Sie wird pro Discovery (Control Plane) bzw.
// pro Tool-Aufruf (Daemon) frisch aufgebaut — Zustand hält Covey nicht.
type Conn struct {
	url       string
	authHdr   string // Header-Name für das Token (leer = keine Auth)
	authVal   string // fertiger Header-Wert
	http      *http.Client
	sessionID string
	nextID    int
}

// Dial baut eine Verbindung auf und führt den initialize-Handshake aus.
// authHeader/authValue sind optional (leer = kein Auth-Header).
func Dial(ctx context.Context, url, authHeader, authValue string, hc *http.Client) (*Conn, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
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
	// notifications/initialized: Notification (ohne id) — Ergebnis egal.
	_ = c.notify(ctx, "notifications/initialized", map[string]any{})
	return nil
}

// ListTools ruft tools/list und liefert die Werkzeuge des Servers.
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

// CallTool ruft tools/call mit den gegebenen Argumenten und liefert den
// rohen result-Teil (content/structuredContent) zurück.
func (c *Conn) CallTool(ctx context.Context, name string, args json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(args)) == 0 {
		args = json.RawMessage(`{}`)
	}
	return c.call(ctx, "tools/call", map[string]any{"name": name, "arguments": args})
}

// notify sendet eine JSON-RPC-Notification (keine id, keine Antwort erwartet).
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

// call sendet eine JSON-RPC-Anfrage und liefert das result-Feld der Antwort.
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

// decodeRPC liest die Antwort — application/json direkt, text/event-stream
// zeilenweise (die erste data:-Zeile mit einer JSON-RPC-Response gewinnt).
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
		return rpcResponse{}, fmt.Errorf("antwort nicht dekodierbar: %w (%.200s)", err, data)
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
		case line == "": // Event-Ende
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
	return rpcResponse{}, fmt.Errorf("SSE-Stream ohne JSON-RPC-Antwort")
}
