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

// --- Target system plugins: enable/disable built-ins, upload manifests ---

// handleListTargets returns the target systems of the organization: compiled
// built-ins (registry) plus uploaded manifest plugins, with their enabled state.
func (s *Server) handleListTargets(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Targets.List(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	for i := range list {
		list[i].SetupDoc = strings.ReplaceAll(list[i].SetupDoc, "{public_url}", s.origin(r))
	}
	writeJSON(w, http.StatusOK, list)
}

// agentSystem answers the question "what can this agent do in which target
// system?" in one place: the plugin (name, kind, enabled state), the agent's
// access from ACCESS.md (scopes, tool allowlist) and the action list — the
// same text its system prompt gets.
//
// Without this merge, the answer lives in three places: in the target system
// administration (enabled?), in ACCESS.md (is he allowed to?) and in the
// compiled prompt (what exactly?). Whoever wants to know why an agent does not
// close a ticket does not want to read three places.
type agentSystem struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Kind        string `json:"kind"`
	Category    string `json:"category,omitempty"`
	// Enabled: unlocked for the organization (opt-in, fail-closed).
	Enabled bool `json:"enabled"`
	// Access: the agent has an access in ACCESS.md — without it the broker
	// refuses every credential request, no matter how enabled the plugin is.
	Access bool     `json:"access"`
	Scopes []string `json:"scopes,omitempty"`
	// Tools is the agent's tool allowlist (MCP only); empty = all.
	Tools []string `json:"tools,omitempty"`
	// Doc are the actions in the wording of the prompt; empty as long as the
	// system is not enabled for the organization or the agent has no line for
	// it in ACCESS.md — in both cases nothing of it reaches its prompt.
	Doc string `json:"doc,omitempty"`
}

// handleAgentSystems returns the target systems from an agent's point of view.
func (s *Server) handleAgentSystems(w http.ResponseWriter, r *http.Request) {
	// agentScoped has already resolved the agent and checked the organization.
	id := agentFrom(r).ID
	p := principalFrom(r)
	ctx := r.Context()

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
	// Accesses first — that is the list this is about; the rest shows what this
	// agent could still get.
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

// handleUploadTarget takes a plugin file (JSON manifest), validates it
// fail-closed and stores it as a custom target system.
func (s *Server) handleUploadTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
		return
	}
	m, err := s.Targets.PutManifest(r.Context(), p.OrgID, raw)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": m.Name, "kind": "custom"})
}

// handleToggleTarget enables/disables a plugin for the organization.
func (s *Server) handleToggleTarget(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	name := r.PathValue("name")
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "field enabled (true|false) is missing")
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

// --- MCP servers as target system plugins ---

// mcpInput is the create/update body of an MCP plugin. token only serves the
// immediate tool discovery and is NOT stored — at runtime the daemon brokers
// <name>_token from the vault.
type mcpInput struct {
	Name        string   `json:"name"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	URL         string   `json:"url"`
	Auth        mcp.Auth `json:"auth"`
	Token       string   `json:"token"`
}

// handleCreateMCP creates an MCP target system (or updates it) and tries to
// discover the tool list right away. If the discovery fails (server
// unreachable, wrong auth), the config stays saved nonetheless and the error is
// reported back as discover_error.
func (s *Server) handleCreateMCP(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in mcpInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "body not readable")
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

// handleDiscoverMCP connects to the MCP server again and updates the stored
// tool list. The optional token applies to this call only.
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
		writeErr(w, http.StatusBadGateway, "discovery failed: "+derr.Error())
		return
	}
	if err := s.Targets.SetMCPTools(r.Context(), p.OrgID, name, tools); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"name": name, "tools": tools})
}

// handleListMCPTools returns the most recently discovered tool list of an MCP plugin.
func (s *Server) handleListMCPTools(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	cfg, err := s.Targets.MCPConfig(r.Context(), p.OrgID, r.PathValue("name"))
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cfg.Tools)
}

// discoverMCP performs the tools/list handshake against an MCP server. The
// token applies to this call only (discovery), it is not persisted.
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

// --- Per-agent tool assignment ---

// handleGetAgentTools returns the tools assigned to an agent for a system.
// Empty list = no restriction (all tools allowed).
func (s *Server) handleGetAgentTools(w http.ResponseWriter, r *http.Request) {
	// Checked by agentScoped (server.go) — the agent is already fixed here.
	agentID := agentFrom(r).ID
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

// handleSetAgentTools replaces an agent's tool assignment for a system.
func (s *Server) handleSetAgentTools(w http.ResponseWriter, r *http.Request) {
	// Checked by agentScoped (server.go) — the agent is already fixed here.
	agentID := agentFrom(r).ID
	var in struct {
		Tools []string `json:"tools"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "field tools (list) is missing")
		return
	}
	system := r.PathValue("system")
	if err := s.Targets.SetAgentTools(r.Context(), agentID, system, in.Tools); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"system": system, "tools": in.Tools})
}

// handleDeleteTarget removes a custom plugin (built-ins cannot be deleted).
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
