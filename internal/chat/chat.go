// Package chat holds the conversations — between people, between people and
// agents (#440) — and the triage that decides what a message to an agent is.
//
// It sits beside the backlog and not inside it, because the two answer
// different questions. The backlog is the ledger — what is to be done, by
// whom, in which state. The conversation is the envelope — what was said. A
// message that turns into work points at its task; a message the agent simply
// answered points at nothing (#302). Nothing of the backlog is mirrored into
// a conversation, except what a task opened there reports back, and a
// question a task from elsewhere parks on, which goes to the agent's
// supervisor.
package chat

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

/*
Der Zustand der Triage. Er steht an der Nachricht und nicht in einer

	eigenen Tabelle: Es ist eine Eigenschaft dieser einen Nachricht, und eine
	Warteschlange mit einer Zeile je Vorgang wäre dieselbe Zeile noch einmal.
*/
const (
	// StatePending: angenommen, noch nicht entschieden.
	StatePending = "pending"
	// StateDone: entschieden — geantwortet, notiert oder Aufgabe.
	StateDone = "done"
	// StateFailed: endgültig gescheitert. Die Nachricht wurde trotzdem zur
	// Aufgabe; der Zustand ist die Spur davon, nicht ihr Verlust.
	StateFailed = "failed"
)

// TriageMode is what an organisation has decided about its conversations.
type TriageMode string

const (
	// TriageOff: every message becomes a task. The behaviour before #302, and
	// the default — an installation that upgrades changes nothing.
	TriageOff TriageMode = "off"
	// TriageOn: the agent decides whether to answer or to open the work.
	TriageOn TriageMode = "on"
)

// Mode reads the organisation's setting. An unknown value counts as off: a
// setting nobody can read must not silently start spending money.
func (s *Store) Mode(ctx context.Context, orgID uuid.UUID) (TriageMode, error) {
	var v string
	if err := s.pool.QueryRow(ctx,
		`SELECT chat_triage FROM organizations WHERE id=$1`, orgID).Scan(&v); err != nil {
		return TriageOff, err
	}
	if TriageMode(v) == TriageOn {
		return TriageOn, nil
	}
	return TriageOff, nil
}

// SetMode switches it.
func (s *Store) SetMode(ctx context.Context, orgID uuid.UUID, mode TriageMode) error {
	if mode != TriageOn {
		mode = TriageOff
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE organizations SET chat_triage=$2 WHERE id=$1`, orgID, string(mode))
	return err
}

// TeamSurface says whether the organisation has turned the team surface on
// (#328). Off is the default while the surface is in beta; an organisation
// that cannot be read counts as off, the same rule as Mode.
func (s *Store) TeamSurface(ctx context.Context, orgID uuid.UUID) (bool, error) {
	var on bool
	if err := s.pool.QueryRow(ctx,
		`SELECT team_surface FROM organizations WHERE id=$1`, orgID).Scan(&on); err != nil {
		return false, err
	}
	return on, nil
}

// SetTeamSurface switches it.
func (s *Store) SetTeamSurface(ctx context.Context, orgID uuid.UUID, on bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE organizations SET team_surface=$2 WHERE id=$1`, orgID, on)
	return err
}
