package workplaces

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"covey/internal/sandbox"
)

// The images an organisation lets run beside its sandboxes.
//
// It lives in this package because it is the same kind of statement as the
// workplaces above: what an organisation brings along itself, decided once and
// read at every wake. The syntax and the matching sit in internal/sandbox,
// where the Service type is — this half is only the storage and the
// organisation's boundary around it.
//
// See spec/16 ("Services beside the sandbox") for why the list exists at all.

// ServicePattern is one entry of the allowlist.
type ServicePattern struct {
	ID        uuid.UUID `json:"id"`
	Pattern   string    `json:"pattern"`
	Note      string    `json:"note"`
	CreatedAt string    `json:"created_at"`
}

// ListServicePatterns returns the organisation's allowlist.
func (s *Store) ListServicePatterns(ctx context.Context, orgID uuid.UUID) ([]ServicePattern, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, pattern, note, created_at FROM service_image_allow
		  WHERE org_id=$1 ORDER BY pattern`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServicePattern
	for rows.Next() {
		var p ServicePattern
		var created any
		if err := rows.Scan(&p.ID, &p.Pattern, &p.Note, &created); err != nil {
			return nil, err
		}
		p.CreatedAt = fmt.Sprint(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// ServicePatterns is the plain list the matching needs. Separate from the one
// above because the enforcement runs at every wake and has no use for ids and
// timestamps.
func (s *Store) ServicePatterns(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT pattern FROM service_image_allow WHERE org_id=$1", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddServicePattern enters a pattern. The syntax is checked here rather than
// trusted from the caller — this list is the one thing standing between an
// image reference and a container on the runner.
func (s *Store) AddServicePattern(ctx context.Context, orgID uuid.UUID, pattern, note string) (ServicePattern, error) {
	clean, err := sandbox.ValidateImagePattern(pattern)
	if err != nil {
		return ServicePattern{}, err
	}
	var p ServicePattern
	var created any
	err = s.pool.QueryRow(ctx,
		`INSERT INTO service_image_allow (org_id, pattern, note) VALUES ($1,$2,$3)
		 ON CONFLICT (org_id, pattern) DO UPDATE SET note=EXCLUDED.note
		 RETURNING id, pattern, note, created_at`,
		orgID, clean, strings.TrimSpace(note)).Scan(&p.ID, &p.Pattern, &p.Note, &created)
	if err != nil {
		return ServicePattern{}, err
	}
	p.CreatedAt = fmt.Sprint(created)
	return p, nil
}

// DeleteServicePattern removes one, org-scoped.
//
// It does not check whether an agent still declares an image that only this
// pattern covered. That is deliberate: the check runs at the next wake, where
// it can name the agent, the service and the image — here it could only refuse
// with a list. Taking a pattern away is how one takes an image out of
// circulation, and it has to work while agents still name it.
func (s *Store) DeleteServicePattern(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM service_image_allow WHERE org_id=$1 AND id=$2", orgID, id)
	if err == nil && tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return err
}

// CheckServices reports which of these services an organisation does not allow.
// Empty result = all of them may run.
//
// The error names the remedy, because a refusal that only says no leaves
// whoever reads it to derive the pattern syntax from the documentation — and
// that is the part they are most likely to get subtly wrong.
func (s *Store) CheckServices(ctx context.Context, orgID uuid.UUID, services []sandbox.Service) error {
	if len(services) == 0 {
		return nil
	}
	patterns, err := s.ServicePatterns(ctx, orgID)
	if err != nil {
		return err
	}
	for _, svc := range services {
		if sandbox.ImageAllowed(patterns, svc.Image) {
			continue
		}
		return fmt.Errorf("the image %q of the service %q is not on this organisation's allowlist — add `%s` to let it run",
			svc.Image, svc.Name, sandbox.SuggestPattern(svc.Image))
	}
	return nil
}
