// Package templates verwaltet die Vorlagen-Bibliothek: gespeicherte
// agentBundle-JSON-Schnappschüsse, aus denen neue Agenten instanziert werden.
package templates

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("vorlage nicht gefunden")

type Template struct {
	ID          uuid.UUID       `json:"id"`
	OrgID       uuid.UUID       `json:"org_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Bundle      json.RawMessage `json:"bundle"`
	CreatedBy   *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

const cols = "id, org_id, name, description, bundle, created_by, created_at, updated_at"

func scan(row pgx.Row) (Template, error) {
	var t Template
	err := row.Scan(&t.ID, &t.OrgID, &t.Name, &t.Description, &t.Bundle, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Template, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+cols+" FROM agent_templates WHERE org_id=$1 ORDER BY name", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Template
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Template, error) {
	return scan(s.pool.QueryRow(ctx,
		"SELECT "+cols+" FROM agent_templates WHERE org_id=$1 AND id=$2", orgID, id))
}

func (s *Store) Create(ctx context.Context, orgID uuid.UUID, name, description string, bundle json.RawMessage, createdBy *uuid.UUID) (Template, error) {
	bundleJSON, err := json.Marshal(json.RawMessage(bundle))
	if err != nil {
		return Template{}, err
	}
	return scan(s.pool.QueryRow(ctx,
		`INSERT INTO agent_templates (org_id, name, description, bundle, created_by)
		 VALUES ($1,$2,$3,$4,$5)
		 ON CONFLICT (org_id, name) DO UPDATE
		   SET description=EXCLUDED.description, bundle=EXCLUDED.bundle, updated_at=now()
		 RETURNING `+cols,
		orgID, name, description, bundleJSON, createdBy))
}

func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM agent_templates WHERE org_id=$1 AND id=$2", orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}
