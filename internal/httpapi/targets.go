package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"covey/internal/target/mcp"
	targetstore "covey/internal/target/store"
)

// --- Zielsystem-Plugins: Built-ins aktivieren/deaktivieren, Manifeste hochladen ---

// handleListTargets liefert die Zielsysteme der Organisation: kompilierte
// Built-ins (Registry) plus hochgeladene Manifest-Plugins, mit Aktivierungs-Status.
func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Targets.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	for i := range list {
		list[i].SetupDoc = strings.ReplaceAll(list[i].SetupDoc, "{public_url}", strings.TrimRight(s.PublicURL, "/"))
	}
	writeJSON(w, http.StatusOK, list)
}

// agentSystem beantwortet die Frage „was kann dieser Agent in welchem
// Zielsystem tun?" an einer Stelle: das Plugin (Name, Art, Aktivierung), der
// Zugang des Agenten aus ACCESS.md (Scopes, Tool-Allowlist) und die
// Aktionsliste — derselbe Text, den auch sein System-Prompt bekommt.
//
// Ohne diese Zusammenführung steht die Antwort an drei Orten: in der
// Zielsystem-Verwaltung (aktiviert?), in ACCESS.md (darf er?) und im
// kompilierten Prompt (was genau?). Wer wissen will, warum ein Agent ein
// Ticket nicht schließt, will nicht drei Orte lesen.
type agentSystem struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
	Category    string `json:"category,omitempty"`
	// Enabled: für die Organisation freigeschaltet (opt-in, fail-closed).
	Enabled bool `json:"enabled"`
	// Access: der Agent hat einen Zugang in ACCESS.md — ohne ihn verweigert
	// der Broker jede Credential-Anfrage, egal wie aktiv das Plugin ist.
	Access bool     `json:"access"`
	Scopes []string `json:"scopes,omitempty"`
	// Tools ist die Werkzeug-Allowlist des Agenten (nur MCP); leer = alle.
	Tools []string `json:"tools,omitempty"`
	// Doc sind die Aktionen im Wortlaut des Prompts; leer, solange das System
	// für die Organisation nicht aktiviert ist (dann gibt es keine).
	Doc string `json:"doc,omitempty"`
}

// handleAgentSystems liefert die Zielsysteme aus der Sicht eines Agenten.
func (s *Server) handleAgentSystems(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "ungültige id")
		return
	}
	p := principalFrom(r)
	ctx := r.Context()

	agent, err := s.Registry.Get(ctx, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if agent.OrgID != p.OrgID {
		writeErr(w, http.StatusNotFound, "agent nicht gefunden")
		return
	}

	plugins, err := s.Targets.List(ctx, p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	docs, err := s.Targets.DocsForAgent(ctx, p.OrgID, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	bySystem := map[string]string{}
	for _, d := range docs {
		bySystem[d.System] = d.Doc
	}
	accesses, err := s.Registry.Accesses(ctx, id)
	if err != nil {
		mapErr(w, err)
		return
	}
	scopes := map[string][]string{}
	for _, a := range accesses {
		scopes[a.System] = a.Scopes
	}

	out := make([]agentSystem, 0, len(plugins))
	for _, pl := range plugins {
		sys := agentSystem{
			Name: pl.Name, Label: pl.Label, Description: pl.Description,
			Kind: pl.Kind, Category: pl.Category, Enabled: pl.Enabled,
			Doc: strings.TrimSpace(bySystem[pl.Name]),
		}
		if sc, ok := scopes[pl.Name]; ok {
			sys.Access = true
			sys.Scopes = sc
		}
		if tools, err := s.Targets.AgentTools(ctx, id, pl.Name); err == nil && len(tools) > 0 {
			sys.Tools = tools
		}
		out = append(out, sys)
	}
	// Zugänge zuerst — das ist die Liste, um die es geht; der Rest zeigt, was
	// dieser Agent noch bekommen könnte.
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Access != out[j].Access {
			return out[i].Access
		}
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, http.StatusOK, out)
}

// handleUploadTarget nimmt eine Plugin-Datei (JSON-Manifest) entgegen,
// validiert sie fail-closed und speichert sie als Custom-Zielsystem.
func (s *Server) handleUploadTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	m, err := s.Targets.PutManifest(r.Context(), p.OrgID, raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": m.Name, "kind": "custom"})
}

// handleToggleTarget aktiviert/deaktiviert ein Plugin für die Organisation.
func (s *Server) handleToggleTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "feld enabled (true|false) fehlt")
		return
	}
	if err := s.Targets.SetEnabled(r.Context(), p.OrgID, name, *in.Enabled); err != nil {
		if errors.Is(err, targetstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "enabled": *in.Enabled})
}

// --- MCP-Server als Zielsystem-Plugins ---

// mcpInput ist der Anlege-/Aktualisieren-Body eines MCP-Plugins. token dient
// nur der sofortigen Tool-Discovery und wird NICHT gespeichert — zur Laufzeit
// brokert der Daemon <name>_token aus dem Vault.
type mcpInput struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Auth        mcp.Auth `json:"auth"`
	Token       string   `json:"token"`
}

// handleCreateMCP legt ein MCP-Zielsystem an (oder aktualisiert es) und
// versucht direkt, die Tool-Liste zu entdecken. Scheitert die Discovery
// (Server nicht erreichbar, Auth falsch), bleibt die Config trotzdem gespeichert
// und der Fehler wird als discover_error zurückgemeldet.
func (s *Server) handleCreateMCP(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in mcpInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body nicht lesbar")
		return
	}
	cfg := mcp.Config{Name: in.Name, Label: in.Label, Description: in.Description, URL: in.URL, Auth: in.Auth}
	saved, err := s.Targets.PutMCP(r.Context(), p.OrgID, cfg)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	tools, derr := s.discoverMCP(r.Context(), saved, in.Token)
	resp := map[string]any{"name": saved.Name, "kind": "mcp", "tools": tools}
	if derr != nil {
		resp["discover_error"] = derr.Error()
	} else {
		_ = s.Targets.SetMCPTools(r.Context(), p.OrgID, saved.Name, tools)
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleDiscoverMCP verbindet sich erneut zum MCP-Server und aktualisiert die
// gespeicherte Tool-Liste. Optionaler token nur für diesen Aufruf.
func (s *Server) handleDiscoverMCP(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	cfg, err := s.Targets.MCPConfig(r.Context(), p.OrgID, name)
	if err != nil {
		mapErr(w, err)
		return
	}
	var in struct {
		Token string `json:"token"`
	}
	_ = readJSON(r, &in)
	tools, derr := s.discoverMCP(r.Context(), cfg, in.Token)
	if derr != nil {
		writeErr(w, http.StatusBadGateway, "discovery fehlgeschlagen: "+derr.Error())
		return
	}
	if err := s.Targets.SetMCPTools(r.Context(), p.OrgID, name, tools); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "tools": tools})
}

// handleListMCPTools liefert die zuletzt entdeckte Tool-Liste eines MCP-Plugins.
func (s *Server) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cfg, err := s.Targets.MCPConfig(r.Context(), p.OrgID, r.PathValue("name"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg.Tools)
}

// discoverMCP führt den tools/list-Handshake gegen einen MCP-Server aus.
// Der token gilt nur für diesen Aufruf (Discovery), nicht persistiert.
func (s *Server) discoverMCP(ctx context.Context, cfg mcp.Config, token string) ([]mcp.Tool, error) {
	dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var hdr, val string
	if token != "" {
		hdr = cfg.Auth.Header
		if hdr == "" {
			hdr = "Authorization"
		}
		format := cfg.Auth.Format
		if format == "" {
			format = "Bearer {token}"
		}
		val = strings.ReplaceAll(format, "{token}", token)
	}
	conn, err := mcp.Dial(dctx, cfg.URL, hdr, val, nil)
	if err != nil {
		return nil, err
	}
	return conn.ListTools(dctx)
}

// --- Per-Agent-Tool-Zuweisung ---

// handleGetAgentTools liefert die einem Agenten für ein System zugewiesenen
// Tools. Leere Liste = keine Einschränkung (alle Tools erlaubt).
func (s *Server) handleGetAgentTools(w http.ResponseWriter, r *http.Request) {
	agentID, ok := s.agentInOrg(w, r)
	if !ok {
		return
	}
	tools, err := s.Targets.AgentTools(r.Context(), agentID, r.PathValue("system"))
	if err != nil {
		mapErr(w, err)
		return
	}
	if tools == nil {
		tools = []string{}
	}
	writeJSON(w, http.StatusOK, tools)
}

// handleSetAgentTools ersetzt die Tool-Zuweisung eines Agenten für ein System.
func (s *Server) handleSetAgentTools(w http.ResponseWriter, r *http.Request) {
	agentID, ok := s.agentInOrg(w, r)
	if !ok {
		return
	}
	var in struct {
		Tools []string `json:"tools"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "feld tools (liste) fehlt")
		return
	}
	system := r.PathValue("system")
	if err := s.Targets.SetAgentTools(r.Context(), agentID, system, in.Tools); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"system": system, "tools": in.Tools})
}

// handleDeleteTarget entfernt ein Custom-Plugin (Built-ins sind nicht löschbar).
func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	if err := s.Targets.DeleteManifest(r.Context(), p.OrgID, name); err != nil {
		if errors.Is(err, targetstore.ErrNotFound) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"deleted": name})
}
