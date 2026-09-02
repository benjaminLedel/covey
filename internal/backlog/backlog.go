// Package backlog implements the backlog as a first-class Postgres object:
// state, priority, origin, history, correlation key (spec/03).
// The queue mechanics are SELECT … FOR UPDATE SKIP LOCKED + LISTEN/NOTIFY.
package backlog

import (
	"context"
	"errors"
	"fmt"
	"strings"
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

// NotifyChannel is the Postgres channel for wake events (payload: agent_id).
const NotifyChannel = "covey_wake"

// validTransitions encodes the state machine of a task.
var validTransitions = map[string][]string{
	StateOpen:       {StateInProgress, StateCancelled},
	StateInProgress: {StateBlocked, StateDone, StateFailed, StateOpen, StateCancelled},
	StateBlocked:    {StateOpen, StateCancelled}, // wake-on-correlation reopens it (with resume)
	StateFailed:     {StateOpen},                 // retry: reschedule manually
	StateCancelled:  {StateOpen},                 // resuming a discarded task
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
	ErrNotFound          = errors.New("task not found")
	ErrInvalidTransition = errors.New("invalid state transition")
)

type Task struct {
	ID               uuid.UUID  `json:"id"`
	OrgID            uuid.UUID  `json:"org_id"`
	AgentID          uuid.UUID  `json:"agent_id"`
	Title            string     `json:"title"`
	Body             string     `json:"body"`
	State            string     `json:"state"`
	Priority         int        `json:"priority"`
	Origin           string     `json:"origin"`
	CorrelationKey   *string    `json:"correlation_key,omitempty"`
	RuntimeSessionID *string    `json:"runtime_session_id,omitempty"`
	ResumeInput      *string    `json:"resume_input,omitempty"`
	Result           *string    `json:"result,omitempty"`
	Error            *string    `json:"error,omitempty"`
	StageID          *uuid.UUID `json:"stage_id,omitempty"`
	ParentTaskID     *uuid.UUID `json:"parent_task_id,omitempty"`
	ArchivedAt       *time.Time `json:"archived_at,omitempty"`
	// DaemonRetries counts how often this task lost its sandbox connection in a
	// row (see ReopenAfterDaemonLoss). Any run that ends for a different reason
	// puts it back to zero — "in a row" is the whole point.
	DaemonRetries int       `json:"daemon_retries"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Stage is a freely definable Kanban column of an agent (an overlay on top of
// the lifecycle state). Order via position, color optional (CSS token).
// CreatedBy distinguishes the origin: 'agent' columns ("invented" via
// set_stage) are cleaned up automatically as soon as they are empty.
type Stage struct {
	ID        uuid.UUID `json:"id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Name      string    `json:"name"`
	Position  int       `json:"position"`
	Color     string    `json:"color"`
	CreatedBy string    `json:"created_by"`
	CreatedAt time.Time `json:"created_at"`
}

// Note is a proactive note on a task: interim results, findings, things tried —
// task-bound, in contrast to the memory (generally valid).
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
	// OnComplete is called after a task has ended — done or failed. It exists
	// for the notification mails (#169), and it is a function field rather
	// than an import so that the backlog keeps knowing nothing about who is
	// told what.
	//
	// Five places in the orchestrator finish a task, and each of them would
	// have been a place to forget the call. The hook sits where they all pass
	// through. It runs synchronously, so it has to stay cheap, and its failure
	// is not the task's.
	OnComplete func(context.Context, Task)
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

const taskCols = `id, org_id, agent_id, title, body, state, priority, origin,
	correlation_key, runtime_session_id, resume_input, result, error, stage_id, parent_task_id,
	archived_at, daemon_retries, created_at, updated_at`

func scanTask(row pgx.Row) (Task, error) {
	var t Task
	err := row.Scan(&t.ID, &t.OrgID, &t.AgentID, &t.Title, &t.Body, &t.State, &t.Priority, &t.Origin,
		&t.CorrelationKey, &t.RuntimeSessionID, &t.ResumeInput, &t.Result, &t.Error, &t.StageID, &t.ParentTaskID,
		&t.ArchivedAt, &t.DaemonRetries, &t.CreatedAt, &t.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// Create creates a task and fires NOTIFY so that the dispatch loop wakes up.
func (s *Store) Create(ctx context.Context, orgID, agentID uuid.UUID, title, body, origin string, priority int) (Task, error) {
	if priority == 0 {
		priority = 5
	}
	// A new task lands in the agent's first stage (if one is defined).
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

// ChildSpec describes a task that emerges from another one: the continuation of
// a run aborted at the turn limit, or a subtask the agent split off itself. An
// empty AgentID = the same agent as the originating task (delegation points it
// at a colleague).
//
// SessionID/ResumeInput are only set for a continuation: with them the runtime
// picks the aborted session back up instead of starting from scratch.
type ChildSpec struct {
	AgentID     uuid.UUID
	Title       string
	Body        string
	Origin      string
	Priority    int
	SessionID   string
	ResumeInput string
}

// CreateChild creates a task that hangs off an originating task. It inherits
// the organization and (by default) the agent from it — a task can therefore
// never fall out of the org of its parent task.
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

// AncestorsWithOrigin counts how many ancestors of a task (including the task
// itself) carry an origin with the given prefix. This carries the loop
// protection: a chain of continuations or subtasks must not grow endlessly, and
// the only reliable counter is the chain itself.
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

// ChainStart is the moment the chain a task belongs to began: the created_at of
// its oldest ancestor.
//
// The heartbeat watermark asks with it "what has this run done itself" — and a
// run is the whole chain, not its last segment: a continuation carries on where
// the turn limit cut off, and the comment written before it belongs to the same
// run. Anchoring on the heartbeat's last_fired_at instead would be wrong, since
// the schedule ticks on (and moves that timestamp) while a long run is still
// going.
func (s *Store) ChainStart(ctx context.Context, id uuid.UUID) (time.Time, error) {
	var start time.Time
	err := s.pool.QueryRow(ctx, `
		WITH RECURSIVE chain AS (
			SELECT id, parent_task_id, created_at FROM backlog_tasks WHERE id=$1
			UNION ALL
			SELECT t.id, t.parent_task_id, t.created_at
			FROM backlog_tasks t JOIN chain c ON t.id = c.parent_task_id
		)
		SELECT MIN(created_at) FROM chain`, id).Scan(&start)
	return start, err
}

// CountChildren counts the tasks that emerged directly from a task — the width
// of the decomposition.
func (s *Store) CountChildren(ctx context.Context, parentID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM backlog_tasks WHERE parent_task_id=$1", parentID).Scan(&n)
	return n, err
}

// OpenWithTitle says whether the agent already has a non-terminal task with
// this title. The duplicate protection for self-created tasks: an agent that
// creates the same task anew on every run would otherwise build itself a queue
// that never empties.
func (s *Store) OpenWithTitle(ctx context.Context, agentID uuid.UUID, title string) (bool, error) {
	var exists bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM backlog_tasks
		WHERE agent_id=$1 AND title=$2 AND state NOT IN ('done','failed','cancelled'))`,
		agentID, title).Scan(&exists)
	return exists, err
}

func (s *Store) notify(ctx context.Context, agentID uuid.UUID) {
	// The wake signal is best-effort — the periodic tick catches losses.
	_, _ = s.pool.Exec(ctx, "SELECT pg_notify($1,$2)", NotifyChannel, agentID.String())
}

func (s *Store) Get(ctx context.Context, id uuid.UUID) (Task, error) {
	return scanTask(s.pool.QueryRow(ctx, "SELECT "+taskCols+" FROM backlog_tasks WHERE id=$1", id))
}

// ListByAgent returns the active backlog; includeArchived adds the archive.
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

// SearchMaxResults caps what a search hands out. A search is a look, not an
// export: whoever needs everything takes the archive view.
const SearchMaxResults = 50

// likeEscaper defuses the wildcards of LIKE. Without it a '%' typed into the
// search field would fetch the whole backlog and an '_' would quietly match
// more than it says — the field takes text, not a pattern.
var likeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// SearchByAgent finds an agent's tasks by their text: title, body, and what a
// run left behind (result, error). It searches the archive along with the
// active board — deliberately, because whoever searches is looking for
// something that is no longer in front of them; a search that stopped at the
// board's edge would find precisely the tasks one could already see.
//
// Newest first: an older task is found by its words, ordered by its date.
func (s *Store) SearchByAgent(ctx context.Context, agentID uuid.UUID, q string, limit int) ([]Task, error) {
	if limit <= 0 || limit > SearchMaxResults {
		limit = SearchMaxResults
	}
	pattern := "%" + likeEscaper.Replace(q) + "%"
	rows, err := s.pool.Query(ctx, "SELECT "+taskCols+` FROM backlog_tasks
		WHERE agent_id=$1 AND (title ILIKE $2 ESCAPE '\' OR body ILIKE $2 ESCAPE '\'
			OR coalesce(result,'') ILIKE $2 ESCAPE '\' OR coalesce(error,'') ILIKE $2 ESCAPE '\')
		ORDER BY created_at DESC
		LIMIT $3`, agentID, pattern, limit)
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

// ClaimNext fetches the agent's next open task — serially, with SKIP LOCKED so
// that competing dispatchers do not grab the same one.
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

// stateStage maps the lifecycle state onto the default column in which a task
// is then expected. It only applies as long as the task sits in a default
// column (or in none) — a deliberately chosen stage of its own (agent via
// set_stage, human via drag & drop) is never overwritten.
//
// The column names are data: they are seeded per agent (DefaultStages) and
// matched here by name, so renaming them would silently stop auto-follow on
// every board that already exists.
var stateStage = map[string]string{
	StateOpen:       "Backlog",
	StateInProgress: "In Arbeit",
	StateBlocked:    "In Arbeit",
	StateDone:       "Erledigt",
	StateFailed:     "Erledigt",
	StateCancelled:  "Erledigt",
}

// terminalState says whether a state is the end of the task.
func terminalState(state string) bool {
	return state == StateDone || state == StateFailed || state == StateCancelled
}

// syncStage lets the stage follow the new state (within the transaction of the
// state transition). If the target column is missing (renamed/deleted), nothing
// happens — auto-follow is a convenience, never a constraint.
//
// On the transition into a terminal state, auto-follow additionally applies to
// the columns the agent created itself: a completed task does not belong in
// "Recherche", it belongs in "Erledigt". Without this, a terminal task would
// stay behind in every invented column, keeping that column permanently "not
// empty" — and over weeks the board would collect a dozen dead working states.
// Columns created by humans stay untouched here as well: a deliberate placement
// is never overwritten.
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

// transition performs a validated state transition including its history entry.
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
	// The task has just left an agent column — if that column now stands empty,
	// it goes with it. This keeps the board on the working states that really
	// exist.
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

// Block parks the task: correlation key + runtime session for --resume (spec/12, spec/13).
//
// This clears daemon_retries: the run got far enough to ask a question, so
// whatever connection losses came before it were not a series.
func (s *Store) Block(ctx context.Context, id uuid.UUID, correlationKey, sessionID, question string) (Task, error) {
	return s.transition(ctx, id, StateBlocked, "blocked: "+question,
		"correlation_key=$3, runtime_session_id=$4, resume_input=NULL, daemon_retries=0", correlationKey, sessionID)
}

// Complete finishes the task (done) or fails it (failed).
func (s *Store) Complete(ctx context.Context, id uuid.UUID, state, result, errMsg string) (Task, error) {
	if state != StateDone && state != StateFailed {
		return Task{}, fmt.Errorf("%w: complete to %s", ErrInvalidTransition, state)
	}
	task, err := s.transition(ctx, id, state, "", "result=$3, error=NULLIF($4,''), correlation_key=NULL", result, errMsg)
	if err == nil && s.OnComplete != nil {
		s.OnComplete(ctx, task)
	}
	return task, err
}

// Reopen resets an in_progress task back to open (e.g. a kill mid-run, or the
// budget stop). The run ended for its own reasons, not because the link died —
// so daemon_retries goes back to zero. Use ReopenAfterDaemonLoss for the other
// case; the two are deliberately separate calls.
func (s *Store) Reopen(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	return s.transition(ctx, id, StateOpen, note, "daemon_retries=0")
}

// ReopenAfterDaemonLoss requeues a task whose sandbox connection dropped
// mid-run and counts that loss. The returned task carries the new count, so the
// caller can decide when a series has gone on long enough to be a standing
// fault rather than a blip — see the orchestrator's maxDaemonLossRetries.
//
// The counting has to happen inside the same transaction as the state change:
// a read-modify-write from outside would lose a loss whenever two runs of the
// same task overlap on a restart, and it is precisely a restart that produces
// these losses.
func (s *Store) ReopenAfterDaemonLoss(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	return s.transition(ctx, id, StateOpen, note, "daemon_retries = daemon_retries + 1")
}

// RequeueOrphaned resets all in_progress tasks back to open at startup. A
// freshly started control-plane process has no live daemon sessions — a task in
// in_progress therefore hung off a sandbox that vanished together with the last
// process (crash/deploy). Without this it would stay stuck forever, because
// tick/ClaimNext only see state='open'. blocked tasks stay untouched (they wake
// up via correlation).
//
// daemon_retries is deliberately left alone here, unlike in Reopen. A restart
// is not proof that the run was fine — a control plane crash-looping on the
// same task is exactly the standing fault the counter exists to catch, and
// clearing it on every start would hide precisely that case.
//
// HA note: correct for a single node (the current state). With several active
// control-plane processes, a restart would reset the running tasks of other
// nodes — that would need cross-node session liveness.
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
			VALUES ($1,'in_progress','open','reconcile: control plane restarted')`, id); err != nil {
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

// Retry reschedules a failed or discarded task: back to open, the old
// result/error cleared (the history lives in task_transitions), and the agent
// is woken.
//
// daemon_retries goes with it. Somebody is deliberately asking for another
// attempt — including for a task that was failed BECAUSE of a connection
// series — and that attempt has to start from a clean count, or the task would
// fail again on its first hiccup.
func (s *Store) Retry(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	t, err := s.transition(ctx, id, StateOpen, note,
		"result=NULL, error=NULL, correlation_key=NULL, archived_at=NULL, daemon_retries=0")
	if err != nil {
		return t, err
	}
	s.notify(ctx, t.AgentID)
	return t, nil
}

// Archive hides a terminal task from the active backlog. No deletion: history
// and recording references stay complete.
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
		return t, fmt.Errorf("%w: only completed tasks can be archived (state %s)",
			ErrInvalidTransition, t.State)
	}
	t, err := s.Get(ctx, id)
	if err == nil {
		// Archiving may have emptied an agent column.
		_, _ = cleanupEmptyAgentStages(ctx, s.pool, t.AgentID)
	}
	return t, err
}

// ArchiveTerminalOlderThan archives, org-wide, every terminal task that has not
// been touched for `age`, and afterwards clears out the agent columns that have
// become empty. This is the self-cleanup path: without it every completed card
// keeps its column alive, and a board an agent has worked on for days grows
// into a history instead of a state — the column cleanup alone never fires,
// because the corpse stays lying in the column. Whoever wants to clean up right
// away still has ArchiveTerminal (UI button).
//
// Deliberately time-based instead of "after every run": freshly completed work
// should stay visible on the board, otherwise the work of the last run vanishes
// before the eyes of the person who is about to review it.
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

// ArchiveTerminal tidies up: archives all terminal tasks of an agent.
func (s *Store) ArchiveTerminal(ctx context.Context, agentID uuid.UUID) (int64, error) {
	tag, err := s.pool.Exec(ctx, `UPDATE backlog_tasks SET archived_at=now()
		WHERE agent_id=$1 AND archived_at IS NULL AND state IN ('done','failed','cancelled')`, agentID)
	if err != nil {
		return 0, err
	}
	// Archiving may have emptied agent columns — clear them out.
	_, _ = cleanupEmptyAgentStages(ctx, s.pool, agentID)
	return tag.RowsAffected(), nil
}

func (s *Store) Cancel(ctx context.Context, id uuid.UUID, note string) (Task, error) {
	return s.transition(ctx, id, StateCancelled, note, "")
}

// CorrelateWake is the blocked→working edge (M4): an incoming event with a
// correlation key reopens the parked task with resume input and wakes the
// agent. Returns ErrNotFound if nothing is waiting on the key.
func (s *Store) CorrelateWake(ctx context.Context, correlationKey, resumeInput string) (Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx, "SELECT "+taskCols+` FROM backlog_tasks
		WHERE state='blocked' AND correlation_key=$1
		ORDER BY updated_at LIMIT 1`, correlationKey))
	if err != nil {
		return Task{}, err
	}
	t, err = s.transition(ctx, t.ID, StateOpen, "correlated event: "+correlationKey,
		"resume_input=$3, priority=1", resumeInput)
	if err != nil {
		return Task{}, err
	}
	s.notify(ctx, t.AgentID)
	return t, nil
}

// InOrg answers whether a task belongs to this organization. The answer is
// deliberately a boolean and not a task: the caller (the taskScoped middleware)
// only wants to check the boundary, not the data.
func (s *Store) InOrg(ctx context.Context, orgID, taskID uuid.UUID) bool {
	var one int
	err := s.pool.QueryRow(ctx,
		"SELECT 1 FROM backlog_tasks WHERE id=$1 AND org_id=$2", taskID, orgID).Scan(&one)
	return err == nil
}

// StageInOrg checks the same for a board column. Columns hang off the agent, so
// the organization is only settled after the join.
func (s *Store) StageInOrg(ctx context.Context, orgID, stageID uuid.UUID) bool {
	var one int
	err := s.pool.QueryRow(ctx, `SELECT 1 FROM agent_stages st JOIN agents a ON a.id = st.agent_id
		WHERE st.id=$1 AND a.org_id=$2`, stageID, orgID).Scan(&one)
	return err == nil
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

// HasOpen reports whether the agent has open work (tick decision without an LLM).
func (s *Store) HasOpen(ctx context.Context, agentID uuid.UUID) (bool, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM backlog_tasks WHERE agent_id=$1 AND state='open'", agentID).Scan(&n)
	return n > 0, err
}

// --- Stages (Kanban overlay, per agent) ---

const stageCols = "id, agent_id, name, position, color, created_by, created_at"

func scanStage(row pgx.Row) (Stage, error) {
	var st Stage
	err := row.Scan(&st.ID, &st.AgentID, &st.Name, &st.Position, &st.Color, &st.CreatedBy, &st.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrNotFound
	}
	return st, err
}

// ListStages returns an agent's columns in display order.
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

// querier abstracts pool and transaction so that stage operations can also run
// inside a transaction (SetTaskStageByName).
type querier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// CreateStage appends a new column at the end (position = max+1) — the human
// path (UI/API); agent columns come into being via EnsureStage.
func (s *Store) CreateStage(ctx context.Context, agentID uuid.UUID, name, color string) (Stage, error) {
	return createStage(ctx, s.pool, agentID, name, color, "human")
}

func createStage(ctx context.Context, q querier, agentID uuid.UUID, name, color, createdBy string) (Stage, error) {
	return scanStage(q.QueryRow(ctx, `INSERT INTO agent_stages (id, agent_id, name, color, position, created_by)
		VALUES ($1,$2,$3,$4,
			(SELECT COALESCE(MAX(position),-1)+1 FROM agent_stages WHERE agent_id=$2),$5)
		RETURNING `+stageCols, uuid.New(), agentID, name, color, createdBy))
}

// DefaultStages are the columns every agent should have from the start — a
// plain Kanban. The first column is the landing zone for new tasks (see
// Create). Colors use the same CSS variables as the frontend. The names are
// German because they are persisted per agent and matched by name (stateStage);
// renaming them needs a migration of the existing boards.
var DefaultStages = []struct{ Name, Color string }{
	{"Backlog", "var(--text-muted)"},
	{"In Arbeit", "var(--text-accent)"},
	{"Erledigt", "var(--text-success)"},
}

// SeedDefaultStages creates the default columns for an agent — idempotent: if
// the agent already has stages, nothing happens. This way every new agent has a
// board from the start, and backfilling existing agents is harmless.
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

// UpdateStage changes name, color and position of a column.
func (s *Store) UpdateStage(ctx context.Context, id uuid.UUID, name, color string, position int) (Stage, error) {
	return scanStage(s.pool.QueryRow(ctx, `UPDATE agent_stages SET name=$2, color=$3, position=$4
		WHERE id=$1 RETURNING `+stageCols, id, name, color, position))
}

// DeleteStage removes a column; tasks in it fall back to stage=NULL via the FK.
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

// ReorderStages sets the positions according to the given order.
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

// EnsureStage returns the stage of that name or creates it — the way the agent
// "invents" a new column via set_stage. Such columns carry created_by='agent'
// and are cleared out automatically once they run empty.
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

// SetTaskStage moves a task into a stage (nil = no stage). Purely a matter of
// display — no lifecycle transition, no NOTIFY. Clears out empty agent columns
// afterwards (the task might have been the last one in such a column).
func (s *Store) SetTaskStage(ctx context.Context, taskID uuid.UUID, stageID *uuid.UUID) (Task, error) {
	t, err := scanTask(s.pool.QueryRow(ctx,
		"UPDATE backlog_tasks SET stage_id=$2, updated_at=now() WHERE id=$1 RETURNING "+taskCols,
		taskID, stageID))
	if err == nil {
		_, _ = cleanupEmptyAgentStages(ctx, s.pool, t.AgentID)
	}
	return t, err
}

// SetTaskStageByName is the agent path (set_stage) in one transaction: ensure
// the stage, move the task, clear out empty agent columns. One transaction, so
// that no concurrent cleanup removes the freshly created column between
// creating it and assigning it.
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

// CleanupEmptyAgentStages clears out columns the agent created itself
// (created_by='agent') and in which no active (unarchived) task is left — the
// agent "invents" those columns while working, so they also disappear again by
// themselves. Columns created by humans stay untouched.
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

// --- Notes (proactive interim results on the task) ---

// AddNote appends a note to the task. Empty content is not an error, but it is
// not a note either.
func (s *Store) AddNote(ctx context.Context, taskID uuid.UUID, author, content string) (Note, error) {
	var n Note
	err := s.pool.QueryRow(ctx, `INSERT INTO task_notes (id, task_id, author, content)
		VALUES ($1,$2,$3,$4) RETURNING id, task_id, author, content, created_at`,
		uuid.New(), taskID, author, content).
		Scan(&n.ID, &n.TaskID, &n.Author, &n.Content, &n.CreatedAt)
	return n, err
}

// ListNotes returns a task's notes in chronological order.
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
