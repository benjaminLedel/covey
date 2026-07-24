// Package agents hält die Agent-Registry und die Config-Kompilierung
// (SOUL.md & Co. → System-Prompt), siehe spec/02-agenten-modell.md.
package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/org"
)

var slugRe = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

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
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Slug        string    `json:"slug"`
	DisplayName string    `json:"display_name"`
	Runtime     string    `json:"runtime"`
	// Model wählt das LLM innerhalb der Runtime (z. B. claude-opus-4-8);
	// leer = die Runtime nutzt ihren eigenen Default.
	Model string `json:"model"`
	// MaxTurns begrenzt die Turns eines Runtime-Laufs (Runaway-Guard);
	// 0 = Default des Orchestrators.
	MaxTurns int        `json:"max_turns"`
	Status   string     `json:"status"`
	OwnerID  *uuid.UUID `json:"owner_id,omitempty"`
	// SupervisorID ist der Platz im Org-Chart: der Mensch, an den der Agent
	// berichtet und eskaliert (spec/02).
	SupervisorID *uuid.UUID `json:"supervisor_id,omitempty"`
	// DepartmentID ordnet den Agenten einer Abteilung zu; nil = keiner.
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	// Profile sind dieselben Mitarbeiter-Stammdaten wie bei den Menschen
	// (org.Profile): Funktion, Kontakt, Plattform-Kennungen und die Werte der
	// org-weit konfigurierbaren Profilfelder — Agenten sind Mitarbeiter (spec/02).
	org.Profile
	Killed    bool    `json:"killed"`
	BudgetUSD float64 `json:"budget_usd"`
	// WebhookToken ist das Geheimnis des optionalen generischen Webhook-
	// Triggers (nil = deaktiviert). Bewusst nicht im JSON — lesbar nur über
	// den dedizierten Webhook-Endpoint (Manager-Rollen).
	WebhookToken *string   `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
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

const agentCols = "id, org_id, slug, display_name, runtime, model, max_turns, status, owner_id, supervisor_id, department_id, job_title, identities, phone, responsibilities, custom, killed, budget_usd, webhook_token, created_at, updated_at"

func scanAgent(row pgx.Row) (Agent, error) {
	var a Agent
	err := row.Scan(&a.ID, &a.OrgID, &a.Slug, &a.DisplayName, &a.Runtime, &a.Model, &a.MaxTurns, &a.Status,
		&a.OwnerID, &a.SupervisorID, &a.DepartmentID, &a.JobTitle, &a.Identities, &a.Phone, &a.Responsibilities, &a.Custom,
		&a.Killed, &a.BudgetUSD, &a.WebhookToken, &a.CreatedAt, &a.UpdatedAt)
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

// SetModel legt das Modell des Agenten fest (leer = Runtime-Default).
// Greift wie SetRuntime beim nächsten Task-Dispatch.
func (r *Registry) SetModel(ctx context.Context, id uuid.UUID, model string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET model=$2, updated_at=now() WHERE id=$1", id, model)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetMaxTurns legt das Turn-Limit je Runtime-Lauf fest (0 = Default des
// Orchestrators). Greift wie SetModel beim nächsten Task-Dispatch.
func (r *Registry) SetMaxTurns(ctx context.Context, id uuid.UUID, maxTurns int) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET max_turns=$2, updated_at=now() WHERE id=$1", id, maxTurns)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// Rename ändert den Anzeigenamen. Slug separat über SetSlug änderbar.
// Delete löscht einen Agenten endgültig. Alle abhängigen Daten (Config,
// Backlog, Secrets, Zuweisungen) werden per ON DELETE CASCADE mitgelöscht.
// orgID verhindert Cross-Org-Löschung über geratene IDs.
func (r *Registry) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, "DELETE FROM agents WHERE id=$1 AND org_id=$2", id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// supervisor_id trägt keinen DB-Fremdschlüssel mehr (Migration 0025):
	// verweisende Untergebene hier lösen, damit keine Referenz ins Leere zeigt.
	_, _ = r.pool.Exec(ctx, "UPDATE agents SET supervisor_id=NULL WHERE supervisor_id=$1 AND org_id=$2", id, orgID)
	return nil
}

func (r *Registry) Rename(ctx context.Context, id uuid.UUID, displayName string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET display_name=$2, updated_at=now() WHERE id=$1", id, displayName)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// SetSlug ändert den Slug eines Agenten. Der neue Slug muss innerhalb der Org
// eindeutig sein — die DB-Unique-Constraint (org_id, slug) sorgt dafür.
// Erlaubtes Format: nur Kleinbuchstaben, Ziffern und Bindestriche.
func (r *Registry) SetSlug(ctx context.Context, id uuid.UUID, slug string) error {
	if !slugRe.MatchString(slug) {
		return fmt.Errorf("slug darf nur Kleinbuchstaben, Ziffern und Bindestriche enthalten")
	}
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET slug=$2, updated_at=now() WHERE id=$1", id, slug)
	if err != nil {
		if strings.Contains(err.Error(), "unique") || strings.Contains(err.Error(), "duplicate") {
			return fmt.Errorf("slug bereits vergeben")
		}
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetWebhookToken setzt bzw. rotiert das Token des generischen Webhook-
// Triggers; nil deaktiviert den Webhook (spec/03, Wake-Quelle Event).
func (r *Registry) SetWebhookToken(ctx context.Context, id uuid.UUID, token *string) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET webhook_token=$2, updated_at=now() WHERE id=$1", id, token)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// GetByWebhookToken löst das Trigger-Token auf den Agenten auf —
// die Authentifizierung des öffentlichen Webhook-Endpoints.
func (r *Registry) GetByWebhookToken(ctx context.Context, token string) (Agent, error) {
	return scanAgent(r.pool.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE webhook_token=$1", token))
}

// SupervisorName liefert den Anzeigenamen des Vorgesetzten aus dem Org-Chart —
// für Eskalations-Texte. Leer, wenn kein Vorgesetzter zugeordnet ist.
func (r *Registry) SupervisorName(ctx context.Context, agentID uuid.UUID) (string, error) {
	var name string
	err := r.pool.QueryRow(ctx, `SELECT COALESCE(h.display_name, sa.display_name, '')
		FROM agents a
		LEFT JOIN humans h ON h.id = a.supervisor_id
		LEFT JOIN agents sa ON sa.id = a.supervisor_id
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

// ProfileUpdate ist ein partielles Profil-Update — nil-Felder bleiben
// unverändert; die Maps ersetzen bei nicht-nil den kompletten Bestand
// (der Store normalisiert). Spiegelbild der Profil-Felder von org.HumanUpdate.
type ProfileUpdate struct {
	JobTitle         *string
	Identities       map[string]string
	Phone            *string
	Responsibilities *string
	Custom           map[string]string
}

// UpdateProfile schreibt die Mitarbeiter-Stammdaten des Agenten. orgID
// verhindert Cross-Org-Zugriff über geratene IDs.
func (r *Registry) UpdateProfile(ctx context.Context, orgID, id uuid.UUID, upd ProfileUpdate) (Agent, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return Agent{}, err
	}
	defer tx.Rollback(ctx)

	a, err := scanAgent(tx.QueryRow(ctx, "SELECT "+agentCols+" FROM agents WHERE id=$1 AND org_id=$2 FOR UPDATE", id, orgID))
	if err != nil {
		return Agent{}, err
	}
	if upd.JobTitle != nil {
		a.JobTitle = *upd.JobTitle
	}
	if upd.Identities != nil {
		a.Identities = org.NormalizeIdentities(upd.Identities)
	}
	if upd.Phone != nil {
		a.Phone = *upd.Phone
	}
	if upd.Responsibilities != nil {
		a.Responsibilities = *upd.Responsibilities
	}
	if upd.Custom != nil {
		a.Custom = org.NormalizeCustom(upd.Custom)
	}
	if a.Identities == nil {
		a.Identities = map[string]string{}
	}
	if a.Custom == nil {
		a.Custom = map[string]string{}
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET job_title=$2, identities=$3, phone=$4,
			responsibilities=$5, custom=$6, updated_at=now() WHERE id=$1`,
		id, a.JobTitle, a.Identities, a.Phone, a.Responsibilities, a.Custom); err != nil {
		return Agent{}, err
	}
	return a, tx.Commit(ctx)
}

// SetDepartment weist den Agenten einer Abteilung zu; nil löst die Zuordnung.
func (r *Registry) SetDepartment(ctx context.Context, id uuid.UUID, deptID *uuid.UUID) error {
	tag, err := r.pool.Exec(ctx, "UPDATE agents SET department_id=$2, updated_at=now() WHERE id=$1", id, deptID)
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
	heartbeats, err := ParseHeartbeat(files["HEARTBEAT.md"])
	if err != nil {
		return ConfigVersion{}, fmt.Errorf("HEARTBEAT.md: %w", err)
	}

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

	// Heartbeats materialisieren: Upsert je titel, damit last_fired_at über
	// Config-Versionen hinweg erhalten bleibt; entfernte Einträge löschen.
	names := make([]string, 0, len(heartbeats))
	for _, hb := range heartbeats {
		names = append(names, hb.Name)
		var everySeconds *int64
		var dailyAt *string
		if hb.Every > 0 {
			s := int64(hb.Every / time.Second)
			everySeconds = &s
		} else {
			dailyAt = &hb.DailyAt
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_heartbeats (agent_id, name, task_body, every_seconds, daily_at, only_if)
			VALUES ($1,$2,$3,$4,$5,$6)
			ON CONFLICT (agent_id, name) DO UPDATE
			SET task_body=EXCLUDED.task_body, every_seconds=EXCLUDED.every_seconds, daily_at=EXCLUDED.daily_at,
			    only_if=EXCLUDED.only_if`,
			agentID, hb.Name, hb.Task, everySeconds, dailyAt, hb.OnlyIf); err != nil {
			return ConfigVersion{}, err
		}
	}
	if _, err := tx.Exec(ctx, "DELETE FROM agent_heartbeats WHERE agent_id=$1 AND NOT (name = ANY($2))",
		agentID, names); err != nil {
		return ConfigVersion{}, err
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
