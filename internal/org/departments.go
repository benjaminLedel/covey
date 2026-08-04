package org

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrDeptNotFound = errors.New("department not found")

// DeptLead is a member of a department's leadership — a human or an agent.
type DeptLead struct {
	Kind string    `json:"kind"` // "human" | "agent"
	ID   uuid.UUID `json:"id"`
}

type Department struct {
	ID          uuid.UUID  `json:"id"`
	OrgID       uuid.UUID  `json:"org_id"`
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Color       string     `json:"color"` // hex accent color, empty = default
	Leads       []DeptLead `json:"leads"`
	CreatedAt   time.Time  `json:"created_at"`
}

func (s *Store) ListDepartments(ctx context.Context, orgID uuid.UUID) ([]Department, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, name, description, color, created_at FROM departments WHERE org_id=$1 ORDER BY name`,
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Department
	for rows.Next() {
		var d Department
		if err := rows.Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.Color, &d.CreatedAt); err != nil {
			return nil, err
		}
		d.Leads = []DeptLead{}
		list = append(list, d)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return list, nil
	}

	byID := make(map[uuid.UUID]*Department, len(list))
	for i := range list {
		byID[list[i].ID] = &list[i]
	}
	leadRows, err := s.pool.Query(ctx,
		`SELECT dl.department_id, dl.human_id, dl.agent_id
		 FROM department_leads dl JOIN departments d ON d.id = dl.department_id
		 WHERE d.org_id=$1 ORDER BY dl.created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer leadRows.Close()
	for leadRows.Next() {
		var deptID uuid.UUID
		var humanID, agentID *uuid.UUID
		if err := leadRows.Scan(&deptID, &humanID, &agentID); err != nil {
			return nil, err
		}
		d := byID[deptID]
		if d == nil {
			continue
		}
		if humanID != nil {
			d.Leads = append(d.Leads, DeptLead{Kind: "human", ID: *humanID})
		} else if agentID != nil {
			d.Leads = append(d.Leads, DeptLead{Kind: "agent", ID: *agentID})
		}
	}
	return list, leadRows.Err()
}

func (s *Store) GetDepartment(ctx context.Context, orgID, id uuid.UUID) (Department, error) {
	var d Department
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, name, description, color, created_at FROM departments WHERE id=$1 AND org_id=$2`,
		id, orgID).Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.Color, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Department{}, ErrDeptNotFound
	}
	return d, err
}

func (s *Store) CreateDepartment(ctx context.Context, orgID uuid.UUID, name, description, color string) (Department, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Department{}, errors.New("name is required")
	}
	d := Department{OrgID: orgID, Name: name, Description: strings.TrimSpace(description), Color: color}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO departments (org_id, name, description, color) VALUES ($1,$2,$3,$4) RETURNING id, created_at`,
		orgID, d.Name, d.Description, d.Color).Scan(&d.ID, &d.CreatedAt)
	return d, err
}

// SetDepartmentColor sets the accent color; empty restores the default.
func (s *Store) SetDepartmentColor(ctx context.Context, orgID, id uuid.UUID, color string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE departments SET color=$1 WHERE id=$2 AND org_id=$3`, color, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDeptNotFound
	}
	return nil
}

func (s *Store) RenameDepartment(ctx context.Context, orgID, id uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name is required")
	}
	tag, err := s.pool.Exec(ctx,
		`UPDATE departments SET name=$1 WHERE id=$2 AND org_id=$3`, name, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDeptNotFound
	}
	return nil
}

func (s *Store) DeleteDepartment(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM departments WHERE id=$1 AND org_id=$2`, id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrDeptNotFound
	}
	return nil
}

// AddDepartmentLead adds a member (human or agent) to the leadership of a
// department. Idempotent — an existing leadership assignment simply stays in
// place. Department and member must belong to the organization.
func (s *Store) AddDepartmentLead(ctx context.Context, orgID, deptID uuid.UUID, kind string, memberID uuid.UUID) error {
	if _, err := s.GetDepartment(ctx, orgID, deptID); err != nil {
		return err
	}
	var col, table string
	switch kind {
	case "human":
		col, table = "human_id", "humans"
	case "agent":
		col, table = "agent_id", "agents"
	default:
		return errors.New("kind must be human or agent")
	}
	tag, err := s.pool.Exec(ctx,
		`INSERT INTO department_leads (department_id, `+col+`)
		 SELECT $1, id FROM `+table+` WHERE id=$2 AND org_id=$3
		 ON CONFLICT DO NOTHING`, deptID, memberID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		// Either the member does not exist (in this org) — or it already is
		// leadership. The latter is not an error.
		var n int
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM `+table+` WHERE id=$1 AND org_id=$2`, memberID, orgID).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			return ErrNotFound
		}
	}
	return nil
}

// RemoveDepartmentLead removes a member from a department's leadership.
func (s *Store) RemoveDepartmentLead(ctx context.Context, orgID, deptID, memberID uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM department_leads dl USING departments d
		 WHERE d.id = dl.department_id AND d.org_id=$1 AND dl.department_id=$2
		   AND (dl.human_id=$3 OR dl.agent_id=$3)`, orgID, deptID, memberID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// SetHumanDepartment assigns a human to a department; nil detaches the assignment.
func (s *Store) SetHumanDepartment(ctx context.Context, orgID, humanID uuid.UUID, deptID *uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE humans SET department_id=$1 WHERE id=$2 AND org_id=$3`, deptID, humanID, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
