// Package backlog implementiert das Backlog als First-Class-Postgres-Objekt:
// State, Priorität, Herkunft, Historie, Korrelations-Key (spec/03).
// Die Queue-Mechanik ist SELECT … FOR UPDATE SKIP LOCKED + LISTEN/NOTIFY.
package backlog

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	StateOpen       = "open"
	StateInProgress = "in_progress"
	StateBlocked    = "blocked"
	StateDone       = "done"
	StateFailed     = "failed"
	StateCancelled  = "cancelled"
)

// NotifyChannel ist der Postgres-Kanal für Wake-Events (Payload: agent_id).
const NotifyChannel = "covey_wake"

// validTransitions kodiert die Zustandsmaschine der Aufgabe.
var validTransitions = map[string][]string{
	StateOpen:       {StateInProgress, StateCancelled},
	StateInProgress: {StateBlocked, StateDone, StateFailed, StateOpen, StateCancelled},
	StateBlocked:    {StateOpen, StateCancelled}, // Wake-on-correlation öffnet neu (mit resume)
	StateFailed:     {StateOpen},                 // Retry: manuell erneut einplanen
	StateCancelled:  {StateOpen},                 // Wiederaufnahme einer verworfenen Aufgabe
}

func transitionAllowed(from, to string) bool {
	for _, t := range validTransitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

var (
	ErrNotFound          = errors.New("aufgabe nicht gefunden")
	ErrInvalidTransition = errors.New("unzulässiger Zustandsübergang")
)

type Task struct {
	ID               uuid.UUID `json:"id"`
	OrgID            uuid.UUID `json:"org_id"`
	AgentID          uuid.UUID `json:"agent_id"`
	Title            string    `json:"title"`
	Body             string    `json:"body"`
	State            string    `json:"state"`
	Priority         int       `json:"priority"`
	Origin           string    `json:"origin"`
	CorrelationKey   *string   `json:"correlation_key,omitempty"`
	RuntimeSessionID *string   `json:"runtime_session_id,omitempty"`
	ResumeInput      *string   `json:"resume_input,omitempty"`
	Result           *string    `json:"result,omitempty"`
	Error            *string    `json:"error,omitempty"`
	StageID          *uuid.UUID `json:"stage_id,omitempty"`
	ParentTaskID     *uuid.UUID `json:"parent_task_id,omitempty"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// Stage ist eine frei definierbare Kanban-Spalte eines Agenten (Overlay über
// den Lifecycle-State). Reihenfolge über position, Farbe optional (CSS-Token).
// CreatedBy unterscheidet die Herkunft: 'agent'-Spalten (per set_stage
// „erfunden") werden automatisch abgeräumt, sobald sie leer sind.
type Stage struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	Color     string    `json:"color"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Note ist eine proaktive Notiz an einer Aufgabe: Zwischenstände, Befunde,
// Versuchtes — aufgabenbezogen, im Gegensatz zum Gedächtnis (allgemeingültig).
type Note struct {
	ID        uuid.UUID `json:"id"`
	TaskID    uuid.UUID `json:"task_id"`
	Author    string    `json:"author"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

type Transition struct {
	FromState string    `json:"from_state"`
	ToState   string    `json:"to_state"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const taskCols = `id, org_id, agent_id, title, body, state, priority, origin,
	correlation_key, runtime_session_id, resume_input, result, error, stage_id, parent_task_id,
	archived_at, created_at, updated_at`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.OrgID, &t.AgentID, &t.Title, &t.Body, &t.State, &t.Priority, &t.Origin,
		&t.CorrelationKey, &t.RuntimeSessionID, &t.ResumeInput, &t.Result, &t.Error, &t.StageID, &t.ParentTaskID,
		&t.ArchivedAt, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Create legt eine Aufgabe an und feuert NOTIFY, damit der Dispatch-Loop aufwacht.
func (s *Store) Create(ctx context.Context, orgID, agentID uuid.UUID, title, body, origin string, priority int) (Task, error) {
	if priority == 0 {
		priority = 5
	}
	// Neue Aufgabe landet in der ersten Stage des Agenten (falls definiert).
	row := s.pool.QueryRow(ctx, `INSERT INTO backlog_tasks (id, org_id, agent_id, title, body, origin, priority, stage_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,
			(SELECT id FROM agent_stages WHERE agent_id=$3 ORDER BY position, created_at LIMIT 1))
		RETURNING `+taskCols,
		uuid.New(), orgID, agentID, title, body, origin, priority)
	t, err := scanTask(row)
	if err != nil {
		return t, err
	}
	s.notify(ctx, agentID)
	return t, nil
}

// ChildSpec beschreibt eine Aufgabe, die aus einer anderen hervorgeht: die
// Fortsetzung eines am Turn-Limit abgebrochenen Laufs oder eine vom Agenten
// selbst abgespaltene Teilaufgabe. AgentID leer = derselbe Agent wie die
// Ursprungsaufgabe (Delegation setzt sie auf einen Kollegen).
//
// SessionID/ResumeInput sind nur für die Fortsetzung gesetzt: mit ihnen nimmt
// die Runtime die abgebrochene Session wieder auf, statt bei null anzufangen.
type ChildSpec struct {
	AgentID     uuid.UUID
	Title       string
	Body        string
	Origin      string
	Priority    int
	SessionID   string
	ResumeInput string
}

// CreateChild legt eine Aufgabe an, die an einer Ursprungsaufgabe hängt.
// Organisation und (per Default) Agent erbt sie von ihr — eine Aufgabe kann
// also nie aus der Org ihrer Elternaufgabe herausfallen.
func (s *Store) CreateChild(ctx context.Context, parentID uuid.UUID, spec ChildSpec) (Task, error) {
	parent, err := s.Get(ctx, parentID)
	if err != nil {
		return Task{}, err
	}
	agentID := spec.AgentID
	if agentID == uuid.Nil {
		agentID = parent.AgentID
	}
	if spec.Priority == 0 {
		spec.Priority = 5
	}
	row := s.pool.QueryRow(ctx, `INSERT INTO backlog_tasks
		(id, org_id, agent_id, title, body, origin, priority, parent_task_id,
		 runtime_session_id, resume_input, stage_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8, NULLIF($9,''), NULLIF($10,''),
			(SELECT id FROM agent_stages WHERE agent_id=$3 ORDER BY position, created_at LIMIT 1))
		RETURNING `+taskCols,
		uuid.New(), parent.OrgID, agentID, spec.Title, spec.Body, spec.Origin, spec.Priority,
		parentID, spec.SessionID, spec.ResumeInput)
	t, err := scanTask(row)
	if err != nil {
		return t, err
	}
	s.notify(ctx, agentID)
	return t, nil
}

// AncestorsWithOrigin zählt, wie viele Vorfahren einer Aufgabe (inklusive ihrer
// selbst) eine Herkunft mit dem gegebenen Präfix tragen. Das trägt den
// Loop-Schutz: eine Fortsetzungs- oder Teilaufgaben-Kette darf nicht endlos
// wachsen, und der einzige verlässliche Zähler ist die Kette selbst.
func (s *Store) AncestorsWithOrigin(ctx context.Context, id uuid.UUID, originPrefix string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, parent_task_id, origin FROM backlog_tasks WHERE id=$1
			UNION ALL
			SELECT t.id, t.parent_task_id, t.origin
			FROM backlog_tasks t JOIN chain c ON t.id = c.parent_task_id
		)
		SELECT COUNT(*) FROM chain WHERE origin LIKE $2 || '%'`, id, originPrefix).Scan(&n)
	return n, err
}

// CountChildren zählt die direkt aus einer Aufgabe hervorgegangenen Aufgaben —
// die Breite der Zerlegung.
func (s *Store) CountChildren(ctx context.Context, parentID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM backlog_tasks WHERE parent_task_id=$1", parentID).Scan(&n)
	return n, err
}

// OpenWithTitle sagt, ob der Agent bereits eine nicht-terminale Aufgabe mit
// diesem Titel hat. Der Dublettenschutz für selbst angelegte Aufgaben: Ein
// Agent, der in jedem Lauf dieselbe Aufgabe neu anlegt, baut sich sonst eine
// Warteschlange, die nie leer wird.
func (s *Store) OpenWithTitle(ctx context.Context, agentID uuid.UUID, title string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM backlog_tasks
		WHERE agent_id=$1 AND title=$2 AND state NOT IN ('done','failed','cancelled'))`,
		agentID, title).Scan(&exists)
	return exists, err
}

func (s *Store) notify(ctx context.Context, agentID uuid.UUID) {
	// Wake-Signal ist best-effort — der periodische Tick fängt Verluste ab.
	_, _ = s.pool.Exec(ctx, "SELECT pg_notify($1,$2)", NotifyChannel, agentID.String())
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, "SELECT "+taskCols+" FROM backlog_tasks WHERE id=$1", id))
}

// ListByAgent liefert das aktive Backlog; includeArchived nimmt das Archiv dazu.
func (s *Store) ListByAgent(ctx context.Context, agentID uuid.UUID, includeArchived bool) ([]Task, error) {
	filter := " AND archived_at IS NULL"
	if includeArchived {
		filter = ""
	}
	rows, err := s.pool.Query(ctx, "SELECT "+taskCols+` FROM backlog_tasks WHERE agent_id=$1`+filter+`
		ORDER BY (state IN ('done','failed','cancelled')), priority, created_at`, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// ClaimNext holt die nächste offene Aufgabe des Agenten — seriell, mit
// SKIP LOCKED, damit konkurrierende Dispatcher sich nicht dieselbe greifen.
func (s *Store) ClaimNext(ctx context.Context, agentID uuid.UUID) (Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)

	t, err := scanTask(tx.QueryRow(ctx, "SELECT "+taskCols+` FROM backlog_tasks
		WHERE agent_id=$1 AND state='open'
		ORDER BY priority, created_at
		FOR UPDATE SKIP LOCKED LIMIT 1`, agentID))
	if err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec(ctx,
		"UPDATE backlog_tasks SET state='in_progress', updated_at=now() WHERE id=$1", t.ID); err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO task_transitions (task_id, from_state, to_state, note)
		VALUES ($1,'open','in_progress','dispatch')`, t.ID); err != nil {
		return Task{}, err
	}
	if err := syncStage(ctx, tx, t.ID, StateInProgress); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	t.State = StateInProgress
	return t, nil
}

// stateStage bildet den Lifecycle-State auf die Standard-Spalte ab, in der
// eine Aufgabe dann erwartet wird. Gilt nur, solange die Aufgabe in einer
// Standard-Spalte (oder keiner) liegt — eine bewusst gesetzte eigene Stage
// (Agent via set_stage, Mensch via Drag & Drop) wird nie überschrieben.
var stateStage = map[string]string{
	StateOpen:       "Backlog",
	StateInProgress: "In Arbeit",
	StateBlocked:    "In Arbeit",
	StateDone:       "Erledigt",
	StateFailed:     "Erledigt",
	StateCancelled:  "Erledigt",
}

// terminalState sagt, ob ein State das Ende der Aufgabe ist.
func terminalState(state string) bool {
	return state == StateDone || state == StateFailed || state == StateCancelled
}

// syncStage führt die Stage dem neuen State nach (innerhalb der Transaktion
// des Zustandsübergangs). Fehlt die Ziel-Spalte (umbenannt/gelöscht), passiert
// nichts — Auto-Follow ist Komfort, nie Zwang.
//
// Beim Übergang in einen terminalen State greift Auto-Follow zusätzlich für die
// vom Agenten selbst angelegten Spalten: Eine erledigte Aufgabe gehört nicht in
// „Recherche", sie gehört nach „Erledigt". Ohne das bliebe in jeder erfundenen
// Spalte eine terminale Aufgabe liegen, die Spalte damit dauerhaft „nicht leer"
// — und das Board sammelte über Wochen ein Dutzend toter Arbeitszustände an.
// Von Menschen angelegte Spalten bleiben auch hier unangetastet: bewusste
// Platzierung wird nie überschrieben.
func syncStage(ctx context.Context, tx pgx.Tx, taskID uuid.UUID, newState string) error {
	target, ok := stateStage[newState]
	if !ok {
		return nil
	}
	names := make([]string, len(DefaultStages))
	for i, d := range DefaultStages {
		names[i] = d.Name
	}
	_, err := tx.Exec(ctx, `UPDATE backlog_tasks t SET stage_id = s.id
		FROM agent_stages s
		WHERE t.id=$1 AND s.agent_id=t.agent_id AND s.name=$2
		  AND (t.stage_id IS NULL OR t.stage_id IN (
		      SELECT d.id FROM agent_stages d
		      WHERE d.agent_id=t.agent_id
		        AND (d.name = ANY($3) OR ($4 AND d.created_by='agent'))))`,
		taskID, target, names, terminalState(newState))
	return err
}

// transition führt einen validierten Zustandsübergang inkl. Historie aus.
func (s *Store) transition(ctx context.Context, id uuid.UUID, to, note string, set string, args ...any) (Task, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback(ctx)

	t, err := scanTask(tx.QueryRow(ctx, "SELECT "+taskCols+" FROM backlog_tasks WHERE id=$1 FOR UPDATE", id))
	if err != nil {
		return Task{}, err
	}
	if !transitionAllowed(t.State, to) {
		return Task{}, fmt.Errorf("%w: %s → %s", ErrInvalidTransition, t.State, to)
	}
	query := "UPDATE backlog_tasks SET state=$1, updated_at=now()"
	if set != "" {
		query += ", " + set
	}
	query += " WHERE id=$2"
	if _, err := tx.Exec(ctx, query, append([]any{to, id}, args...)...); err != nil {
		return Task{}, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO task_transitions (task_id, from_state, to_state, note)
		VALUES ($1,$2,$3,$4)`, id, t.State, to, note); err != nil {
		return Task{}, err
	}
	if err := syncStage(ctx, tx, id, to); err != nil {
		return Task{}, err
	}
	// Die Aufgabe hat gerade eine Agenten-Spalte verlassen — steht die jetzt
	// leer, verschwindet sie mit. Das hält das Board auf den Arbeitszuständen,
	// die es wirklich gibt.
	if terminalState(to) {
		if _, err := cleanupEmptyAgentStages(ctx, tx, t.AgentID); err != nil {
			return Task{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Task{}, err
	}
	return s.Get(ctx, id)
}

// Block parkt die Aufgabe: Korrelations-Key + Runtime-Session für --resume (spec/12, spec/13).
func (s *Store) Block(ctx context.Context, id uuid.UUID, correlationKey, sessionID, question string) (Task, error) {
	return s.transition(ctx, id, StateBlocked, "blocked: "+question,
		"correlation_key=$3, runtime_session_id=$4, resume_input=NULL", correlationKey, sessionID)
}

// Complete schließt die Aufgabe ab (done) oder scheitert sie (failed).
func (s *Store) Complete(ctx context.Context, id uuid.UUID, state, result, errMsg string) (Task, error) {
	if state != StateDone && state != StateFailed {
		return Task{}, fmt.Errorf("%w: complete nach %s", ErrInvalidTransition, state)
	}
	return s.transition(ctx, id, state, "", "result=$3, error=NULLIF($4,''), correlation_key=NULL", result, errMsg)
}

// Reopen setzt eine in_progress-Aufgabe zurück auf open (z. B. Kill mitten im Lauf).
func (s *Store) Reopen(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	return s.transition(ctx, id, StateOpen, note, "")
}

// RequeueOrphaned setzt beim Start alle in_progress-Aufgaben zurück auf open.
// Ein frisch gestarteter Control-Plane-Prozess hat keine lebenden Daemon-
// Sessions — eine Aufgabe in in_progress hing also an einer Sandbox, die mit dem
// letzten Prozess (Crash/Deploy) verschwunden ist. Ohne dies bliebe sie dauerhaft
// hängen, weil tick/ClaimNext nur state='open' sehen. blocked-Aufgaben bleiben
// unberührt (sie wachen per Korrelation auf).
//
// HA-Hinweis: korrekt für Single-Node (aktueller Stand). Liefen mehrere aktive
// Control-Plane-Prozesse, würde ein Neustart laufende Aufgaben anderer Nodes
// zurücksetzen — das bräuchte node-übergreifende Session-Liveness.
func (s *Store) RequeueOrphaned(ctx context.Context) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx,
		`UPDATE backlog_tasks SET state='open', updated_at=now()
		 WHERE state='in_progress' RETURNING id`)
	if err != nil {
		return 0, err
	}
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if _, err := tx.Exec(ctx, `INSERT INTO task_transitions (task_id, from_state, to_state, note)
			VALUES ($1,'in_progress','open','reconcile: control plane neu gestartet')`, id); err != nil {
			return 0, err
		}
		if err := syncStage(ctx, tx, id, StateOpen); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

// Retry plant eine gescheiterte oder verworfene Aufgabe erneut ein: zurück auf
// open, altes Ergebnis/Fehler geleert (die Historie steht in task_transitions),
// und der Agent wird geweckt.
func (s *Store) Retry(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	t, err := s.transition(ctx, id, StateOpen, note,
		"result=NULL, error=NULL, correlation_key=NULL, archived_at=NULL")
	if err != nil {
		return t, err
	}
	s.notify(ctx, t.AgentID)
	return t, nil
}

// Archive blendet eine terminale Aufgabe aus dem aktiven Backlog aus.
// Kein Löschen: Historie und Recording-Verweise bleiben vollständig.
func (s *Store) Archive(ctx context.Context, id uuid.UUID) (Task, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET archived_at=now()
		WHERE id=$1 AND archived_at IS NULL AND state IN ('done','failed','cancelled')`, id)
	if err != nil {
		return Task{}, err
	}
	if tag.RowsAffected() == 0 {
		t, err := s.Get(ctx, id)
		if err != nil {
			return t, err
		}
		return t, fmt.Errorf("%w: nur abgeschlossene Aufgaben lassen sich archivieren (Zustand %s)",
			ErrInvalidTransition, t.State)
	}
	t, err := s.Get(ctx, id)
	if err == nil {
		// Archivieren kann eine Agenten-Spalte geleert haben.
		_, _ = cleanupEmptyAgentStages(ctx, s.pool, t.AgentID)
	}
	return t, err
}

// ArchiveTerminalOlderThan archiviert organisationsweit jede terminale Aufgabe,
// die seit `age` nicht mehr angefasst wurde, und räumt danach die leer
// gewordenen Agenten-Spalten ab. Das ist der Selbstaufräum-Pfad: ohne ihn hält
// jede erledigte Karte ihre Spalte am Leben, und ein Board, auf dem ein Agent
// tagelang gearbeitet hat, wächst zu einem Verlauf statt eines Zustands — das
// Spalten-Cleanup allein greift nie, weil die Leiche in der Spalte liegen
// bleibt. Wer sofort aufräumen will, hat weiter ArchiveTerminal (UI-Button).
//
// Bewusst zeitbasiert statt „nach jedem Lauf": frisch Erledigtes soll auf dem
// Board sichtbar bleiben, sonst verschwindet die Arbeit des letzten Laufs vor
// den Augen dessen, der sie gerade prüfen will.
func (s *Store) ArchiveTerminalOlderThan(ctx context.Context, age time.Duration) (int64, error) {
	rows, err := s.pool.Query(ctx, `UPDATE backlog_tasks SET archived_at=now()
		WHERE archived_at IS NULL AND state IN ('done','failed','cancelled')
		  AND updated_at < now() - $1::interval
		RETURNING agent_id`, age.String())
	if err != nil {
		return 0, err
	}
	var archived int64
	seen := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		seen[id] = true
		archived++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for id := range seen {
		if _, err := cleanupEmptyAgentStages(ctx, s.pool, id); err != nil {
			return archived, err
		}
	}
	return archived, nil
}

// ArchiveTerminal räumt auf: archiviert alle terminalen Aufgaben eines Agenten.
func (s *Store) ArchiveTerminal(ctx context.Context, agentID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET archived_at=now()
		WHERE agent_id=$1 AND archived_at IS NULL AND state IN ('done','failed','cancelled')`, agentID)
	if err != nil {
		return 0, err
	}
	// Archivieren kann Agenten-Spalten geleert haben — abräumen.
	_, _ = cleanupEmptyAgentStages(ctx, s.pool, agentID)
	return tag.RowsAffected(), nil
}

func (s *Store) Cancel(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	return s.transition(ctx, id, StateCancelled, note, "")
}

// CorrelateWake ist die blocked→working-Kante (M4): ein eingehendes Event mit
// Korrelations-Key öffnet die geparkte Aufgabe mit Resume-Input neu und weckt
// den Agenten. Liefert ErrNotFound, wenn nichts auf den Key wartet.
func (s *Store) CorrelateWake(ctx context.Context, correlationKey, resumeInput string) (Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx, "SELECT "+taskCols+` FROM backlog_tasks
		WHERE state='blocked' AND correlation_key=$1
		ORDER BY updated_at LIMIT 1`, correlationKey))
	if err != nil {
		return Task{}, err
	}
	t, err = s.transition(ctx, t.ID, StateOpen, "korreliertes Event: "+correlationKey,
		"resume_input=$3, priority=1", resumeInput)
	if err != nil {
		return Task{}, err
	}
	s.notify(ctx, t.AgentID)
	return t, nil
}

func (s *Store) Transitions(ctx context.Context, taskID uuid.UUID) ([]Transition, error) {
	rows, err := s.pool.Query(ctx, `SELECT from_state, to_state, note, created_at
		FROM task_transitions WHERE task_id=$1 ORDER BY id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Transition
	for rows.Next() {
		var tr Transition
		if err := rows.Scan(&tr.FromState, &tr.ToState, &tr.Note, &tr.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, tr)
	}
	return out, rows.Err()
}

// HasOpen meldet, ob der Agent offene Arbeit hat (Tick-Entscheidung ohne LLM).
func (s *Store) HasOpen(ctx context.Context, agentID uuid.UUID) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM backlog_tasks WHERE agent_id=$1 AND state='open'", agentID).Scan(&n)
	return n > 0, err
}

// --- Stages (Kanban-Overlay, pro Agent) ---

const stageCols = "id, agent_id, name, position, color, created_by, created_at"

func scanStage(row pgx.Row) (Stage, error) {
	var st Stage
	err := row.Scan(&st.ID, &st.AgentID, &st.Name, &st.Position, &st.Color, &st.CreatedBy, &st.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, err
}

// ListStages liefert die Spalten eines Agenten in Anzeigereihenfolge.
func (s *Store) ListStages(ctx context.Context, agentID uuid.UUID) ([]Stage, error) {
	rows, err := s.pool.Query(ctx, "SELECT "+stageCols+
		" FROM agent_stages WHERE agent_id=$1 ORDER BY position, created_at", agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stage
	for rows.Next() {
		st, err := scanStage(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// querier abstrahiert Pool und Transaktion, damit Stage-Operationen auch
// innerhalb einer Transaktion laufen können (SetTaskStageByName).
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// CreateStage hängt eine neue Spalte hinten an (position = max+1) — der
// menschliche Pfad (UI/API); Agenten-Spalten entstehen über EnsureStage.
func (s *Store) CreateStage(ctx context.Context, agentID uuid.UUID, name, color string) (Stage, error) {
	return createStage(ctx, s.pool, agentID, name, color, "human")
}

func createStage(ctx context.Context, q querier, agentID uuid.UUID, name, color, createdBy string) (Stage, error) {
	return scanStage(q.QueryRow(ctx, `INSERT INTO agent_stages (id, agent_id, name, color, position, created_by)
		VALUES ($1,$2,$3,$4,
			(SELECT COALESCE(MAX(position),-1)+1 FROM agent_stages WHERE agent_id=$2),$5)
		RETURNING `+stageCols, uuid.New(), agentID, name, color, createdBy))
}

// DefaultStages sind die Spalten, die jeder Agent von Beginn an haben soll —
// ein schlichtes Kanban. Die erste Spalte ist die Landezone neuer Aufgaben
// (siehe Create). Farben nutzen dieselben CSS-Variablen wie das Frontend.
var DefaultStages = []struct{ Name, Color string }{
	{"Backlog", "var(--text-muted)"},
	{"In Arbeit", "var(--text-accent)"},
	{"Erledigt", "var(--text-success)"},
}

// SeedDefaultStages legt die Default-Spalten für einen Agenten an — idempotent:
// hat der Agent bereits Stages, passiert nichts. So bekommt jeder neue Agent
// von Anfang an ein Board, und ein Backfill bestehender Agenten ist gefahrlos.
func (s *Store) SeedDefaultStages(ctx context.Context, agentID uuid.UUID) error {
	var n int
	if err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM agent_stages WHERE agent_id=$1", agentID).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, d := range DefaultStages {
		if _, err := s.CreateStage(ctx, agentID, d.Name, d.Color); err != nil {
			return err
		}
	}
	return nil
}

// UpdateStage ändert Name, Farbe und Position einer Spalte.
func (s *Store) UpdateStage(ctx context.Context, id uuid.UUID, name, color string, position int) (Stage, error) {
	return scanStage(s.pool.QueryRow(ctx, `UPDATE agent_stages SET name=$2, color=$3, position=$4
		WHERE id=$1 RETURNING `+stageCols, id, name, color, position))
}

// DeleteStage entfernt eine Spalte; Aufgaben darin fallen per FK auf stage=NULL.
func (s *Store) DeleteStage(ctx context.Context, id uuid.UUID) error {
	ct, err := s.pool.Exec(ctx, "DELETE FROM agent_stages WHERE id=$1", id)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ReorderStages setzt die Positionen anhand der übergebenen Reihenfolge.
func (s *Store) ReorderStages(ctx context.Context, agentID uuid.UUID, ordered []uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for i, id := range ordered {
		if _, err := tx.Exec(ctx,
			"UPDATE agent_stages SET position=$1 WHERE id=$2 AND agent_id=$3", i, id, agentID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// EnsureStage liefert die gleichnamige Stage oder legt sie an — der Weg, über
// den der Agent per set_stage eine neue Spalte „erfindet". Solche Spalten
// tragen created_by='agent' und werden automatisch abgeräumt, wenn sie leeren.
func (s *Store) EnsureStage(ctx context.Context, agentID uuid.UUID, name string) (Stage, error) {
	return ensureStage(ctx, s.pool, agentID, name)
}

func ensureStage(ctx context.Context, q querier, agentID uuid.UUID, name string) (Stage, error) {
	st, err := scanStage(q.QueryRow(ctx, "SELECT "+stageCols+
		" FROM agent_stages WHERE agent_id=$1 AND name=$2", agentID, name))
	if err == nil {
		return st, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Stage{}, err
	}
	return createStage(ctx, q, agentID, name, "", "agent")
}

// SetTaskStage verschiebt eine Aufgabe in eine Stage (nil = ohne Stage). Rein
// anzeigend — kein Lifecycle-Übergang, kein NOTIFY. Räumt danach leere
// Agenten-Spalten ab (die Aufgabe könnte die letzte in einer gewesen sein).
func (s *Store) SetTaskStage(ctx context.Context, taskID uuid.UUID, stageID *uuid.UUID) (Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		"UPDATE backlog_tasks SET stage_id=$2, updated_at=now() WHERE id=$1 RETURNING "+taskCols,
		taskID, stageID))
	if err == nil {
		_, _ = cleanupEmptyAgentStages(ctx, s.pool, t.AgentID)
	}
	return t, err
}

// SetTaskStageByName ist der Agenten-Pfad (set_stage) in einer Transaktion:
// Stage sicherstellen, Aufgabe verschieben, leere Agenten-Spalten abräumen.
// Eine Transaktion, damit kein nebenläufiges Cleanup die frisch angelegte
// Spalte zwischen Anlegen und Zuweisen wegräumt.
func (s *Store) SetTaskStageByName(ctx context.Context, agentID, taskID uuid.UUID, name string) (Stage, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Stage{}, err
	}
	defer tx.Rollback(ctx)
	st, err := ensureStage(ctx, tx, agentID, name)
	if err != nil {
		return Stage{}, err
	}
	if _, err := tx.Exec(ctx,
		"UPDATE backlog_tasks SET stage_id=$2, updated_at=now() WHERE id=$1", taskID, st.ID); err != nil {
		return Stage{}, err
	}
	if _, err := cleanupEmptyAgentStages(ctx, tx, agentID); err != nil {
		return Stage{}, err
	}
	return st, tx.Commit(ctx)
}

// CleanupEmptyAgentStages räumt Spalten ab, die der Agent selbst angelegt hat
// (created_by='agent') und in denen keine aktive (unarchivierte) Aufgabe mehr
// liegt — die Spalten „erfindet" der Agent im Arbeiten, also verschwinden sie
// auch wieder von selbst. Menschlich angelegte Spalten bleiben unberührt.
func (s *Store) CleanupEmptyAgentStages(ctx context.Context, agentID uuid.UUID) (int64, error) {
	return cleanupEmptyAgentStages(ctx, s.pool, agentID)
}

func cleanupEmptyAgentStages(ctx context.Context, q querier, agentID uuid.UUID) (int64, error) {
	tag, err := q.Exec(ctx, `DELETE FROM agent_stages s
		WHERE s.agent_id=$1 AND s.created_by='agent'
		  AND NOT EXISTS (SELECT 1 FROM backlog_tasks t
		      WHERE t.stage_id=s.id AND t.archived_at IS NULL)`, agentID)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

// --- Notizen (proaktive Zwischenstände an der Aufgabe) ---

// AddNote hängt eine Notiz an die Aufgabe. Leerer Inhalt ist kein Fehler,
// aber auch keine Notiz.
func (s *Store) AddNote(ctx context.Context, taskID uuid.UUID, author, content string) (Note, error) {
	var n Note
	err := s.pool.QueryRow(ctx, `INSERT INTO task_notes (id, task_id, author, content)
		VALUES ($1,$2,$3,$4) RETURNING id, task_id, author, content, created_at`,
		uuid.New(), taskID, author, content).
		Scan(&n.ID, &n.TaskID, &n.Author, &n.Content, &n.CreatedAt)
	return n, err
}

// ListNotes liefert die Notizen einer Aufgabe in zeitlicher Reihenfolge.
func (s *Store) ListNotes(ctx context.Context, taskID uuid.UUID) ([]Note, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, task_id, author, content, created_at
		FROM task_notes WHERE task_id=$1 ORDER BY created_at, id`, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Note
	for rows.Next() {
		var n Note
		if err := rows.Scan(&n.ID, &n.TaskID, &n.Author, &n.Content, &n.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}
