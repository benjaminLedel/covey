package org

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrDeptNotFound = errors.New("abteilung nicht gefunden")

type Department struct {
	ID          uuid.UUID `json:"id"`
	OrgID       uuid.UUID `json:"org_id"`
	Name        string    `json:"name"`
	Description string    `json:"description"`
	CreatedAt   time.Time `json:"created_at"`
}

func (s *Store) ListDepartments(ctx context.Context, orgID uuid.UUID) ([]Department, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, name, description, created_at FROM departments WHERE org_id=$1 ORDER BY name`,
		orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []Department
	for rows.Next() {
		var d Department
		if err := rows.Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, d)
	}
	return list, rows.Err()
}

func (s *Store) GetDepartment(ctx context.Context, orgID, id uuid.UUID) (Department, error) {
	var d Department
	err := s.pool.QueryRow(ctx,
		`SELECT id, org_id, name, description, created_at FROM departments WHERE id=$1 AND org_id=$2`,
		id, orgID).Scan(&d.ID, &d.OrgID, &d.Name, &d.Description, &d.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Department{}, ErrDeptNotFound
	}
	return d, err
}

func (s *Store) CreateDepartment(ctx context.Context, orgID uuid.UUID, name, description string) (Department, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Department{}, errors.New("name ist Pflicht")
	}
	d := Department{OrgID: orgID, Name: name, Description: strings.TrimSpace(description)}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO departments (org_id, name, description) VALUES ($1,$2,$3) RETURNING id, created_at`,
		orgID, d.Name, d.Description).Scan(&d.ID, &d.CreatedAt)
	return d, err
}

func (s *Store) RenameDepartment(ctx context.Context, orgID, id uuid.UUID, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("name ist Pflicht")
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

// SetHumanDepartment weist einen Menschen einer Abteilung zu; nil löst die Zuordnung.
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
