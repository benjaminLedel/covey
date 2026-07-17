// Package agents hält die Agent-Registry und die Config-Kompilierung
// (SOUL.md & Co. → System-Prompt), siehe spec/02-agenten-modell.md.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Agent-Status der Zustandsmaschine aus spec/03-lifecycle-scheduling.md.
// 'blocked' lebt auf der Aufgabe, nicht auf dem Agenten: ein Agent mit einer
// geparkten Aufgabe ist wieder 'sleeping' und kann anderes bearbeiten.
const (
	StatusSleeping  = "sleeping"
	StatusTriggered = "triggered"
	StatusTriage    = "triage"
	StatusWorking   = "working"
	StatusKilled    = "killed"
)

type Agent struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	Slug        string     `json:"slug"`
	DisplayName string     `json:"display_name"`
	Runtime     string     `json:"runtime"`
	Status      string     `json:"status"`
	OwnerID     *uuid.UUID `json:"owner_id,omitempty"`
	// SupervisorID ist der Platz im Org-Chart: der Mensch, an den der Agent
	// berichtet und eskaliert (spec/02).
	SupervisorID *uuid.UUID `json:"supervisor_id,omitempty"`
	Killed      bool       `json:"killed"`
	BudgetUSD   float64    `json:"budget_usd"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

type ConfigVersion struct {
	ID             uuid.UUID         `json:"id"`
	AgentID        uuid.UUID         `json:"agent_id"`
	Version        int               `json:"version"`
	Files          map[string]string `json:"files"`
	CompiledPrompt string            `json:"compiled_prompt"`
	CreatedAt      time.Time         `json:"created_at"`
}

type SystemAccess struct {
	System string   `json:"system"`
	Scopes []string `json:"scopes"`
	// Tools ist die Tool-Allowlist des Agenten für dieses System (MCP);
	// leer = alle Tools erlaubt. Materialisiert wird sie nicht hier, sondern
	// im target-Store (agent_target_tools) — ACCESS.md ist die Text-Sicht.
	Tools []string `json:"tools,omitempty"`
}

var ErrNotFound = errors.New("agent nicht gefunden")

type Registry struct {
	pool *pgxpool.Pool
}

func NewRegistry(pool *pgxpool.Pool) *Registry { return &Registry{pool: pool} }

const agentCols = "id, org_id, slug, display_name, runtime, status, owner_id, supervisor_id, killed, budget_usd, created_at, updated_at"

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.OrgID, &a.Slug, &a.DisplayName, &a.Runtime, &a.Status,
		&a.OwnerID, &a.SupervisorID, &a.Killed, &a.BudgetUSD, &a.CreatedAt, &a.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return a, ErrNotFound
	}
	return a, err
}

func (r *Registry) Create(ctx context.Context, orgID uuid.UUID, slug, displayName, runtime string, ownerID *uuid.UUID) (Agent, error) {
	if runtime == "" {
		runtime = "claude-code"
	}
	row := r.pool.QueryRow(ctx, `INSERT INTO agents (id, org_id, slug, display_name, runtime, owner_id)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING `+agentCols,
		uuid.New(), orgID, slug, displayName, runtime, ownerID)
	return scanAgent(row)
}

func (r *Registry) Get(ctx context.Context, id uuid.UUID) (Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE id=$1", id))
}

func (r *Registry) GetBySlug(ctx context.Context, orgID uuid.UUID, slug string) (Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE org_id=$1 AND slug=$2", orgID, slug))
}

func (r *Registry) List(ctx context.Context, orgID uuid.UUID) ([]Agent, error) {
	rows, err := r.pool.Query(ctx, "SELECT "+agentCols+" FROM agents WHERE org_id=$1 ORDER BY created_at", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Agent
	for rows.Next() {
		a, err := scanAgent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (r *Registry) SetStatus(ctx context.Context, id uuid.UUID, status string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET status=$2, updated_at=now() WHERE id=$1", id, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetKilled setzt den Kill-Switch eines einzelnen Agenten (spec/06).
func (r *Registry) SetKilled(ctx context.Context, id uuid.UUID, killed bool) error {
	status := StatusKilled
	if !killed {
		status = StatusSleeping
	}
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET killed=$2, status=$3, updated_at=now() WHERE id=$1", id, killed, status)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetFleetKilled ist der flottenweite Notaus.
func (r *Registry) SetFleetKilled(ctx context.Context, orgID uuid.UUID, killed bool) error {
	_, err := r.pool.Exec(ctx, "UPDATE organizations SET fleet_killed=$2 WHERE id=$1", orgID, killed)
	return err
}

func (r *Registry) FleetKilled(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var v bool
	err := r.pool.QueryRow(ctx, "SELECT fleet_killed FROM organizations WHERE id=$1", orgID).Scan(&v)
	return v, err
}

func (r *Registry) SetBudget(ctx context.Context, id uuid.UUID, budgetUSD float64) error {
	_, err := r.pool.Exec(ctx, "UPDATE agents SET budget_usd=$2, updated_at=now() WHERE id=$1", id, budgetUSD)
	return err
}

// SetRuntime schaltet die Runtime eines Agenten um. Greift beim nächsten
// Task-Dispatch — laufende Sessions bleiben unberührt.
func (r *Registry) SetRuntime(ctx context.Context, id uuid.UUID, runtime string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET runtime=$2, updated_at=now() WHERE id=$1", id, runtime)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SupervisorName liefert den Anzeigenamen des Vorgesetzten aus dem Org-Chart —
// für Eskalations-Texte. Leer, wenn kein Vorgesetzter zugeordnet ist.
func (r *Registry) SupervisorName(ctx context.Context, agentID uuid.UUID) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(h.display_name, '')
		FROM agents a LEFT JOIN humans h ON h.id = a.supervisor_id
		WHERE a.id=$1`, agentID).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return name, err
}

// SetSupervisor hängt den Agenten im Org-Chart um; nil löst die Zuordnung.
func (r *Registry) SetSupervisor(ctx context.Context, id uuid.UUID, supervisorID *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET supervisor_id=$2, updated_at=now() WHERE id=$1", id, supervisorID)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SaveConfig legt eine neue Config-Version an (bestehende werden nie editiert),
// kompiliert den System-Prompt und materialisiert ACCESS.md für den Broker.
func (r *Registry) SaveConfig(ctx context.Context, agentID uuid.UUID, files map[string]string, createdBy *uuid.UUID) (ConfigVersion, error) {
	compiled := CompilePrompt(files)
	accesses := ParseAccess(files["ACCESS.md"])

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return ConfigVersion{}, err
	}
	defer tx.Rollback(ctx)

	var version int
	if err := tx.QueryRow(ctx,
		"SELECT COALESCE(MAX(version),0)+1 FROM agent_config_versions WHERE agent_id=$1", agentID).Scan(&version); err != nil {
		return ConfigVersion{}, err
	}
	filesJSON, err := json.Marshal(files)
	if err != nil {
		return ConfigVersion{}, err
	}
	cv := ConfigVersion{ID: uuid.New(), AgentID: agentID, Version: version, Files: files, CompiledPrompt: compiled}
	if err := tx.QueryRow(ctx, `INSERT INTO agent_config_versions (id, agent_id, version, files, compiled_prompt, created_by)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		cv.ID, agentID, version, filesJSON, compiled, createdBy).Scan(&cv.CreatedAt); err != nil {
		return ConfigVersion{}, err
	}

	if _, err := tx.Exec(ctx, "DELETE FROM system_accesses WHERE agent_id=$1", agentID); err != nil {
		return ConfigVersion{}, err
	}
	for _, acc := range accesses {
		if _, err := tx.Exec(ctx, "INSERT INTO system_accesses (agent_id, system, scopes) VALUES ($1,$2,$3)",
			agentID, acc.System, acc.Scopes); err != nil {
			return ConfigVersion{}, err
		}
	}
	return cv, tx.Commit(ctx)
}

// CurrentConfig liefert die jüngste Config-Version des Agenten.
func (r *Registry) CurrentConfig(ctx context.Context, agentID uuid.UUID) (ConfigVersion, error) {
	var cv ConfigVersion
	var filesJSON []byte
	err := r.pool.QueryRow(ctx, `SELECT id, agent_id, version, files, compiled_prompt, created_at
		FROM agent_config_versions WHERE agent_id=$1 ORDER BY version DESC LIMIT 1`, agentID).
		Scan(&cv.ID, &cv.AgentID, &cv.Version, &filesJSON, &cv.CompiledPrompt, &cv.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return cv, ErrNotFound
	}
	if err != nil {
		return cv, err
	}
	if err := json.Unmarshal(filesJSON, &cv.Files); err != nil {
		return cv, fmt.Errorf("config files: %w", err)
	}
	return cv, nil
}

// Accesses liefert die materialisierten Zugänge aus ACCESS.md (Broker-Prüfung).
func (r *Registry) Accesses(ctx context.Context, agentID uuid.UUID) ([]SystemAccess, error) {
	rows, err := r.pool.Query(ctx, "SELECT system, scopes FROM system_accesses WHERE agent_id=$1", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SystemAccess
	for rows.Next() {
		var a SystemAccess
		if err := rows.Scan(&a.System, &a.Scopes); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// HasAccess prüft Agent→System(+Scope) — die Berechtigungsfrage des Brokers.
func (r *Registry) HasAccess(ctx context.Context, agentID uuid.UUID, system, scope string) (bool, error) {
	accs, err := r.Accesses(ctx, agentID)
	if err != nil {
		return false, err
	}
	for _, a := range accs {
		if a.System != system {
			continue
		}
		if scope == "" {
			return true, nil
		}
		for _, s := range a.Scopes {
			if s == scope {
				return true, nil
			}
		}
	}
	return false, nil
}
