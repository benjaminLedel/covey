// Package store manages target-system plugins per organization: the activation
// of the compiled built-ins and the uploaded manifest plugins (kind=custom).
// Control-plane side — the daemon gets manifests brokered over the daemon
// protocol.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/target"
	"covey/internal/target/mcp"
)

var ErrNotFound = errors.New("target system not found")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Plugin is the UI/API view of a target-system plugin.
type Plugin struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description"`
	Kind        string `json:"kind"` // builtin | custom | mcp
	// Category places the plugin in the store: for built-ins from the
	// descriptor, for manifest plugins from the manifest field "category".
	Category  string          `json:"category,omitempty"`
	Enabled   bool            `json:"enabled"`
	Manifest  json.RawMessage `json:"manifest,omitempty"` // custom: manifest, mcp: config
	UpdatedAt *time.Time      `json:"updated_at,omitempty"`
	// Scopes are the access levels the plugin understands in ACCESS.md — the
	// UI offers exactly these instead of letting somebody type a word that is
	// then silently ignored. Empty for manifest/MCP plugins.
	Scopes []string `json:"scopes,omitempty"`
	// SetupDoc is the setup guide for the UI: for built-ins from the plugin
	// descriptor, for manifest/MCP plugins generated generically.
	SetupDoc string `json:"setup_doc,omitempty"`
}

// List merges the compiled registry with the organization's DB rows.
// Activation is opt-in (fail-closed): a built-in without a row is disabled —
// only an explicit row with enabled=TRUE counts. (Existing organizations were
// put on their previous state by migration 0020.)
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Plugin, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, kind, enabled, manifest, updated_at FROM target_plugins WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := map[string]Plugin{}
	for rows.Next() {
		var p Plugin
		var manifest []byte
		if err := rows.Scan(&p.Name, &p.Kind, &p.Enabled, &manifest, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Manifest = manifest
		stored[p.Name] = p
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	var out []Plugin
	for _, d := range target.All() {
		p := Plugin{Name: d.Name, Label: d.Label, Description: d.Description, Kind: "builtin", Category: d.Category, Enabled: false, SetupDoc: d.SetupDoc, Scopes: d.Scopes}
		if row, ok := stored[d.Name]; ok {
			p.Enabled = row.Enabled
			p.UpdatedAt = row.UpdatedAt
			delete(stored, d.Name)
		}
		out = append(out, p)
	}
	for _, p := range stored {
		switch p.Kind {
		case "custom":
			p.Category = target.CategoryOther
			if m, err := target.ParseManifest(p.Manifest); err == nil {
				p.Label, p.Description = m.Label, m.Description
				if p.Label == "" {
					p.Label = m.Name
				}
				if m.Category != "" {
					p.Category = m.Category
				}
			}
			p.SetupDoc = customSetupDoc(p.Name)
		case "mcp":
			p.Category = target.CategoryOther
			if c, err := mcp.ParseConfig(p.Manifest); err == nil {
				p.Label, p.Description = c.Label, c.Description
				if p.Label == "" {
					p.Label = c.Name
				}
			}
			p.SetupDoc = mcpSetupDoc(p.Name)
		default:
			continue // row of a built-in that this binary did not compile in
		}
		out = append(out, p)
	}
	return out, nil
}

// customSetupDoc is the generic setup guide of a manifest plugin — the webhook
// route and secret names follow the convention, the details (auth header,
// actions) are in the manifest itself.
func customSetupDoc(name string) string {
	return fmt.Sprintf(`1. Store under Secrets and assign to the agent:
   %[1]s_url   = base URL of the system
   %[1]s_token = API token (sent via the auth header defined in the manifest)

2. Enable it in the agent's ACCESS.md:
   - system: %[1]s scope: read,write

3. Set up a webhook in the target system pointing at this URL:
   {public_url}/api/webhooks/%[1]s/<agent-slug>
   Signature secret: value of COVEY_%[2]s_WEBHOOK_SECRET (process env, empty = check off)

The webhook's field mapping and the available actions are defined by the
uploaded JSON manifest.`, name, strings.ToUpper(name))
}

// mcpSetupDoc is the generic setup guide of an MCP plugin — no webhook
// inbound; the tools run through the action proxy.
func mcpSetupDoc(name string) string {
	return fmt.Sprintf(`1. Store under Secrets and assign to the agent (if the server requires auth):
   %[1]s_token = token for the MCP server

2. Enable it in the agent's ACCESS.md (optionally with a tool allowlist):
   - system: %[1]s scope: read,write   tools: alle

3. Keep the tool list current: "Refresh tools" on this card runs discovery
   (tools/list) again.

MCP systems have no webhook inbound — work arrives via the backlog or other
target systems; the agent calls the tools through the action proxy.`, name)
}

// SetEnabled turns a plugin on/off for the organization. For built-ins the row
// is created on demand; unknown names are an error.
func (s *Store) SetEnabled(ctx context.Context, orgID uuid.UUID, name string, enabled bool) error {
	if _, ok := target.Get(name); ok {
		_, err := s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled)
			VALUES ($1,$2,'builtin',$3)
			ON CONFLICT (org_id, name) DO UPDATE SET enabled=$3, updated_at=now()`,
			orgID, name, enabled)
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE target_plugins SET enabled=$3, updated_at=now()
		WHERE org_id=$1 AND name=$2 AND kind IN ('custom','mcp')`, orgID, name, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PutManifest validates and stores an uploaded manifest plugin. The name of a
// compiled built-in is off limits — no silent shadowing.
func (s *Store) PutManifest(ctx context.Context, orgID uuid.UUID, raw []byte) (target.Manifest, error) {
	m, err := target.ParseManifest(raw)
	if err != nil {
		return m, err
	}
	if _, ok := target.Get(m.Name); ok {
		return m, fmt.Errorf("name %q is taken by a built-in plugin", m.Name)
	}
	// Store it normalized (compact JSON), not the raw upload.
	norm, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled, manifest)
		VALUES ($1,$2,'custom',TRUE,$3)
		ON CONFLICT (org_id, name) DO UPDATE SET kind='custom', manifest=$3, updated_at=now()`,
		orgID, m.Name, norm)
	return m, err
}

// DeleteManifest removes a custom plugin. Built-ins can only be disabled, not
// deleted.
func (s *Store) DeleteManifest(ctx context.Context, orgID uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM target_plugins WHERE org_id=$1 AND name=$2 AND kind IN ('custom','mcp')`, orgID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// System resolves a target system for an organization — only if activated
// (fail-closed): first the compiled registry, then the custom manifests.
func (s *Store) System(ctx context.Context, orgID uuid.UUID, name string) (target.System, error) {
	var kind string
	var enabled bool
	var manifest []byte
	err := s.pool.QueryRow(ctx, `SELECT kind, enabled, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2`, orgID, name).Scan(&kind, &enabled, &manifest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// No row = not activated (fail-closed, for built-ins too).
		if _, ok := target.Get(name); ok {
			return nil, fmt.Errorf("%w: %s is not activated", ErrNotFound, name)
		}
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: %s is disabled", ErrNotFound, name)
	}
	switch kind {
	case "builtin":
		if sys, ok := target.Get(name); ok {
			return sys, nil
		}
		return nil, ErrNotFound
	case "mcp":
		c, err := mcp.ParseConfig(manifest)
		if err != nil {
			return nil, fmt.Errorf("stored mcp config %s: %w", name, err)
		}
		return mcp.NewSystem(c), nil
	default:
		m, err := target.ParseManifest(manifest)
		if err != nil {
			return nil, fmt.Errorf("stored manifest %s: %w", name, err)
		}
		return target.NewManifestSystem(m), nil
	}
}

// PutMCP validates and stores an MCP server configuration (kind=mcp). As with
// the manifest, the name of a built-in is off limits. Existing tools are kept
// if the call does not supply them (discovery runs separately).
func (s *Store) PutMCP(ctx context.Context, orgID uuid.UUID, cfg mcp.Config) (mcp.Config, error) {
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if _, ok := target.Get(cfg.Name); ok {
		return cfg, fmt.Errorf("name %q is taken by a built-in plugin", cfg.Name)
	}
	if prev, err := s.MCPConfig(ctx, orgID, cfg.Name); err == nil && len(cfg.Tools) == 0 {
		cfg.Tools = prev.Tools
	}
	norm, err := json.Marshal(cfg)
	if err != nil {
		return cfg, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled, manifest)
		VALUES ($1,$2,'mcp',TRUE,$3)
		ON CONFLICT (org_id, name) DO UPDATE SET kind='mcp', manifest=$3, updated_at=now()`,
		orgID, cfg.Name, norm)
	return cfg, err
}

// MCPConfig reads the stored config of an MCP plugin.
func (s *Store) MCPConfig(ctx context.Context, orgID uuid.UUID, name string) (mcp.Config, error) {
	var kind string
	var manifest []byte
	err := s.pool.QueryRow(ctx, `SELECT kind, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2`, orgID, name).Scan(&kind, &manifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return mcp.Config{}, ErrNotFound
	}
	if err != nil {
		return mcp.Config{}, err
	}
	if kind != "mcp" {
		return mcp.Config{}, fmt.Errorf("%w: %s is not an mcp plugin", ErrNotFound, name)
	}
	return mcp.ParseConfig(manifest)
}

// SetMCPTools persists the tool list found by discovery into the config.
func (s *Store) SetMCPTools(ctx context.Context, orgID uuid.UUID, name string, tools []mcp.Tool) error {
	cfg, err := s.MCPConfig(ctx, orgID, name)
	if err != nil {
		return err
	}
	cfg.Tools = tools
	norm, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE target_plugins SET manifest=$3, updated_at=now()
		WHERE org_id=$1 AND name=$2 AND kind='mcp'`, orgID, name, norm)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Kind returns the plugin type (builtin|custom|mcp) — the broker branches on it
// (e.g. for mcp the token/URL secrets are optional).
func (s *Store) Kind(ctx context.Context, orgID uuid.UUID, name string) (string, error) {
	var kind string
	err := s.pool.QueryRow(ctx, `SELECT kind FROM target_plugins WHERE org_id=$1 AND name=$2`,
		orgID, name).Scan(&kind)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if _, ok := target.Get(name); ok {
			return "builtin", nil
		}
		return "", ErrNotFound
	case err != nil:
		return "", err
	}
	return kind, nil
}

// BrokeredDefinition returns the plugin definition of an activated
// non-built-in to be brokered into the sandbox: the type (custom|mcp) and the
// raw JSON definition. From it the daemon builds the matching target.System
// (fail-closed).
func (s *Store) BrokeredDefinition(ctx context.Context, orgID uuid.UUID, name string) (kind string, raw json.RawMessage, err error) {
	var enabled bool
	var manifest []byte
	err = s.pool.QueryRow(ctx, `SELECT kind, enabled, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2 AND kind IN ('custom','mcp')`, orgID, name).Scan(&kind, &enabled, &manifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, ErrNotFound
	}
	if err != nil {
		return "", nil, err
	}
	if !enabled {
		return "", nil, fmt.Errorf("%w: %s is disabled", ErrNotFound, name)
	}
	return kind, manifest, nil
}

// Manifest returns the stored manifest of an activated custom plugin (for the
// daemon, which needs it for execution).
func (s *Store) Manifest(ctx context.Context, orgID uuid.UUID, name string) (target.Manifest, error) {
	var enabled bool
	var manifest []byte
	err := s.pool.QueryRow(ctx, `SELECT enabled, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2 AND kind='custom'`, orgID, name).Scan(&enabled, &manifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return target.Manifest{}, ErrNotFound
	}
	if err != nil {
		return target.Manifest{}, err
	}
	if !enabled {
		return target.Manifest{}, fmt.Errorf("%w: %s is disabled", ErrNotFound, name)
	}
	return target.ParseManifest(manifest)
}

// SystemDoc is the prompt doc of a target system together with its name: what
// an agent can do there, in exactly the wording that also stands in its prompt.
// The prompt only needs the text; the web interface shows the actions per
// system and therefore needs the mapping.
type SystemDoc struct {
	System string `json:"system"`
	Doc    string `json:"doc"`
}

// DocsForAgent collects the prompt docs of the target systems activated for
// the organization (built-ins, manifest and MCP plugins) AND granted to the
// agent in ACCESS.md, for MCP filtered down to the tools assigned to the agent
// (an empty assignment = all tools).
//
// Two conditions, and the second one used to be missing. The organisation's
// activation decided alone which docs went into a prompt — so every agent
// carried the instructions for every enabled system around, including the ones
// whose credentials the broker refuses it (HasAccess, ACCESS.md). That is wrong
// twice over: it invites the agent to attempt something that cannot work, and
// it is expensive. The built-in docs measure around 11,000 tokens in total,
// GitLab and GitHub about 4,000 each — and they sit in the context of EVERY
// turn. A run of tester-1 read 2.34 million cached tokens across 31 turns;
// every token that need not be in there is saved 31 times over.
//
// Activation is opt-in (fail-closed) on both sides: neither a built-in without
// a row with enabled=TRUE nor a system without a line in ACCESS.md shows up.
func (s *Store) DocsForAgent(ctx context.Context, orgID, agentID uuid.UUID) ([]SystemDoc, error) {
	grantedScopes, err := s.agentScopeSet(ctx, agentID)
	if err != nil {
		return nil, err
	}
	granted := make(map[string]bool, len(grantedScopes))
	for name := range grantedScopes {
		granted[name] = true
	}
	rows, err := s.pool.Query(ctx,
		`SELECT name, kind, enabled, manifest FROM target_plugins WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	enabledBuiltin := map[string]bool{}
	var docs []SystemDoc
	for rows.Next() {
		var name, kind string
		var enabled bool
		var manifest []byte
		if err := rows.Scan(&name, &kind, &enabled, &manifest); err != nil {
			return nil, err
		}
		if !enabled || !granted[name] {
			continue
		}
		switch kind {
		case "builtin":
			enabledBuiltin[name] = true
		case "custom":
			if m, err := target.ParseManifest(manifest); err == nil {
				docs = append(docs, SystemDoc{System: name, Doc: target.NewManifestSystem(m).PromptDoc()})
			}
		case "mcp":
			if c, err := mcp.ParseConfig(manifest); err == nil {
				only, err := s.agentToolSet(ctx, agentID, name)
				if err != nil {
					return nil, err
				}
				if doc := mcp.NewSystem(c).PromptDocFor(only); doc != "" {
					docs = append(docs, SystemDoc{System: name, Doc: doc})
				}
			}
		}
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	// Built-ins only with explicit activation (opt-in, fail-closed) — and only
	// with the part of their doc the agent's scopes cover (scopedDoc).
	for _, d := range target.All() {
		if enabledBuiltin[d.Name] && granted[d.Name] && d.System != nil {
			docs = append(docs, SystemDoc{System: d.Name, Doc: scopedDoc(d.System, grantedScopes[d.Name])})
		}
	}
	return docs, nil
}

// EnabledDocsForAgent returns the same docs as a plain text list — the form in
// which they are compiled into the system prompt.
func (s *Store) EnabledDocsForAgent(ctx context.Context, orgID, agentID uuid.UUID) ([]string, error) {
	docs, err := s.DocsForAgent(ctx, orgID, agentID)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(docs))
	for _, d := range docs {
		out = append(out, d.Doc)
	}
	return out, nil
}

// agentScopeSet is the set of target systems the agent has a line for in
// ACCESS.md, each with its scopes — the same table the credential broker asks
// (agents.HasAccess). Read here rather than through the agent registry so the
// target store does not have to depend on it for one query. The scopes narrow
// the prompt doc of systems that support it (target.ScopedDocSystem).
func (s *Store) agentScopeSet(ctx context.Context, agentID uuid.UUID) (map[string][]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT system, scopes FROM system_accesses WHERE agent_id=$1`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string][]string{}
	for rows.Next() {
		var name string
		var scopes []string
		if err := rows.Scan(&name, &scopes); err != nil {
			return nil, err
		}
		set[strings.TrimSpace(name)] = scopes
	}
	return set, rows.Err()
}

// scopedDoc is a built-in's prompt doc, narrowed to the granted scopes where
// the plugin supports it. Fail-open on both sides: a system without
// ScopedDocSystem and an agent without recorded scopes both get the full doc.
func scopedDoc(sys target.System, scopes []string) string {
	if sc, ok := sys.(target.ScopedDocSystem); ok && len(scopes) > 0 {
		return sc.PromptDocForScopes(scopes)
	}
	return sys.PromptDoc()
}

// AgentTools returns the tools assigned to an agent for a system. An empty
// result = no restriction (all tools allowed).
func (s *Store) AgentTools(ctx context.Context, agentID uuid.UUID, system string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT tool FROM agent_target_tools WHERE agent_id=$1 AND system=$2 ORDER BY tool`,
		agentID, system)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// agentToolSet returns the assignment as a set or nil (= no restriction).
func (s *Store) agentToolSet(ctx context.Context, agentID uuid.UUID, system string) (map[string]bool, error) {
	tools, err := s.AgentTools(ctx, agentID, system)
	if err != nil || len(tools) == 0 {
		return nil, err
	}
	set := make(map[string]bool, len(tools))
	for _, t := range tools {
		set[t] = true
	}
	return set, nil
}

// SetAgentTools atomically replaces an agent's tool assignment for a system.
// An empty list = drop the assignment (all tools allowed).
func (s *Store) SetAgentTools(ctx context.Context, agentID uuid.UUID, system string, tools []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx,
		`DELETE FROM agent_target_tools WHERE agent_id=$1 AND system=$2`, agentID, system); err != nil {
		return err
	}
	for _, t := range tools {
		if t == "" {
			continue
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO agent_target_tools (agent_id, system, tool) VALUES ($1,$2,$3)
			 ON CONFLICT DO NOTHING`, agentID, system, t); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// AgentToolAllowed is the central enforcement point for the tool assignment
// (fail-closed per system as soon as an allowlist exists): no rows for
// (agent, system) = allowed; otherwise the tool must be listed.
func (s *Store) AgentToolAllowed(ctx context.Context, agentID uuid.UUID, system, tool string) (bool, error) {
	var count, hit int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*), count(*) FILTER (WHERE tool=$3)
		   FROM agent_target_tools WHERE agent_id=$1 AND system=$2`,
		agentID, system, tool).Scan(&count, &hit)
	if err != nil {
		return false, err
	}
	if count == 0 {
		return true, nil // no allowlist → no restriction
	}
	return hit > 0, nil
}
