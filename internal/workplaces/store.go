// Package workplaces holds the workplaces an organisation brings along itself:
// an image of its own under a name of its own.
//
// The catalogue (internal/sandbox) says which workplaces the PROJECT publishes.
// This is the other half — the tighter sandbox, the image with the in-house
// certificate, the toolchain nobody else needs. Same shape from the outside: a
// name an agent carries, a description that says what it is for, and one place
// where the address behind it is decided.
package workplaces

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	// ErrNotFound: no such workplace in this organisation.
	ErrNotFound = errors.New("workplaces: not found")
	// ErrTaken: the name is already in use — by another workplace of this
	// organisation, or by a profile from the catalogue. The caller checks the
	// second case; this package does not know the catalogue.
	ErrTaken = errors.New("workplaces: name already taken")
	// ErrInUse: agents still work in it. Deleting would leave them pointing at
	// a name that resolves to nothing, and they would fail at the next wake
	// with an error that names an image nobody can find.
	ErrInUse = errors.New("workplaces: agents still work in it")
)

// Workplace is one entry.
type Workplace struct {
	ID          uuid.UUID `json:"id"`
	Name        string    `json:"name"`
	Label       string    `json:"label"`
	Description string    `json:"description"`
	Image       string    `json:"image"`
	CreatedAt   string    `json:"created_at"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// nameRule is the same shape a profile name has: lowercase, digits, hyphens.
// Not decoration — the name travels into `ACCESS.md`-adjacent config, into URLs
// and into log lines, and a workplace called "Mein Image (neu)" would be
// quoted differently in each of them.
var nameRule = regexp.MustCompile(`^[a-z][a-z0-9-]{1,30}$`)

// ValidName reports whether a name is usable, and says why if it is not.
func ValidName(name string) error {
	if !nameRule.MatchString(name) {
		return fmt.Errorf("name %q: lowercase letters, digits and hyphens, 2–31 characters, starting with a letter", name)
	}
	return nil
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Workplace, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, name, label, description, image, created_at
		   FROM org_workplaces WHERE org_id=$1 ORDER BY name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Workplace
	for rows.Next() {
		var w Workplace
		var created any
		if err := rows.Scan(&w.ID, &w.Name, &w.Label, &w.Description, &w.Image, &created); err != nil {
			return nil, err
		}
		w.CreatedAt = fmt.Sprint(created)
		out = append(out, w)
	}
	return out, rows.Err()
}

// Images is the map the sandbox resolution needs: name → image.
func (s *Store) Images(ctx context.Context, orgID uuid.UUID) (map[string]string, error) {
	list, err := s.List(ctx, orgID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(list))
	for _, w := range list {
		out[w.Name] = w.Image
	}
	return out, nil
}

func (s *Store) Create(ctx context.Context, orgID, by uuid.UUID, w Workplace) (Workplace, error) {
	w.Name = strings.ToLower(strings.TrimSpace(w.Name))
	w.Image = strings.TrimSpace(w.Image)
	if err := ValidName(w.Name); err != nil {
		return Workplace{}, err
	}
	if w.Image == "" {
		return Workplace{}, errors.New("workplaces: no image reference")
	}
	if w.Label == "" {
		w.Label = w.Name
	}
	w.ID = uuid.New()
	var creator *uuid.UUID
	if by != uuid.Nil {
		creator = &by
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO org_workplaces (id, org_id, name, label, description, image, created_by)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		w.ID, orgID, w.Name, w.Label, w.Description, w.Image, creator)
	if err != nil {
		if strings.Contains(err.Error(), "idx_org_workplaces_name") {
			return Workplace{}, ErrTaken
		}
		return Workplace{}, err
	}
	return w, nil
}

// Delete removes a workplace — but not while agents work in it.
func (s *Store) Delete(ctx context.Context, orgID uuid.UUID, name string) error {
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM agents WHERE org_id=$1 AND sandbox_image=$2 AND NOT killed`,
		orgID, name).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("%w: %d", ErrInUse, n)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM org_workplaces WHERE org_id=$1 AND name=$2`, orgID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Get returns one workplace by name.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, name string) (Workplace, error) {
	var w Workplace
	var created any
	err := s.pool.QueryRow(ctx,
		`SELECT id, name, label, description, image, created_at
		   FROM org_workplaces WHERE org_id=$1 AND name=$2`, orgID, name).
		Scan(&w.ID, &w.Name, &w.Label, &w.Description, &w.Image, &created)
	if err == pgx.ErrNoRows {
		return Workplace{}, ErrNotFound
	}
	w.CreatedAt = fmt.Sprint(created)
	return w, err
}

// AllImages is the same map across every organisation — for the instance-wide
// readiness check, which asks about the host and not about a tenant.
//
// Two organisations may use the same name for different images. For the
// question this serves ("does this image exist on this host") that is
// harmless: both images are asked about, only the mapping name → image is
// ambiguous, and nothing here decides anything by name.
func AllImages(ctx context.Context, pool *pgxpool.Pool) (map[string]string, error) {
	rows, err := pool.Query(ctx, `SELECT name, image FROM org_workplaces`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var name, image string
		if err := rows.Scan(&name, &image); err != nil {
			return nil, err
		}
		out[name] = image
	}
	return out, rows.Err()
}
