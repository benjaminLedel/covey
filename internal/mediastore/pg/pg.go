// Package pg is the builtin mediastore.Store: the bytes in a Postgres table
// (migration 0102). Nothing to run beside the database covey already has.
package pg

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/mediastore"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var _ mediastore.Store = (*Store)(nil)

func (s *Store) Put(ctx context.Context, orgID, owner uuid.UUID, contentType string, data []byte) (uuid.UUID, error) {
	id := uuid.New()
	_, err := s.pool.Exec(ctx, `INSERT INTO media (id, org_id, owner_id, content_type, size, data)
		VALUES ($1, $2, $3, $4, $5, $6)`, id, orgID, owner, contentType, len(data), data)
	return id, err
}

func (s *Store) Get(ctx context.Context, owner, id uuid.UUID) (mediastore.Medium, error) {
	var b mediastore.Medium
	err := s.pool.QueryRow(ctx, `SELECT content_type, data FROM media WHERE id=$1 AND owner_id=$2`, id, owner).
		Scan(&b.ContentType, &b.Data)
	if errors.Is(err, pgx.ErrNoRows) {
		return mediastore.Medium{}, mediastore.ErrNotFound
	}
	return b, err
}

func (s *Store) Delete(ctx context.Context, owner, id uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM media WHERE id=$1 AND owner_id=$2`, id, owner)
	return err
}
