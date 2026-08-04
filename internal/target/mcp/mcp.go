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
	"covey/internal/target"
)

// Config ist die persistierte Definition eines angebundenen MCP-Servers.
// Der Endpoint (URL) und der optionale Auth-Header sind die Identität des
// Plugins; das Token selbst wird NICHT hier gespeichert, sondern zur Laufzeit
// aus dem SecretStore gebrokert (<name>_token). Tools ist die zuletzt
// entdeckte Werkzeugliste — reine Metadaten (Namen/Beschreibungen/Schemas),
// gecacht für UI und Prompt-Doku, ohne den Server jedes Mal zu befragen.
type Config struct {
	Name        string `json:"name"`
	Label       string `json:"label,omitempty"`
	Description string `json:"description,omitempty"`

	// URL ist der Streamable-HTTP-Endpoint des MCP-Servers.
	URL string `json:"url"`

	// Auth beschreibt, wie das gebrokerte Token in Requests landet. Leer =
	// keine Authentifizierung (öffentlicher/lokaler Server).
	Auth Auth `json:"auth,omitempty"`

	// Tools ist die zuletzt entdeckte Werkzeugliste (Cache, siehe oben).
	Tools []Tool `json:"tools,omitempty"`
}

type Auth struct {
	// Header, in den das Token geschrieben wird (Default "Authorization").
	Header string `json:"header,omitempty"`
	// Format des Header-Werts; {token} wird ersetzt (Default "Bearer {token}").
	Format string `json:"format,omitempty"`
}

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// ParseConfig validiert eine hochgeladene MCP-Konfiguration fail-closed.
func ParseConfig(raw []byte) (Config, error) {
	var c Config
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, fmt.Errorf("mcp-config: %w", err)
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	if !nameRe.MatchString(c.Name) {
		return fmt.Errorf("mcp-config: name muss %s entsprechen", nameRe)
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("mcp-config: url muss mit http(s):// beginnen")
	}
	return nil
}

// authFields liefert Header-Name und -Wert für das gebrokerte Token; leer,
// wenn keine Auth konfiguriert ist oder kein Token vorliegt.
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

// System interpretiert eine Config als target.System — dieselbe Schnittstelle
// wie kompilierte und Manifest-Plugins, damit Broker, Guard-Rails und Recording
// identisch greifen. Jedes MCP-Tool ist eine Aktion (action == Tool-Name).
type System struct {
	Cfg  Config
	HTTP *http.Client
}

func NewSystem(c Config) *System {
	return &System{Cfg: c, HTTP: reqlog.Client("mcp", 30*time.Second)}
}

func (s *System) Name() string { return s.Cfg.Name }

// VerifyWebhook: MCP-Server sind keine Webhook-Quelle — der Webhook-Eingang
// wird für sie nie benutzt. Die Methode ist Teil des Interfaces und gibt
// neutral true zurück.
func (s *System) VerifyWebhook(secret string, body []byte, header http.Header) bool { return true }

// ParseWebhook ist für MCP nicht anwendbar (kein eingehender Kanal).
func (s *System) ParseWebhook(body []byte) (target.WebhookEvent, error) {
	return target.WebhookEvent{}, fmt.Errorf("mcp-zielsystem %q kennt keine webhooks", s.Cfg.Name)
}

// ActionSubject bildet Tool-Aufrufe auf das Guard-Rail-Subjekt <name>:<tool> ab.
func (s *System) ActionSubject(action string, params json.RawMessage) string {
	return s.Cfg.Name + ":" + action
}

// Execute ruft ein MCP-Tool auf: frische Sitzung aufbauen (initialize),
// tools/call, Ergebnis zurück. URL kommt aus der Config, das optionale Token
// aus dem gebrokerten Credential — nie persistiert.
func (s *System) Execute(ctx context.Context, action string, params json.RawMessage, cred target.Credential) (any, error) {
	url := s.Cfg.URL
	if cred.BaseURL != "" {
		url = cred.BaseURL
	}
	hdr, val := s.Cfg.authFields(cred.Token)
	conn, err := Dial(ctx, url, hdr, val, s.HTTP)
	if err != nil {
		return nil, fmt.Errorf("%s: verbindung fehlgeschlagen: %w", s.Cfg.Name, err)
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

// PromptDoc beschreibt die verfügbaren Tools für den System-Prompt. only
// filtert (falls nicht nil) auf die dem Agenten zugewiesenen Tools.
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
