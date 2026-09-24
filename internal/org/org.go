// Package org manages the tenants (organizations) and the humans within them
// (RBAC, spec/09). The organization is covey's unit — this store carries the
// admin side: create/change/remove users, manage tenants. Protective rules (the
// last org_admin stays) are enforced here, not in the UI.
package org

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/identity"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrEmailTaken = errors.New("e-mail is already taken")
	// ErrLastAdmin guards against lockout: the last org_admin of an
	// organization can neither be deleted nor demoted.
	ErrLastAdmin = errors.New("the last org_admin of the organization cannot be removed")
	// ErrManagerCycle keeps the org chart acyclic: nobody can (transitively)
	// report to themselves.
	ErrManagerCycle = errors.New("manager relation would form a cycle")
	// ErrAlreadyMember: an account holds at most one seat per organization
	// (humans_account_per_org, migration 0059).
	ErrAlreadyMember = errors.New("the account already has a seat in this organization")
)

type Organization struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
	// Description: what this organisation does, in a few sentences. Master data,
	// not a setup prompt — it goes into the config of newly drafted agents, into
	// every hiring brief and into the config copilot's system prompt (spec/20).
	Description string `json:"description"`
	// PlatformRepo names where this platform's own source lives — the target
	// system and the project on it (spec/21). covey Doctor reads
	// the code there and files its issues there; both from the same address,
	// because reporting against code you have not read produces symptoms.
	// Empty = not set up, and then nothing about it stands in any prompt.
	PlatformRepoSystem  string `json:"platform_repo_system"`
	PlatformRepoProject string `json:"platform_repo_project"`
	// OfficeFurnishing says how densely the office is furnished — "sparse",
	// "normal" or "rich" (#325). A property of the building, and the building
	// belongs to the organisation: set once by an admin, seen by everyone.
	OfficeFurnishing string    `json:"office_furnishing"`
	FleetKilled      bool      `json:"fleet_killed"`
	HumanCount       int       `json:"human_count"`
	AgentCount       int       `json:"agent_count"`
	CreatedAt        time.Time `json:"created_at"`
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
	// LastSeenAt is when a request last arrived on this seat (#315). nil means
	// never seen — which is NOT the same as away, and no surface may conflate
	// the two: a seat nobody has used yet looks different from one that was
	// used this morning.
	//
	// A timestamp and not a flag, deliberately: "online" as a boolean has to be
	// cleared by something, and whatever fails to clear it leaves somebody
	// permanently present who went home on Friday. This says when the platform
	// last saw a person; what still counts as present is the reader's decision.
	LastSeenAt *time.Time `json:"last_seen_at,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
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

// --- Presence (#315) ---

// Anwesenheit is one seat and when the platform last saw it.
type Anwesenheit struct {
	HumanID    uuid.UUID `json:"id"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

// GesehenSchwelle: how old a sighting may be and still count as present. Five
// minutes is long enough to survive a coffee and short enough that a closed
// laptop stops claiming somebody is there.
const GesehenSchwelle = 5 * time.Minute

// Gesehen records that a request arrived on this seat.
//
// The condition in the statement is the whole point: the interface polls, so
// without it this would be several writes per second per person for a value
// whose resolution is minutes. With it, the statement runs every time and
// matches nothing almost every time — a primary-key lookup, and no write.
// The caller throttles on top of that so the statement is not even sent
// (httpapi.Server.gesehen).
func (s *Store) Gesehen(ctx context.Context, humanID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE humans SET last_seen_at = now()
		WHERE id = $1 AND (last_seen_at IS NULL OR last_seen_at < now() - interval '1 minute')`, humanID)
	return err
}

// Anwesende returns everybody in the organisation who has been seen at all,
// with the moment they were seen. Deliberately NOT a filtered list of "who is
// online": the threshold belongs to the reader, and a surface that wants to
// show "away since 14:02" needs the timestamp, not a verdict.
func (s *Store) Anwesende(ctx context.Context, orgID uuid.UUID) ([]Anwesenheit, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, last_seen_at FROM humans WHERE org_id=$1 AND last_seen_at IS NOT NULL`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Anwesenheit{}
	for rows.Next() {
		var a Anwesenheit
		if err := rows.Scan(&a.HumanID, &a.LastSeenAt); err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, rows.Err()
}

// --- Humans (org-scoped) ---

func (s *Store) ListHumans(ctx context.Context, orgID uuid.UUID) ([]Human, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, email, display_name, role, manager_id,
			department_id, job_title, identities, phone, responsibilities, custom, last_seen_at, created_at
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
			department_id, job_title, identities, phone, responsibilities, custom, last_seen_at, created_at
		FROM humans WHERE id=$1 AND org_id=$2`, id, orgID).
		Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID,
			&h.DepartmentID, &h.JobTitle, &h.Identities, &h.Phone, &h.Responsibilities, &h.Custom,
			&h.LastSeenAt, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Human{}, ErrNotFound
	}
	return h, err
}

// CreateHuman creates a seat — and the login that goes with it, if the person
// does not already have one.
//
// The second case is what makes multi-organisation membership work in
// practice: whoever an admin adds by an address that already has an account
// gets a SEAT in this organisation, not a second login. Their existing
// password stays valid and the one supplied here is ignored — an admin of
// organisation B must not be able to set the password of somebody who works in
// organisation A. That is why the password is not an update but only a
// fallback for a login that does not exist yet.
func (s *Store) CreateHuman(ctx context.Context, orgID uuid.UUID, email, displayName, role, passwordHash string, profile Profile) (Human, error) {
	profile.Identities = NormalizeIdentities(profile.Identities)
	profile.Custom = NormalizeCustom(profile.Custom)
	email = strings.ToLower(strings.TrimSpace(email))
	h := Human{ID: uuid.New(), OrgID: orgID, Email: email, DisplayName: displayName, Role: role, Profile: profile}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Human{}, err
	}
	defer tx.Rollback(ctx)

	var accountID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM accounts WHERE email=$1`, email).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		// The address counts as confirmed: it comes from an administrator, not
		// from a self-registration nobody has checked.
		accountID = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
			VALUES ($1,$2,$3,$4,now())`, accountID, email, passwordHash, displayName); err != nil {
			return Human{}, err
		}
	} else if err != nil {
		return Human{}, err
	}

	err = tx.QueryRow(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role,
			job_title, identities, phone, responsibilities, custom)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING created_at`,
		h.ID, orgID, accountID, email, displayName, passwordHash, role,
		profile.JobTitle, profile.Identities, profile.Phone, profile.Responsibilities, profile.Custom).Scan(&h.CreatedAt)
	if isUniqueViolation(err) {
		return Human{}, ErrEmailTaken
	}
	if err != nil {
		return Human{}, err
	}
	return h, tx.Commit(ctx)
}

// AddMember gives an existing account a seat in an organisation — how the
// instance administration puts one person into a second organisation (#262).
//
// Unlike CreateHuman it never creates a login: the account exists, and neither
// its password nor its address is anything to decide here. Name and address
// are copied from the account; the profile starts empty and is the new
// organisation's to fill in. An unknown account or organisation is ErrNotFound.
func (s *Store) AddMember(ctx context.Context, orgID, accountID uuid.UUID, role string) (Human, error) {
	h := Human{ID: uuid.New(), OrgID: orgID, Role: role,
		Profile: Profile{Identities: map[string]string{}, Custom: map[string]string{}}}
	err := s.pool.QueryRow(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		SELECT $1, o.id, a.id, a.email, COALESCE(NULLIF(a.display_name, ''), a.email), a.password_hash, $4
		FROM accounts a, organizations o WHERE o.id=$2 AND a.id=$3
		RETURNING email, display_name, created_at`, h.ID, orgID, accountID, role).
		Scan(&h.Email, &h.DisplayName, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Human{}, ErrNotFound
	}
	if isUniqueViolation(err) {
		return Human{}, ErrAlreadyMember
	}
	return h, err
}

// SeatOf returns the id of the seat an account holds in an organisation.
func (s *Store) SeatOf(ctx context.Context, orgID, accountID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM humans WHERE org_id=$1 AND account_id=$2`, orgID, accountID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
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

	if upd.Role != nil && h.Role == identity.RoleOrgAdmin && *upd.Role != identity.RoleOrgAdmin {
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
		// The password sits on the account, not on the seat — it is one
		// password for one person, no matter how many organisations they work
		// in. And for the same reason every session of that ACCOUNT ends, not
		// just those of this seat: whoever changes their password wants to be
		// signed out everywhere, including the browser they are worried about.
		var accountID uuid.UUID
		if err := tx.QueryRow(ctx, `SELECT account_id FROM humans WHERE id=$1`, id).Scan(&accountID); err != nil {
			return Human{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE accounts SET password_hash=$1 WHERE id=$2`, *upd.PasswordHash, accountID); err != nil {
			return Human{}, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM http_sessions WHERE account_id=$1`, accountID); err != nil {
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
	if role == identity.RoleOrgAdmin {
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
	// A session working from this seat moves to the account's next seat, or to
	// none. The foreign key would delete it (ON DELETE CASCADE): with one seat
	// per account that was the same as signing out, with several it signs
	// somebody out of every organisation because they left one (#262). Without
	// a seat left the session stays valid and lands where a sign-in would, on
	// the "no organisation" page. API keys are bound to the seat and do go.
	if _, err := tx.Exec(ctx, `UPDATE http_sessions s SET human_id = (
			SELECT h.id FROM humans h WHERE h.account_id = s.account_id AND h.id <> $1
			ORDER BY h.created_at LIMIT 1)
		WHERE s.human_id = $1`, id); err != nil {
		return err
	}
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
		WHERE org_id=$1 AND role='org_admin' AND id<>$2`, orgID, exceptID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// --- Organizations (tenants) ---

// ListOrgs returns all tenants of the installation. In the MVP org_admin
// is at the same time the operator role of the deployment instance — a
// dedicated super-admin level only follows with the OIDC build-out.
func (s *Store) ListOrgs(ctx context.Context) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.name, o.description, o.office_furnishing, o.fleet_killed, o.created_at,
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
		if err := rows.Scan(&o.ID, &o.Name, &o.Description, &o.OfficeFurnishing, &o.FleetKilled, &o.CreatedAt, &o.HumanCount, &o.AgentCount); err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

// CreateOrg creates a tenant together with an initial org_admin — an
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
	// The admin's login: an existing account keeps its password (the person
	// already works somewhere and simply gains a seat), otherwise one is
	// created — see CreateHuman for why the supplied password is only ever a
	// fallback and never an update.
	adminEmail = strings.ToLower(strings.TrimSpace(adminEmail))
	var accountID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM accounts WHERE email=$1`, adminEmail).Scan(&accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		accountID = uuid.New()
		if _, err := tx.Exec(ctx, `INSERT INTO accounts (id, email, password_hash, display_name, email_verified_at)
			VALUES ($1,$2,$3,$4,now())`, accountID, adminEmail, adminPasswordHash, adminName); err != nil {
			return Organization{}, err
		}
	} else if err != nil {
		return Organization{}, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO humans (id, org_id, account_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,$4,$5,$6,'org_admin')`,
		uuid.New(), o.ID, accountID, adminEmail, adminName, adminPasswordHash)
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
			office_furnishing, fleet_killed, created_at FROM organizations WHERE id=$1`, id).
		Scan(&o.ID, &o.Name, &o.Description, &o.PlatformRepoSystem, &o.PlatformRepoProject,
			&o.OfficeFurnishing, &o.FleetKilled, &o.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return o, ErrNotFound
	}
	return o, err
}

// OfficeFurnishings are the densities the office knows. The names are the
// API's vocabulary, not the interface's: the web app translates them.
var OfficeFurnishings = []string{"sparse", "normal", "rich"}

// SetOrgOfficeFurnishing stores how densely the office is furnished (#325).
// Anything but the three known values is refused here, not in the handler:
// whoever writes the column writes one of three words.
func (s *Store) SetOrgOfficeFurnishing(ctx context.Context, id uuid.UUID, furnishing string) error {
	if !slices.Contains(OfficeFurnishings, furnishing) {
		return fmt.Errorf("office furnishing must be one of %v, got %q", OfficeFurnishings, furnishing)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE organizations SET office_furnishing=$1 WHERE id=$2`, furnishing, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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
			&h.DepartmentID, &h.JobTitle, &h.Identities, &h.Phone, &h.Responsibilities, &h.Custom,
			&h.LastSeenAt, &h.CreatedAt); err != nil {
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
