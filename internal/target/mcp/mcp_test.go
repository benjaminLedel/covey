package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"covey/internal/target"
)

// fakeMCP ist ein minimaler MCP-Server über Streamable HTTP: initialize,
// tools/list, tools/call. sse steuert das Antwortformat (JSON vs. SSE).
func fakeMCP(t *testing.T, sse bool, wantAuth string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if wantAuth != "" && r.Header.Get("Authorization") != wantAuth {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		body, _ := io.ReadAll(r.Body)
		var req struct {
			ID     int             `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &req)
		if req.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{"protocolVersion": protocolVersion, "capabilities": map[string]any{},
				"serverInfo": map[string]any{"name": "fake", "version": "1"}}
		case "tools/list":
			result = map[string]any{"tools": []map[string]any{
				{"name": "get_weather", "description": "Wetter\nMehrzeilig", "inputSchema": map[string]any{"type": "object"}},
				{"name": "send_alert", "description": "Alarm"},
			}}
		case "tools/call":
			var p struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			}
			_ = json.Unmarshal(req.Params, &p)
			result = map[string]any{"content": []map[string]any{
				{"type": "text", "text": "tool=" + p.Name + " args=" + string(p.Arguments)}}}
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}
		resp := map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result}
		raw, _ := json.Marshal(resp)
		w.Header().Set("Mcp-Session-Id", "sess-1")
		if sse {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("event: message\ndata: " + string(raw) + "\n\n"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(raw)
	}))
}

func TestDialListCall_JSON(t *testing.T) {
	srv := fakeMCP(t, false, "")
	defer srv.Close()
	conn, err := Dial(context.Background(), srv.URL, "", "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tools, err := conn.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tools) != 2 || tools[0].Name != "get_weather" {
		t.Fatalf("tools = %+v", tools)
	}
	res, err := conn.CallTool(context.Background(), "get_weather", json.RawMessage(`{"city":"HH"}`))
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	if !strings.Contains(string(res), "tool=get_weather") || !strings.Contains(string(res), "HH") {
		t.Fatalf("result = %s", res)
	}
}

func TestDialListCall_SSE(t *testing.T) {
	srv := fakeMCP(t, true, "")
	defer srv.Close()
	conn, err := Dial(context.Background(), srv.URL, "", "", nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	tools, err := conn.ListTools(context.Background())
	if err != nil || len(tools) != 2 {
		t.Fatalf("sse list: %v tools=%v", err, tools)
	}
}

func TestSystemExecute_Auth(t *testing.T) {
	srv := fakeMCP(t, false, "Bearer secret-123")
	defer srv.Close()
	cfg := Config{Name: "weather", URL: srv.URL}
	sys := NewSystem(cfg)

	// Ohne Token → 401 → Fehler.
	if _, err := sys.Execute(context.Background(), "send_alert", nil, target.Credential{}); err == nil {
		t.Fatal("erwartete auth-fehler ohne token")
	}
	// Mit Token → ok.
	out, err := sys.Execute(context.Background(), "send_alert", json.RawMessage(`{"x":1}`),
		target.Credential{Token: "secret-123"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(toJSON(t, out), "tool=send_alert") {
		t.Fatalf("out = %v", out)
	}
}

func TestPromptDocFor_Filter(t *testing.T) {
	cfg := Config{Name: "weather", Label: "Wetterdienst", URL: "https://x", Tools: []Tool{
		{Name: "get_weather", Description: "Wetter\nZeile2"}, {Name: "send_alert", Description: "Alarm"},
	}}
	sys := NewSystem(cfg)
	full := sys.PromptDoc()
	if !strings.Contains(full, "get_weather") || !strings.Contains(full, "send_alert") {
		t.Fatalf("full doc: %s", full)
	}
	if strings.Contains(full, "Zeile2") {
		t.Fatalf("beschreibung sollte auf erste zeile gekürzt sein: %s", full)
	}
	only := sys.PromptDocFor(map[string]bool{"get_weather": true})
	if !strings.Contains(only, "get_weather") || strings.Contains(only, "send_alert") {
		t.Fatalf("gefilterte doc falsch: %s", only)
	}
}

func TestParseConfig_Validate(t *testing.T) {
	if _, err := ParseConfig([]byte(`{"name":"OK","url":"https://x"}`)); err == nil {
		t.Fatal("name mit großbuchstaben sollte scheitern")
	}
	if _, err := ParseConfig([]byte(`{"name":"weather","url":"ftp://x"}`)); err == nil {
		t.Fatal("nicht-http url sollte scheitern")
	}
	if _, err := ParseConfig([]byte(`{"name":"weather","url":"https://x/mcp"}`)); err != nil {
		t.Fatalf("gültige config: %v", err)
	}
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
