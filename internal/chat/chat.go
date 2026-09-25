// Package chat holds the conversation: the messages of a thread, and the
// triage that decides what a message is.
//
// It sits beside the backlog and not inside it, because the two answer
// different questions. The backlog is the ledger — what is to be done, by
// whom, in which state. The conversation is the envelope — what was said. A
// message that turns into work points at its task; a message the agent simply
// answered points at nothing, and that is the whole reason this package
// exists (#302).
package chat

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Message is one line somebody said, human or agent.
type Message struct {
	ID        uuid.UUID  `json:"id"`
	OrgID     uuid.UUID  `json:"org_id"`
	AgentID   uuid.UUID  `json:"agent_id"`
	Author    string     `json:"author"`
	Text      string     `json:"text"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// TriageState: '' | pending | done | failed. Der Verlauf zeigt damit, dass
	// eine Nachricht angenommen, aber noch nicht entschieden ist.
	TriageState string `json:"triage_state,omitempty"`
}

type Store struct{ pool *pgxpool.Pool }

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Add writes one message. The task comes later or never — a message that
// becomes work is linked with LinkTask once the task exists, because the task
// needs a title the triage has not produced yet when the message arrives.
//
// `pending` says that a triage still owes this message a decision. It is what
// makes an accepted message survive a restart: the state is a row, not a
// goroutine.
func (s *Store) Add(ctx context.Context, orgID, agentID uuid.UUID, author, text string, pending bool) (Message, error) {
	m := Message{ID: uuid.New(), OrgID: orgID, AgentID: agentID, Author: author, Text: text}
	if pending {
		m.TriageState = StatePending
	}
	err := s.pool.QueryRow(ctx,
		`INSERT INTO chat_messages (id, org_id, agent_id, author, text, triage_state)
		 VALUES ($1,$2,$3,$4,$5,$6) RETURNING created_at`,
		m.ID, orgID, agentID, author, text, m.TriageState).Scan(&m.CreatedAt)
	return m, err
}

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

// Erledigt schreibt das Ergebnis fest.
func (s *Store) Erledigt(ctx context.Context, id uuid.UUID, zustand string) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chat_messages SET triage_state=$2 WHERE id=$1`, id, zustand)
	return err
}

/*
Liegengeblieben holt die Nachrichten, die beim letzten Mal keine

	Entscheidung mehr bekommen haben.

	Sie werden in derselben Bewegung auf 'done' gesetzt, unter FOR UPDATE SKIP
	LOCKED — dasselbe Muster wie im Backlog. Zwei Control Planes an derselben
	Datenbank holen sich damit nicht dieselbe Nachricht, und eine, die dabei
	stirbt, lässt keine Zeile ewig im Zugriff stehen. Dass der Zustand VOR der
	Arbeit gesetzt wird, ist Absicht: Eine Nachricht, an der die Triage zweimal
	scheitert, soll nicht bei jedem Neustart wieder einen Zug kosten.
*/
func (s *Store) Liegengeblieben(ctx context.Context, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`UPDATE chat_messages SET triage_state=$2
		 WHERE id IN (
		     SELECT id FROM chat_messages
		     WHERE triage_state=$1
		     ORDER BY created_at
		     FOR UPDATE SKIP LOCKED
		     LIMIT $3
		 )
		 RETURNING id, org_id, agent_id, author, text, task_id, created_at`,
		StatePending, StateDone, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.OrgID, &m.AgentID, &m.Author, &m.Text, &m.TaskID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// LinkTask records that this message became work.
func (s *Store) LinkTask(ctx context.Context, messageID, taskID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE chat_messages SET task_id=$2 WHERE id=$1`, messageID, taskID)
	return err
}

// Recent reads the newest messages of one agent, oldest first — the order a
// thread is read in.
func (s *Store) Recent(ctx context.Context, agentID uuid.UUID, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT id, org_id, agent_id, author, text, task_id, created_at FROM (
			SELECT id, org_id, agent_id, author, text, task_id, created_at
			FROM chat_messages WHERE agent_id=$1
			ORDER BY created_at DESC LIMIT $2
		 ) j ORDER BY created_at`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.OrgID, &m.AgentID, &m.Author, &m.Text, &m.TaskID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

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
