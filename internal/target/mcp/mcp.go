package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"covey/internal/reqlog"
	"github.com/benjaminLedel/covey-plugin-sdk/target"
)

// Config is the persisted definition of a connected MCP server. The endpoint
// (URL) and the optional auth header are the plugin's identity; the token
// itself is NOT stored here but brokered from the SecretStore at runtime
// (<name>_token). Tools is the most recently discovered tool list — pure
// metadata (names/descriptions/schemas), cached for the UI and the prompt doc
// so the server does not have to be queried every time.
type Config struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`

	// URL is the MCP server's streamable HTTP endpoint.
	URL string `json:"url"`

	// Auth describes how the brokered token ends up in requests. Empty = no
	// authentication (public/local server).
	Auth Auth `json:"auth,omitempty"`

	// Tools is the most recently discovered tool list (cache, see above).
	Tools []Tool `json:"tools,omitempty"`
}

type Auth struct {
	// Header the token is written into (default "Authorization").
	Header string `json:"header,omitempty"`
	// Format of the header value; {token} is substituted (default "Bearer {token}").
	Format string `json:"format,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// ParseConfig validates an uploaded MCP configuration fail-closed.
func ParseConfig(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("mcp config: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("mcp config: name must match %s", nameRe)
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("mcp config: url must start with http(s)://")
	}
	return nil
}

// authFields returns the header name and value for the brokered token; empty
// if no auth is configured or no token is present.
func (c Config) authFields(token string) (string, string) {
	if token == "" {
		return "", ""
	}
	hdr := c.Auth.Header
	if hdr == "" {
		hdr = "Authorization"
	}
	format := c.Auth.Format
	if format == "" {
		format = "Bearer {token}"
	}
	return hdr, strings.ReplaceAll(format, "{token}", token)
}

// System interprets a Config as a target.System — the same interface as
// compiled and manifest plugins, so that broker, guard-rails and recording
// apply identically. Every MCP tool is one action (action == tool name).
type System struct {
	Cfg  Config
	HTTP *http.Client
}

func NewSystem(c Config) *System {
	return &System{Cfg: c, HTTP: reqlog.Client("mcp", 30*time.Second)}
}

func (s *System) Name() string { return s.Cfg.Name }

// Kein target.Webhooker: ein MCP-Server ist keine Ereignisquelle. Die
// Schnittstelle war frueher Pflicht und wurde deshalb erfuellt — VerifyWebhook
// gab dabei true zurueck, nahm also jeden unsignierten Aufruf an, um ihn danach
// in ParseWebhook abzulehnen. Weggelassen antwortet der Router fail-closed mit
// 404, und die Einrichtung zeigt keinen Webhook-Schritt.

// ActionSubject maps tool calls onto the guard-rail subject <name>:<tool>.
func (s *System) ActionSubject(action string, params json.RawMessage) string {
	return s.Cfg.Name + ":" + action
}

// Execute calls an MCP tool: build a fresh session (initialize), tools/call,
// return the result. The URL comes from the config, the optional token from the
// brokered credential — never persisted.
func (s *System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	url := s.Cfg.URL
	if cred.BaseURL != "" {
		url = cred.BaseURL
	}
	hdr, val := s.Cfg.authFields(cred.Token)
	conn, err := Dial(ctx, url, hdr, val, s.HTTP)
	if err != nil {
		return nil, fmt.Errorf("%s: connection failed: %w", s.Cfg.Name, err)
	}
	res, err := conn.CallTool(ctx, action, params)
	if err != nil {
		return nil, fmt.Errorf("%s tool %q: %w", s.Cfg.Name, action, err)
	}
	var out any
	if len(res) == 0 || json.Unmarshal(res, &out) != nil {
		return string(res), nil
	}
	return out, nil
}

// PromptDoc describes the available tools for the system prompt. only filters
// (if not nil) down to the tools assigned to the agent.
func (s *System) PromptDoc() string { return s.promptDoc(nil) }

func (s *System) PromptDocFor(only map[string]bool) string { return s.promptDoc(only) }

func (s *System) promptDoc(only map[string]bool) string {
	var lines []string
	for _, t := range s.Cfg.Tools {
		if only != nil && !only[t.Name] {
			continue
		}
		desc := strings.TrimSpace(t.Description)
		if desc == "" {
			lines = append(lines, "- "+t.Name)
		} else {
			lines = append(lines, "- "+t.Name+": "+firstLine(desc))
		}
	}
	if len(lines) == 0 {
		return ""
	}
	label := s.Cfg.Label
	if label == "" {
		label = s.Cfg.Name
	}
	return fmt.Sprintf("**%s** (MCP server, system=`%s`) — available tools:\n%s\n"+
		"Call: `curl -s -X POST http://localhost:$COVEY_ACTION_PORT/actions/%s/<tool> -d '<json-arguments>'`.",
		label, s.Cfg.Name, strings.Join(lines, "\n"), s.Cfg.Name)
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
