package chat

import (
	"context"

	"github.com/google/uuid"
)

// Reach is how far a member of the organisation writes to agents directly
// (#440). Adding an agent to a group is not reach: that stays with the roles
// that may create a task by hand.
type Reach string

const (
	// ReachOrg: any agent of the organisation. The default.
	ReachOrg Reach = "org"
	// ReachDepartment: the agents of the person's own departments — the one
	// they belong to and the ones they lead. Without one, a person reaches no
	// agent directly, except one they supervise.
	ReachDepartment Reach = "department"
)

// ReachOf reads the organisation's setting; anything unreadable is the
// narrow one, since a setting nobody can read must not widen who writes.
func (s *Store) ReachOf(ctx context.Context, orgID uuid.UUID) (Reach, error) {
	var v string
	if err := s.pool.QueryRow(ctx, `SELECT chat_reach FROM organizations WHERE id = $1`, orgID).Scan(&v); err != nil {
		return ReachDepartment, err
	}
	if Reach(v) == ReachOrg {
		return ReachOrg, nil
	}
	return ReachDepartment, nil
}

// SetReach switches it.
func (s *Store) SetReach(ctx context.Context, orgID uuid.UUID, r Reach) error {
	if r != ReachDepartment {
		r = ReachOrg
	}
	_, err := s.pool.Exec(ctx, `UPDATE organizations SET chat_reach = $2 WHERE id = $1`, orgID, string(r))
	return err
}

// Reachable are the agents of the organisation the person may write to
// directly under the organisation's setting: all of them for 'org' (and for
// the org admin, whom the caller passes as admin), otherwise those of the
// person's departments and those the person supervises.
func (s *Store) Reachable(ctx context.Context, orgID, humanID uuid.UUID, admin bool) (map[uuid.UUID]bool, error) {
	return s.reachable(ctx, orgID, humanID, admin, nil)
}

func (s *Store) reachable(ctx context.Context, orgID, humanID uuid.UUID, admin bool, nur *uuid.UUID) (map[uuid.UUID]bool, error) {
	reach, err := s.ReachOf(ctx, orgID)
	if err != nil {
		return nil, err
	}
	breit := admin || reach == ReachOrg
	rows, err := s.pool.Query(ctx, `SELECT a.id FROM agents a
		 WHERE a.org_id = $1 AND ($4::uuid IS NULL OR a.id = $4)
		   AND ($3 OR a.supervisor_id = $2
		        OR a.department_id IN (SELECT department_id FROM humans WHERE id = $2 AND department_id IS NOT NULL
		                               UNION SELECT department_id FROM department_leads WHERE human_id = $2))`,
		orgID, humanID, breit, nur)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// Reaches says whether the person may write to this one agent directly.
func (s *Store) Reaches(ctx context.Context, orgID, humanID, agentID uuid.UUID, admin bool) (bool, error) {
	eins, err := s.reachable(ctx, orgID, humanID, admin, &agentID)
	return eins[agentID], err
}
