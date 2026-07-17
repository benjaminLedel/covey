// Package store verwaltet Zielsystem-Plugins pro Organisation: die
// Aktivierung der kompilierten Built-ins und die hochgeladenen
// Manifest-Plugins (kind=custom). Control-Plane-seitig — der Daemon
// bekommt Manifeste über das Daemon-Protokoll gebrokert.
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

var ErrNotFound = errors.New("zielsystem nicht gefunden")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Plugin ist die UI-/API-Sicht auf ein Zielsystem-Plugin.
type Plugin struct {
	Name        string          `json:"name"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"` // builtin | custom | mcp
	Enabled     bool            `json:"enabled"`
	Manifest    json.RawMessage `json:"manifest,omitempty"` // custom: Manifest, mcp: Config
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
	// SetupDoc ist die Einrichtungs-Anleitung fürs UI: bei Built-ins aus dem
	// Plugin-Descriptor, bei Manifest-/MCP-Plugins generisch erzeugt.
	SetupDoc string `json:"setup_doc,omitempty"`
}

// List mergt die kompilierte Registry mit den DB-Zeilen der Organisation.
// Built-ins ohne Zeile gelten als aktiviert (Bestandsschutz).
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
		p := Plugin{Name: d.Name, Label: d.Label, Description: d.Description, Kind: "builtin", Enabled: true, SetupDoc: d.SetupDoc}
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
			if m, err := target.ParseManifest(p.Manifest); err == nil {
				p.Label, p.Description = m.Label, m.Description
				if p.Label == "" {
					p.Label = m.Name
				}
			}
			p.SetupDoc = customSetupDoc(p.Name)
		case "mcp":
			if c, err := mcp.ParseConfig(p.Manifest); err == nil {
				p.Label, p.Description = c.Label, c.Description
				if p.Label == "" {
					p.Label = c.Name
				}
			}
			p.SetupDoc = mcpSetupDoc(p.Name)
		default:
			continue // Zeile eines Built-ins, das dieses Binary nicht mitkompiliert hat
		}
		out = append(out, p)
	}
	return out, nil
}

// customSetupDoc ist die generische Einrichtungs-Anleitung eines
// Manifest-Plugins — Webhook-Route und Secret-Namen folgen der Konvention,
// die Details (Auth-Header, Aktionen) stehen im Manifest selbst.
func customSetupDoc(name string) string {
	return fmt.Sprintf(`1. Unter Secrets hinterlegen und dem Agenten zuweisen:
   %[1]s_url   = Basis-URL des Systems
   %[1]s_token = API-Token (wird über den im Manifest definierten Auth-Header gesendet)

2. In der ACCESS.md des Agenten freischalten:
   - system: %[1]s scope: read,write

3. Im Zielsystem einen Webhook auf diese URL einrichten:
   {public_url}/api/webhooks/%[1]s/<agent-slug>
   Signatur-Secret: Wert von COVEY_%[2]s_WEBHOOK_SECRET (Prozess-Env, leer = Prüfung aus)

Das Feld-Mapping des Webhooks und die verfügbaren Aktionen definiert das
hochgeladene JSON-Manifest.`, name, strings.ToUpper(name))
}

// mcpSetupDoc ist die generische Einrichtungs-Anleitung eines MCP-Plugins —
// kein Webhook-Eingang; die Tools laufen über den Action-Proxy.
func mcpSetupDoc(name string) string {
	return fmt.Sprintf(`1. Unter Secrets hinterlegen und dem Agenten zuweisen (falls der Server Auth verlangt):
   %[1]s_token = Token für den MCP-Server

2. In der ACCESS.md des Agenten freischalten (optional mit Tool-Allowlist):
   - system: %[1]s scope: read,write   tools: alle

3. Tool-Liste aktuell halten: "Tools aktualisieren" auf dieser Karte führt die
   Discovery (tools/list) erneut aus.

MCP-Systeme haben keinen Webhook-Eingang — Aufgaben entstehen über Backlog
oder andere Zielsysteme; die Tools ruft der Agent über den Action-Proxy auf.`, name)
}

// SetEnabled schaltet ein Plugin für die Organisation an/aus. Für Built-ins
// wird die Zeile bei Bedarf angelegt; unbekannte Namen sind ein Fehler.
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

// PutManifest validiert und speichert ein hochgeladenes Manifest-Plugin.
// Der Name eines kompilierten Built-ins ist tabu — kein stilles Überschatten.
func (s *Store) PutManifest(ctx context.Context, orgID uuid.UUID, raw []byte) (target.Manifest, error) {
	m, err := target.ParseManifest(raw)
	if err != nil {
		return m, err
	}
	if _, ok := target.Get(m.Name); ok {
		return m, fmt.Errorf("name %q ist durch ein eingebautes Plugin belegt", m.Name)
	}
	// Normalisiert (kompaktes JSON) speichern, nicht den Roh-Upload.
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

// DeleteManifest entfernt ein Custom-Plugin. Built-ins lassen sich nur
// deaktivieren, nicht löschen.
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

// System löst ein Zielsystem für eine Organisation auf — nur wenn aktiviert
// (fail-closed): erst die kompilierte Registry, dann die Custom-Manifeste.
func (s *Store) System(ctx context.Context, orgID uuid.UUID, name string) (target.System, error) {
	var kind string
	var enabled bool
	var manifest []byte
	err := s.pool.QueryRow(ctx, `SELECT kind, enabled, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2`, orgID, name).Scan(&kind, &enabled, &manifest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Keine Zeile: Built-ins gelten als aktiviert, alles andere gibt es nicht.
		if sys, ok := target.Get(name); ok {
			return sys, nil
		}
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: %s ist deaktiviert", ErrNotFound, name)
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
			return nil, fmt.Errorf("gespeicherte mcp-config %s: %w", name, err)
		}
		return mcp.NewSystem(c), nil
	default:
		m, err := target.ParseManifest(manifest)
		if err != nil {
			return nil, fmt.Errorf("gespeichertes manifest %s: %w", name, err)
		}
		return target.NewManifestSystem(m), nil
	}
}

// PutMCP validiert und speichert eine MCP-Server-Konfiguration (kind=mcp).
// Wie beim Manifest ist der Name eines Built-ins tabu. Bestehende Tools bleiben
// erhalten, wenn der Aufruf sie nicht mitliefert (Discovery läuft separat).
func (s *Store) PutMCP(ctx context.Context, orgID uuid.UUID, cfg mcp.Config) (mcp.Config, error) {
	if err := cfg.Validate(); err != nil {
		return cfg, err
	}
	if _, ok := target.Get(cfg.Name); ok {
		return cfg, fmt.Errorf("name %q ist durch ein eingebautes Plugin belegt", cfg.Name)
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

// MCPConfig liest die gespeicherte Config eines MCP-Plugins.
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
		return mcp.Config{}, fmt.Errorf("%w: %s ist kein mcp-plugin", ErrNotFound, name)
	}
	return mcp.ParseConfig(manifest)
}

// SetMCPTools persistiert die per Discovery entdeckte Tool-Liste in der Config.
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

// Kind liefert den Plugin-Typ (builtin|custom|mcp) — der Broker verzweigt
// danach (z. B. sind für mcp Token/URL-Secrets optional).
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

// BrokeredDefinition liefert die in die Sandbox zu brokernde Plugin-Definition
// eines aktivierten Nicht-Built-ins: den Typ (custom|mcp) und die rohe JSON-
// Definition. Der Daemon baut daraus das passende target.System (fail-closed).
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
		return "", nil, fmt.Errorf("%w: %s ist deaktiviert", ErrNotFound, name)
	}
	return kind, manifest, nil
}

// Manifest liefert das gespeicherte Manifest eines aktivierten Custom-Plugins
// (für den Daemon, der es zur Ausführung braucht).
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
		return target.Manifest{}, fmt.Errorf("%w: %s ist deaktiviert", ErrNotFound, name)
	}
	return target.ParseManifest(manifest)
}

// EnabledDocs sammelt die Prompt-Dokus aller für die Organisation
// aktivierten Zielsysteme (Built-ins + Custom-Manifeste).
func (s *Store) EnabledDocs(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, kind, enabled, manifest FROM target_plugins WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	disabled := map[string]bool{}
	var docs []string
	for rows.Next() {
		var name, kind string
		var enabled bool
		var manifest []byte
		if err := rows.Scan(&name, &kind, &enabled, &manifest); err != nil {
			return nil, err
		}
		if !enabled {
			disabled[name] = true
			continue
		}
		if kind == "custom" {
			if m, err := target.ParseManifest(manifest); err == nil {
				docs = append(docs, target.NewManifestSystem(m).PromptDoc())
			}
		}
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	for _, d := range target.All() {
		if !disabled[d.Name] && d.System != nil {
			docs = append(docs, d.System.PromptDoc())
		}
	}
	return docs, nil
}

// EnabledDocsForAgent ist wie EnabledDocs, filtert MCP-Tools aber auf die dem
// Agenten zugewiesenen (leere Zuweisung = alle Tools). Wird zur Dispatch-Zeit
// benutzt, damit der Prompt nur die tatsächlich nutzbaren Tools nennt.
func (s *Store) EnabledDocsForAgent(ctx context.Context, orgID, agentID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, kind, enabled, manifest FROM target_plugins WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	disabled := map[string]bool{}
	var docs []string
	for rows.Next() {
		var name, kind string
		var enabled bool
		var manifest []byte
		if err := rows.Scan(&name, &kind, &enabled, &manifest); err != nil {
			return nil, err
		}
		if !enabled {
			disabled[name] = true
			continue
		}
		switch kind {
		case "custom":
			if m, err := target.ParseManifest(manifest); err == nil {
				docs = append(docs, target.NewManifestSystem(m).PromptDoc())
			}
		case "mcp":
			if c, err := mcp.ParseConfig(manifest); err == nil {
				only, err := s.agentToolSet(ctx, agentID, name)
				if err != nil {
					return nil, err
				}
				if doc := mcp.NewSystem(c).PromptDocFor(only); doc != "" {
					docs = append(docs, doc)
				}
			}
		}
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	for _, d := range target.All() {
		if !disabled[d.Name] && d.System != nil {
			docs = append(docs, d.System.PromptDoc())
		}
	}
	return docs, nil
}

// AgentTools liefert die einem Agenten für ein System zugewiesenen Tools.
// Leeres Ergebnis = keine Einschränkung (alle Tools erlaubt).
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

// agentToolSet liefert die Zuweisung als Set oder nil (= keine Einschränkung).
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

// SetAgentTools ersetzt die Tool-Zuweisung eines Agenten für ein System
// atomar. Leere Liste = Zuweisung aufheben (alle Tools erlaubt).
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

// AgentToolAllowed ist der zentrale Enforcement-Punkt für die Tool-Zuweisung
// (fail-closed pro System, sobald eine Allowlist existiert): keine Zeilen für
// (agent, system) = erlaubt; sonst muss das Tool gelistet sein.
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
		return true, nil // keine Allowlist → keine Einschränkung
	}
	return hit > 0, nil
}
