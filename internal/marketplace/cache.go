package marketplace

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Cache keeps the last good catalogue across restarts.
//
// The in-memory copy alone left two holes. A restart forgot the catalogue, so
// the first store page after it waited on a server somewhere on the internet —
// and if that server happened to be down, the page was EMPTY rather than
// stale, even though the instance had known the catalogue for weeks. Neither
// is a data loss, but both make an outage somewhere else look like a fault
// here.
//
// An interface rather than the pool directly: the client is used in tests
// without a database, and a cache is exactly the kind of thing that should be
// absent without consequence.
type Cache interface {
	Load(ctx context.Context, url string) ([]byte, time.Time, error)
	Save(ctx context.Context, url string, body []byte, at time.Time) error
}

// PgCache stores the catalogue in Postgres, keyed by its URL.
type PgCache struct{ Pool *pgxpool.Pool }

func NewPgCache(pool *pgxpool.Pool) *PgCache { return &PgCache{Pool: pool} }

func (c *PgCache) Load(ctx context.Context, url string) ([]byte, time.Time, error) {
	var body []byte
	var at time.Time
	err := c.Pool.QueryRow(ctx,
		`SELECT body, fetched_at FROM marketplace_cache WHERE url=$1`, url).Scan(&body, &at)
	if err == pgx.ErrNoRows {
		return nil, time.Time{}, nil // nothing cached yet is not a failure
	}
	return body, at, err
}

func (c *PgCache) Save(ctx context.Context, url string, body []byte, at time.Time) error {
	_, err := c.Pool.Exec(ctx,
		`INSERT INTO marketplace_cache (url, body, fetched_at) VALUES ($1,$2,$3)
		 ON CONFLICT (url) DO UPDATE SET body=$2, fetched_at=$3`, url, body, at)
	return err
}
