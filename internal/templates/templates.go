// Package templates manages the template library: stored agentBundle JSON
// snapshots from which new agents are instantiated.
package templates

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/examples"
)

var (
	ErrNotFound = errors.New("template not found")
	// ErrReadOnly: bundled templates are embedded into the binary and cannot be
	// deleted or overwritten from the org side.
	ErrReadOnly = errors.New("bundled template is read-only")
)

type Template struct {
	ID          uuid.UUID       `json:"id"`
	OrgID       uuid.UUID       `json:"org_id"`
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Bundle      json.RawMessage `json:"bundle"`
	CreatedBy   *uuid.UUID      `json:"created_by,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	// Builtin marks bundled templates (read-only, shared across orgs).
	Builtin bool `json:"builtin"`
}

// builtinTemplates projects the embedded example bundles onto Template, with
// name/description in the requested language (lang, e.g. "de"/"en").
func builtinTemplates(lang string) []Template {
	bs := examples.Builtins()
	out := make([]Template, 0, len(bs))
	for _, b := range bs {
		name, desc := b.Localized(lang)
		out = append(out, Template{
			ID:          b.ID,
			OrgID:       uuid.Nil,
			Name:        name,
			Description: desc,
			Bundle:      b.LocalizedBundle(lang),
			Builtin:     true,
		})
	}
	return out
}

// builtinByID looks up a bundled template by its fixed ID.
func builtinByID(id uuid.UUID, lang string) (Template, bool) {
	for _, t := range builtinTemplates(lang) {
		if t.ID == id {
			return t, true
		}
	}
	return Template{}, false
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

// List returns the bundled templates first (in language lang), then the
// org's own ones.
func (s *Store) List(ctx context.Context, orgID uuid.UUID, lang string) ([]Template, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT "+cols+" FROM agent_templates WHERE org_id=$1 ORDER BY name", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := builtinTemplates(lang)
	for rows.Next() {
		t, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID, lang string) (Template, error) {
	if t, ok := builtinByID(id, lang); ok {
		return t, nil
	}
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
	if _, ok := builtinByID(id, ""); ok {
		return ErrReadOnly
	}
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
