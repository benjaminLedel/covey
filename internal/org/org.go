// Package org manages the tenants (organizations) and the humans within them
// (RBAC, spec/09). The organization is Covey's unit — this store carries the
// admin side: create/change/remove users, manage tenants. Protective rules (the
// last platform_admin stays) are enforced here, not in the UI.
package org

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("e-mail is already taken")
	// ErrLastAdmin guards against lockout: the last platform_admin of an
	// organization can neither be deleted nor demoted.
	ErrLastAdmin = errors.New("the last platform_admin of the organization cannot be removed")
	// ErrManagerCycle keeps the org chart acyclic: nobody can (transitively)
	// report to themselves.
	ErrManagerCycle = errors.New("manager relation would form a cycle")
)

type Organization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Description: what this organisation does, in a few sentences. Master data,
	// not a setup prompt — it goes into the config of newly drafted agents, into
	// every hiring brief and into the config copilot's system prompt (spec/20).
	Description string `json:"description"`
	// PlatformRepo names where this platform's own source lives — the target
	// system and the project on it (spec/21). Covey Doctor reads
	// the code there and files its issues there; both from the same address,
	// because reporting against code you have not read produces symptoms.
	// Empty = not set up, and then nothing about it stands in any prompt.
	PlatformRepoSystem  string    `json:"platform_repo_system"`
	PlatformRepoProject string    `json:"platform_repo_project"`
	FleetKilled         bool      `json:"fleet_killed"`
	HumanCount          int       `json:"human_count"`
	AgentCount          int       `json:"agent_count"`
	CreatedAt           time.Time `json:"created_at"`
}

type Human struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	// ManagerID is the manager relation in the org chart (spec/09);
	// nil = root (reports to nobody).
	ManagerID *uuid.UUID `json:"manager_id,omitempty"`
	// DepartmentID assigns the human to a department; nil = none.
	DepartmentID *uuid.UUID `json:"department_id,omitempty"`
	Profile
	CreatedAt time.Time `json:"created_at"`
}

// Profile is the employee master data beyond login and RBAC: role, contact and
// the identifiers in target systems. Agents get it as a team directory in their
// prompt — the GitLab username, for example, is what a bot uses to assign an
// issue to the right person for testing.
//
// Identities is deliberately a generic map system → identifier (e.g.
// {"gitlab": "maxm", "zammad": "max@company.com"}): target systems are plugins
// without a hardcoded list, the profiles follow the same principle — a new
// platform needs no schema or code change on the profile.
type Profile struct {
	JobTitle         string            `json:"job_title"`
	Identities       map[string]string `json:"identities"`
	Phone            string            `json:"phone"`
	Responsibilities string            `json:"responsibilities"`
	// Custom holds the values of the org-wide configurable profile fields
	// (profile_fields): key → value, e.g. {"location": "Berlin"}.
	Custom map[string]string `json:"custom"`
}

// NormalizeIdentities cleans up an identifier map: system keys lowercased and
// trimmed, values trimmed and without a leading "@" (copy & paste from
// GitLab/Slack), empty entries removed. Never nil — the JSONB column is
// NOT NULL.
func NormalizeIdentities(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		v = strings.TrimPrefix(strings.TrimSpace(v), "@")
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

// NormalizeCustom cleans up the values of the configurable profile fields:
// trimmed, empty entries removed. Keys are the stable field keys from
// profile_fields. Never nil — the JSONB column is NOT NULL.
func NormalizeCustom(in map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range in {
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	return out
}

// HumanUpdate is a partial update — nil fields stay unchanged. ManagerID
// distinguishes three cases: nil = unchanged, Valid=false = detach the
// assignment, Valid=true = new manager.
type HumanUpdate struct {
	DisplayName  *string
	Role         *string
	PasswordHash *string
	ManagerID    *uuid.NullUUID

	JobTitle         *string
	Phone            *string
	Responsibilities *string
	// Identities/Custom: nil = unchanged, otherwise a full replacement of the
	// respective map (normalized before writing).
	Identities map[string]string
	Custom     map[string]string
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// --- Humans (org-scoped) ---

func (s *Store) ListHumans(ctx context.Context, orgID uuid.UUID) ([]Human, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, email, display_name, role, manager_id,
			department_id, job_title, identities, phone, responsibilities, custom, created_at
		FROM humans WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHumans(rows)
}

func (s *Store) GetHuman(ctx context.Context, orgID, id uuid.UUID) (Human, error) {
	var h Human
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, email, display_name, role, manager_id,
			department_id, job_title, identities, phone, responsibilities, custom, created_at
		FROM humans WHERE id=$1 AND org_id=$2`, id, orgID).
		Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID,
			&h.DepartmentID, &h.JobTitle, &h.Identities, &h.Phone, &h.Responsibilities, &h.Custom, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Human{}, ErrNotFound
	}
	return h, err
}

func (s *Store) CreateHuman(ctx context.Context, orgID uuid.UUID, email, displayName, role, passwordHash string, profile Profile) (Human, error) {
	profile.Identities = NormalizeIdentities(profile.Identities)
	profile.Custom = NormalizeCustom(profile.Custom)
	h := Human{ID: uuid.New(), OrgID: orgID, Email: email, DisplayName: displayName, Role: role, Profile: profile}
	err := s.pool.QueryRow(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role,
			job_title, identities, phone, responsibilities, custom)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at`,
		h.ID, orgID, email, displayName, passwordHash, role,
		profile.JobTitle, profile.Identities, profile.Phone, profile.Responsibilities, profile.Custom).Scan(&h.CreatedAt)
	if isUniqueViolation(err) {
		return Human{}, ErrEmailTaken
	}
	return h, err
}

// UpdateHuman changes name, role and/or password. On a password change all of
// the user's sessions are revoked. Runs in a transaction so that the last-admin
// check does not race.
func (s *Store) UpdateHuman(ctx context.Context, orgID, id uuid.UUID, upd HumanUpdate) (Human, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Human{}, err
	}
	defer tx.Rollback(ctx)

	var h Human
	err = tx.QueryRow(ctx, `SELECT id, org_id, email, display_name, role, manager_id,
			department_id, job_title, identities, phone, responsibilities, custom, created_at
		FROM humans WHERE id=$1 AND org_id=$2 FOR UPDATE`, id, orgID).
		Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID,
			&h.DepartmentID, &h.JobTitle, &h.Identities, &h.Phone, &h.Responsibilities, &h.Custom, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Human{}, ErrNotFound
	}
	if err != nil {
		return Human{}, err
	}

	if upd.Role != nil && h.Role == "platform_admin" && *upd.Role != "platform_admin" {
		if err := ensureOtherAdmin(ctx, tx, orgID, id); err != nil {
			return Human{}, err
		}
	}
	if upd.DisplayName != nil {
		h.DisplayName = *upd.DisplayName
	}
	if upd.Role != nil {
		h.Role = *upd.Role
	}
	if upd.ManagerID != nil {
		if upd.ManagerID.Valid {
			if err := ensureNoManagerCycle(ctx, tx, orgID, id, upd.ManagerID.UUID); err != nil {
				return Human{}, err
			}
			h.ManagerID = &upd.ManagerID.UUID
		} else {
			h.ManagerID = nil
		}
	}
	if upd.JobTitle != nil {
		h.JobTitle = *upd.JobTitle
	}
	if upd.Identities != nil {
		h.Identities = NormalizeIdentities(upd.Identities)
	}
	if upd.Phone != nil {
		h.Phone = *upd.Phone
	}
	if upd.Responsibilities != nil {
		h.Responsibilities = *upd.Responsibilities
	}
	if upd.Custom != nil {
		h.Custom = NormalizeCustom(upd.Custom)
	}
	if h.Identities == nil {
		h.Identities = map[string]string{}
	}
	if h.Custom == nil {
		h.Custom = map[string]string{}
	}
	if _, err := tx.Exec(ctx, `UPDATE humans SET display_name=$1, role=$2, manager_id=$3,
			job_title=$4, identities=$5, phone=$6, responsibilities=$7, custom=$8 WHERE id=$9`,
		h.DisplayName, h.Role, h.ManagerID,
		h.JobTitle, h.Identities, h.Phone, h.Responsibilities, h.Custom, id); err != nil {
		return Human{}, err
	}
	if upd.PasswordHash != nil {
		if _, err := tx.Exec(ctx, `UPDATE humans SET password_hash=$1 WHERE id=$2`, *upd.PasswordHash, id); err != nil {
			return Human{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM http_sessions WHERE human_id=$1`, id); err != nil {
			return Human{}, err
		}
	}
	return h, tx.Commit(ctx)
}

func (s *Store) DeleteHuman(ctx context.Context, orgID, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var role string
	err = tx.QueryRow(ctx, `SELECT role FROM humans WHERE id=$1 AND org_id=$2 FOR UPDATE`, id, orgID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if role == "platform_admin" {
		if err := ensureOtherAdmin(ctx, tx, orgID, id); err != nil {
			return err
		}
	}
	// Since migration 0025 agents.supervisor_id no longer carries a DB foreign
	// key on humans — detach agents that reported to this human here (formerly
	// via ON DELETE SET NULL).
	if _, err := tx.Exec(ctx, `UPDATE agents SET supervisor_id=NULL WHERE supervisor_id=$1 AND org_id=$2`, id, orgID); err != nil {
		return err
	}
	// Sessions go away with it via ON DELETE CASCADE.
	if _, err := tx.Exec(ctx, `DELETE FROM humans WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ensureNoManagerCycle checks that the new manager belongs to the organization
// and that the chain upwards from them does not lead back to id. Runs inside
// the update transaction so that the check does not race.
func ensureNoManagerCycle(ctx context.Context, tx pgx.Tx, orgID, id, managerID uuid.UUID) error {
	if managerID == id {
		return ErrManagerCycle
	}
	cur := managerID
	for {
		var next *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT manager_id FROM humans WHERE id=$1 AND org_id=$2`, cur, orgID).Scan(&next)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound // manager does not exist (in this organization)
		}
		if err != nil {
			return err
		}
		if next == nil {
			return nil
		}
		if *next == id {
			return ErrManagerCycle
		}
		cur = *next
	}
}

func ensureOtherAdmin(ctx context.Context, tx pgx.Tx, orgID, exceptID uuid.UUID) error {
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM humans
		WHERE org_id=$1 AND role='platform_admin' AND id<>$2`, orgID, exceptID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// --- Organizations (tenants) ---

// ListOrgs returns all tenants of the installation. In the MVP platform_admin
// is at the same time the operator role of the deployment instance — a
// dedicated super-admin level only follows with the OIDC build-out.
func (s *Store) ListOrgs(ctx context.Context) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.name, o.description, o.fleet_killed, o.created_at,
			(SELECT count(*) FROM humans h WHERE h.org_id=o.id),
			(SELECT count(*) FROM agents a WHERE a.org_id=o.id)
		FROM organizations o ORDER BY o.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Organization
	for rows.Next() {
		var o Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Description, &o.FleetKilled, &o.CreatedAt, &o.HumanCount, &o.AgentCount); err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

// CreateOrg creates a tenant together with an initial platform_admin — an
// organization without an admin would be unreachable.
func (s *Store) CreateOrg(ctx context.Context, name, adminEmail, adminName, adminPasswordHash string) (Organization, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Organization{}, err
	}
	defer tx.Rollback(ctx)

	o := Organization{ID: uuid.New(), Name: name, HumanCount: 1}
	if err := tx.QueryRow(ctx, `INSERT INTO organizations (id, name) VALUES ($1,$2) RETURNING created_at`,
		o.ID, name).Scan(&o.CreatedAt); err != nil {
		return Organization{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,$4,$5,'platform_admin')`,
		uuid.New(), o.ID, adminEmail, adminName, adminPasswordHash)
	if isUniqueViolation(err) {
		return Organization{}, ErrEmailTaken
	}
	if err != nil {
		return Organization{}, err
	}
	// Seed the base allowlist: the runtime's LLM endpoint must be reachable,
	// otherwise no agent can work. Changeable through the egress UI.
	if _, err := tx.Exec(ctx, `INSERT INTO egress_default_hosts (org_id, pattern, note)
		VALUES ($1, 'api.anthropic.com', 'LLM endpoint of the Claude runtime')`, o.ID); err != nil {
		return Organization{}, err
	}
	return o, tx.Commit(ctx)
}

func (s *Store) RenameOrg(ctx context.Context, id uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET name=$1 WHERE id=$2`, name, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// GetOrg reads one organisation's master data (without the counts — whoever
// wants those asks ListOrgs).
func (s *Store) GetOrg(ctx context.Context, id uuid.UUID) (Organization, error) {
	var o Organization
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, description, platform_repo_system, platform_repo_project,
			fleet_killed, created_at FROM organizations WHERE id=$1`, id).
		Scan(&o.ID, &o.Name, &o.Description, &o.PlatformRepoSystem, &o.PlatformRepoProject,
			&o.FleetKilled, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// SetOrgDescription stores what this organisation does (spec/20). Empty is
// allowed — the description is an offer, not an obligation; without it the
// platform simply asks less well-informed questions.
func (s *Store) SetOrgDescription(ctx context.Context, id uuid.UUID, description string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET description=$1 WHERE id=$2`, description, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteOrg removes a tenant together with everything attached to it
// (agents, users, backlog, secrets — via ON DELETE CASCADE).
func (s *Store) DeleteOrg(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM organizations WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Helpers ---

func scanHumans(rows pgx.Rows) ([]Human, error) {
	var list []Human
	for rows.Next() {
		var h Human
		if err := rows.Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID,
			&h.DepartmentID, &h.JobTitle, &h.Identities, &h.Phone, &h.Responsibilities, &h.Custom, &h.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, h)
	}
	return list, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// SetPlatformRepo stores where this platform's own source lives (spec/21).
// Both empty switches the whole thing off again — the prompt section then
// disappears with it, and nobody reads about a repository nobody connected.
func (s *Store) SetPlatformRepo(ctx context.Context, id uuid.UUID, system, project string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE organizations SET platform_repo_system=$1, platform_repo_project=$2 WHERE id=$3`,
		system, project, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
