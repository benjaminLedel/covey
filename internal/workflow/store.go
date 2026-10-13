package workflow

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrNotFound = errors.New("workflow not found")
	// ErrCaseOpen: a case already has an open run in this workflow. The
	// database refuses the second one even when two calls race.
	ErrCaseOpen = errors.New("workflow: the case already has an open run")
)

// InvalidError carries what keeps a definition from being saved.
type InvalidError struct{ Problems []Problem }

func (e *InvalidError) Error() string {
	if len(e.Problems) == 0 {
		return "workflow: invalid"
	}
	return fmt.Sprintf("workflow: %d problem(s), first: %s", len(e.Problems), e.Problems[0])
}

// Stored is one version of a workflow as kept.
type Stored struct {
	ID        uuid.UUID
	OrgID     uuid.UUID
	Slug      string
	Version   int
	Source    string
	Def       *Definition
	UpdatedAt time.Time
}

// Store keeps an organisation's workflows and their runs. Every read and
// write names the organisation; a slug is unique within one only.
type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

var slugPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Save keeps a definition as the workflow's next version. A definition that
// does not parse or does not pass Check is not kept: a version that cannot
// run is no version.
func (s *Store) Save(ctx context.Context, orgID uuid.UUID, slug, source string, by *uuid.UUID) (Stored, error) {
	// "check" and "mine" are routes beside the workflows; a workflow of that
	// name could never be opened.
	if slug == "check" || slug == "mine" {
		return Stored{}, &InvalidError{Problems: []Problem{{Code: "bad-slug", Text: fmt.Sprintf("%q is taken by the interface", slug)}}}
	}
	if !slugPattern.MatchString(slug) {
		return Stored{}, &InvalidError{Problems: []Problem{{Code: "bad-slug", Text: fmt.Sprintf("%q: lower-case letters, digits and dashes", slug)}}}
	}
	def, err := Parse([]byte(source))
	if err != nil {
		return Stored{}, &InvalidError{Problems: []Problem{{Code: "unreadable", Text: err.Error()}}}
	}
	if ps := Check(def); len(ps) > 0 {
		return Stored{}, &InvalidError{Problems: ps}
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Stored{}, err
	}
	defer tx.Rollback(ctx)
	out := Stored{OrgID: orgID, Slug: slug, Source: source, Def: def}
	err = tx.QueryRow(ctx, `SELECT id, version FROM workflows WHERE org_id = $1 AND slug = $2 FOR UPDATE`, orgID, slug).Scan(&out.ID, &out.Version)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		out.ID, out.Version = uuid.New(), 1
		// A racing first save of the same slug meets the unique key; the
		// second caller gets the conflict rather than a silent overwrite.
		if _, err := tx.Exec(ctx, `INSERT INTO workflows (id, org_id, slug, version) VALUES ($1, $2, $3, 1)`, out.ID, orgID, slug); err != nil {
			return Stored{}, err
		}
	case err != nil:
		return Stored{}, err
	default:
		out.Version++
		if _, err := tx.Exec(ctx, `UPDATE workflows SET version = $2, updated_at = now() WHERE id = $1`, out.ID, out.Version); err != nil {
			return Stored{}, err
		}
	}
	if err := tx.QueryRow(ctx, `INSERT INTO workflow_versions (workflow_id, version, source, created_by) VALUES ($1, $2, $3, $4) RETURNING created_at`,
		out.ID, out.Version, source, by).Scan(&out.UpdatedAt); err != nil {
		return Stored{}, err
	}
	return out, tx.Commit(ctx)
}

// Get returns the current version of a workflow.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, slug string) (Stored, error) {
	return s.version(ctx, orgID, slug, 0)
}

// Version returns one version as it was saved.
func (s *Store) Version(ctx context.Context, orgID uuid.UUID, slug string, v int) (Stored, error) {
	return s.version(ctx, orgID, slug, v)
}

func (s *Store) version(ctx context.Context, orgID uuid.UUID, slug string, v int) (Stored, error) {
	out := Stored{OrgID: orgID, Slug: slug}
	err := s.pool.QueryRow(ctx, `
		SELECT w.id, v.version, v.source, v.created_at
		  FROM workflows w
		  JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = CASE WHEN $3 > 0 THEN $3 ELSE w.version END
		 WHERE w.org_id = $1 AND w.slug = $2`, orgID, slug, v).Scan(&out.ID, &out.Version, &out.Source, &out.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Stored{}, ErrNotFound
	}
	if err != nil {
		return Stored{}, err
	}
	// What was saved passed Check; parse it again rather than trust a cache.
	if out.Def, err = Parse([]byte(out.Source)); err != nil {
		return Stored{}, fmt.Errorf("workflow %s version %d no longer parses: %w", slug, out.Version, err)
	}
	return out, nil
}

// List returns the current version of every workflow of an organisation.
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Stored, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.slug, v.version, v.source, v.created_at
		  FROM workflows w
		  JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.version
		 WHERE w.org_id = $1
		 ORDER BY w.slug`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		st := Stored{OrgID: orgID}
		if err := rows.Scan(&st.ID, &st.Slug, &st.Version, &st.Source, &st.UpdatedAt); err != nil {
			return nil, err
		}
		if st.Def, err = Parse([]byte(st.Source)); err != nil {
			return nil, fmt.Errorf("workflow %s version %d no longer parses: %w", st.Slug, st.Version, err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// ListAll returns the current version of every workflow of every
// organisation — what the control plane loads when it starts.
func (s *Store) ListAll(ctx context.Context) ([]Stored, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT w.id, w.org_id, w.slug, v.version, v.source, v.created_at
		  FROM workflows w
		  JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.version
		 ORDER BY w.org_id, w.slug`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Stored
	for rows.Next() {
		var st Stored
		if err := rows.Scan(&st.ID, &st.OrgID, &st.Slug, &st.Version, &st.Source, &st.UpdatedAt); err != nil {
			return nil, err
		}
		if st.Def, err = Parse([]byte(st.Source)); err != nil {
			return nil, fmt.Errorf("workflow %s version %d no longer parses: %w", st.Slug, st.Version, err)
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

// SaveRuns writes runs as the engine reported them. They are written
// together, so a call that moved two runs — one done, the queued one handed
// out — is never half kept.
func (s *Store) SaveRuns(ctx context.Context, workflowID uuid.UUID, runs []Run) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, r := range runs {
		id, err := uuid.Parse(r.ID)
		if err != nil {
			return fmt.Errorf("workflow: run id %q: %w", r.ID, err)
		}
		vars, _ := json.Marshal(r.Vars)
		loops, _ := json.Marshal(r.Loops)
		had, _ := json.Marshal(r.Had)
		log, _ := json.Marshal(r.Log)
		_, err = tx.Exec(ctx, `
			INSERT INTO workflow_runs (id, workflow_id, case_ref, seq, step, state, assignee, since, stuck, unstaffed, vars, loops, had, log)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
			ON CONFLICT (id) DO UPDATE SET
			    step = EXCLUDED.step, state = EXCLUDED.state, assignee = EXCLUDED.assignee,
			    since = EXCLUDED.since, stuck = EXCLUDED.stuck, unstaffed = EXCLUDED.unstaffed,
			    vars = EXCLUDED.vars, loops = EXCLUDED.loops, had = EXCLUDED.had, log = EXCLUDED.log,
			    updated_at = now()
			 WHERE workflow_runs.workflow_id = EXCLUDED.workflow_id`,
			id, workflowID, r.Case, r.Seq(), r.Step, string(r.State), r.Assignee, r.Since, r.Stuck, r.Unstaffed, vars, loops, had, log)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return fmt.Errorf("%w: %s", ErrCaseOpen, r.Case)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// Runs reads a workflow's runs back, in the order they were started — the
// order an engine restored from them queues in.
func (s *Store) Runs(ctx context.Context, workflowID uuid.UUID) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, case_ref, seq, step, state, assignee, since, stuck, unstaffed, vars, loops, had, log
		  FROM workflow_runs WHERE workflow_id = $1 ORDER BY seq`, workflowID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var (
			r                      Run
			id                     uuid.UUID
			seq                    int64
			state                  string
			vars, loops, had, logs []byte
		)
		if err := rows.Scan(&id, &r.Case, &seq, &r.Step, &state, &r.Assignee, &r.Since, &r.Stuck, &r.Unstaffed, &vars, &loops, &had, &logs); err != nil {
			return nil, err
		}
		r.ID, r.State = id.String(), State(state)
		for _, part := range []struct {
			src []byte
			dst any
		}{{vars, &r.Vars}, {loops, &r.Loops}, {had, &r.Had}, {logs, &r.Log}} {
			if err := json.Unmarshal(part.src, part.dst); err != nil {
				return nil, fmt.Errorf("workflow run %s: %w", r.ID, err)
			}
		}
		out = append(out, r.WithSeq(int(seq)))
	}
	return out, rows.Err()
}

// TriggerState is what a workflow's trigger remembers between firings.
type TriggerState struct {
	LastFired *time.Time
	LastSig   string
	HasToken  bool
}

// Trigger reads a workflow's trigger state; a workflow that never fired has
// the zero state.
func (s *Store) Trigger(ctx context.Context, workflowID uuid.UUID) (TriggerState, error) {
	var t TriggerState
	err := s.pool.QueryRow(ctx, `SELECT last_fired_at, last_work_sig, token_hash IS NOT NULL FROM workflow_triggers WHERE workflow_id = $1`, workflowID).
		Scan(&t.LastFired, &t.LastSig, &t.HasToken)
	if errors.Is(err, pgx.ErrNoRows) {
		return TriggerState{}, nil
	}
	return t, err
}

// Fired records that the schedule fired at, with the gate's signature then.
func (s *Store) Fired(ctx context.Context, workflowID uuid.UUID, at time.Time, sig string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workflow_triggers (workflow_id, last_fired_at, last_work_sig) VALUES ($1, $2, $3)
		ON CONFLICT (workflow_id) DO UPDATE SET last_fired_at = EXCLUDED.last_fired_at, last_work_sig = EXCLUDED.last_work_sig`,
		workflowID, at, sig)
	return err
}

// NewToken makes a new trigger token for a workflow and returns it; the
// previous one stops working. Only its hash is kept.
func (s *Store) NewToken(ctx context.Context, workflowID uuid.UUID) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO workflow_triggers (workflow_id, token_hash) VALUES ($1, $2)
		ON CONFLICT (workflow_id) DO UPDATE SET token_hash = EXCLUDED.token_hash`, workflowID, tokenHash(token))
	return token, err
}

// Delete removes a workflow with its versions, runs and trigger state (the
// tables cascade). Whether it may go is the host's call: it knows the runs.
func (s *Store) Delete(ctx context.Context, orgID uuid.UUID, slug string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM workflows WHERE org_id = $1 AND slug = $2`, orgID, slug)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DropToken switches the token trigger off.
func (s *Store) DropToken(ctx context.Context, workflowID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE workflow_triggers SET token_hash = NULL WHERE workflow_id = $1`, workflowID)
	return err
}

// ByToken finds the workflow a trigger token belongs to.
func (s *Store) ByToken(ctx context.Context, token string) (uuid.UUID, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT workflow_id FROM workflow_triggers WHERE token_hash = $1`, tokenHash(token)).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// Fresh records a delivery key and reports whether it is new — the same
// webhook_events table that keeps an agent's webhooks from being taken twice.
func (s *Store) Fresh(ctx context.Context, key, source string) (bool, error) {
	tag, err := s.pool.Exec(ctx, `INSERT INTO webhook_events (dedup_key, source) VALUES ($1, $2) ON CONFLICT (dedup_key) DO NOTHING`, key, source)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
