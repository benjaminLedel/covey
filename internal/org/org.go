// Package org verwaltet die Mandanten (Organisationen) und die Menschen
// darin (RBAC, spec/09). Die Organisation ist die Einheit von Covey — dieser
// Store trägt die Admin-Seite: Nutzer anlegen/ändern/entfernen, Tenants
// verwalten. Schutzregeln (letzter platform_admin bleibt) werden hier
// erzwungen, nicht in der UI.
package org

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound   = errors.New("nicht gefunden")
	ErrEmailTaken = errors.New("e-mail ist bereits vergeben")
	// ErrLastAdmin schützt vor Aussperrung: der letzte platform_admin einer
	// Organisation kann weder gelöscht noch herabgestuft werden.
	ErrLastAdmin = errors.New("der letzte platform_admin der Organisation kann nicht entfernt werden")
	// ErrManagerCycle hält den Org-Chart azyklisch: niemand kann (transitiv)
	// an sich selbst berichten.
	ErrManagerCycle = errors.New("vorgesetzten-beziehung würde einen zyklus bilden")
)

type Organization struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	FleetKilled bool      `json:"fleet_killed"`
	HumanCount  int       `json:"human_count"`
	AgentCount  int       `json:"agent_count"`
	CreatedAt   time.Time `json:"created_at"`
}

type Human struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	// ManagerID ist die Vorgesetzten-Beziehung im Org-Chart (spec/09);
	// nil = Wurzel (berichtet an niemanden).
	ManagerID *uuid.UUID `json:"manager_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// HumanUpdate ist ein partielles Update — nil-Felder bleiben unverändert.
// ManagerID unterscheidet drei Fälle: nil = unverändert, Valid=false = Zuordnung
// lösen, Valid=true = neuer Vorgesetzter.
type HumanUpdate struct {
	DisplayName  *string
	Role         *string
	PasswordHash *string
	ManagerID    *uuid.NullUUID
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// --- Menschen (org-gescopt) ---

func (s *Store) ListHumans(ctx context.Context, orgID uuid.UUID) ([]Human, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, email, display_name, role, manager_id, created_at
		FROM humans WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanHumans(rows)
}

func (s *Store) GetHuman(ctx context.Context, orgID, id uuid.UUID) (Human, error) {
	var h Human
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, email, display_name, role, manager_id, created_at
		FROM humans WHERE id=$1 AND org_id=$2`, id, orgID).
		Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Human{}, ErrNotFound
	}
	return h, err
}

func (s *Store) CreateHuman(ctx context.Context, orgID uuid.UUID, email, displayName, role, passwordHash string) (Human, error) {
	h := Human{ID: uuid.New(), OrgID: orgID, Email: email, DisplayName: displayName, Role: role}
	err := s.pool.QueryRow(ctx, `INSERT INTO humans (id, org_id, email, display_name, password_hash, role)
		VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		h.ID, orgID, email, displayName, passwordHash, role).Scan(&h.CreatedAt)
	if isUniqueViolation(err) {
		return Human{}, ErrEmailTaken
	}
	return h, err
}

// UpdateHuman ändert Name, Rolle und/oder Passwort. Bei einem Passwortwechsel
// werden alle Sessions des Nutzers widerrufen. Läuft in einer Transaktion,
// damit die Last-Admin-Prüfung nicht racet.
func (s *Store) UpdateHuman(ctx context.Context, orgID, id uuid.UUID, upd HumanUpdate) (Human, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Human{}, err
	}
	defer tx.Rollback(ctx)

	var h Human
	err = tx.QueryRow(ctx, `SELECT id, org_id, email, display_name, role, manager_id, created_at
		FROM humans WHERE id=$1 AND org_id=$2 FOR UPDATE`, id, orgID).
		Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID, &h.CreatedAt)
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
	if _, err := tx.Exec(ctx, `UPDATE humans SET display_name=$1, role=$2, manager_id=$3 WHERE id=$4`,
		h.DisplayName, h.Role, h.ManagerID, id); err != nil {
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
	// Sessions fallen per ON DELETE CASCADE mit weg.
	if _, err := tx.Exec(ctx, `DELETE FROM humans WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// ensureNoManagerCycle prüft, dass der neue Vorgesetzte zur Organisation gehört
// und die Kette von ihm aufwärts nicht zurück zu id führt. Läuft in der
// Update-Transaktion, damit die Prüfung nicht racet.
func ensureNoManagerCycle(ctx context.Context, tx pgx.Tx, orgID, id, managerID uuid.UUID) error {
	if managerID == id {
		return ErrManagerCycle
	}
	cur := managerID
	for {
		var next *uuid.UUID
		err := tx.QueryRow(ctx, `SELECT manager_id FROM humans WHERE id=$1 AND org_id=$2`, cur, orgID).Scan(&next)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound // Vorgesetzter existiert nicht (in dieser Organisation)
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

// --- Organisationen (Tenants) ---

// ListOrgs liefert alle Mandanten der Installation. Im MVP ist platform_admin
// zugleich Betreiber-Rolle der Deployment-Instanz — eine eigene
// Super-Admin-Ebene folgt erst mit dem OIDC-Ausbau.
func (s *Store) ListOrgs(ctx context.Context) ([]Organization, error) {
	rows, err := s.pool.Query(ctx, `SELECT o.id, o.name, o.fleet_killed, o.created_at,
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
		if err := rows.Scan(&o.ID, &o.Name, &o.FleetKilled, &o.CreatedAt, &o.HumanCount, &o.AgentCount); err != nil {
			return nil, err
		}
		list = append(list, o)
	}
	return list, rows.Err()
}

// CreateOrg legt einen Mandanten samt initialem platform_admin an — eine
// Organisation ohne Admin wäre unerreichbar.
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
	// Basis-Allowlist seeden: der LLM-Endpunkt der Runtime muss erreichbar
	// sein, sonst kann kein Agent arbeiten. Über die Egress-UI änderbar.
	if _, err := tx.Exec(ctx, `INSERT INTO egress_default_hosts (org_id, pattern, note)
		VALUES ($1, 'api.anthropic.com', 'LLM-Endpunkt der Claude-Runtime')`, o.ID); err != nil {
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

// DeleteOrg entfernt einen Mandanten mitsamt allem, was an ihm hängt
// (Agenten, Nutzer, Backlog, Secrets — per ON DELETE CASCADE).
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

// --- Hilfen ---

func scanHumans(rows pgx.Rows) ([]Human, error) {
	var list []Human
	for rows.Next() {
		var h Human
		if err := rows.Scan(&h.ID, &h.OrgID, &h.Email, &h.DisplayName, &h.Role, &h.ManagerID, &h.CreatedAt); err != nil {
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
