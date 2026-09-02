package runtimes

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/daemon"
)

type Store struct {
	pool    *pgxpool.Pool
	secrets SecretReader
}

func New(pool *pgxpool.Pool, secrets SecretReader) *Store {
	return &Store{pool: pool, secrets: secrets}
}

// --- Administration ---

func (s *Store) Create(ctx context.Context, orgID uuid.UUID, engine, displayName, model string) (Runtime, error) {
	if !daemon.IsRuntime(engine) {
		return Runtime{}, errors.New("unknown engine: " + engine)
	}
	r := Runtime{ID: uuid.New(), OrgID: orgID, Engine: engine, DisplayName: displayName, Model: model}
	err := s.pool.QueryRow(ctx, `INSERT INTO runtimes (id, org_id, engine, display_name, model)
		VALUES ($1,$2,$3,$4,$5) RETURNING created_at, updated_at`,
		r.ID, orgID, engine, displayName, model).Scan(&r.CreatedAt, &r.UpdatedAt)
	r.Credentials = []Credential{}
	return r, err
}

func (s *Store) Update(ctx context.Context, orgID, id uuid.UUID, displayName, model string) error {
	tag, err := s.pool.Exec(ctx, `UPDATE runtimes SET display_name=$3, model=$4, updated_at=now()
		WHERE org_id=$1 AND id=$2`, orgID, id, displayName, model)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Delete removes a runtime. Agents assigned to it lose their assignment (the
// foreign key is ON DELETE SET NULL) rather than the agents being removed —
// deleting a contract must never delete an employee.
func (s *Store) Delete(ctx context.Context, orgID, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, "DELETE FROM runtimes WHERE org_id=$1 AND id=$2", orgID, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Runtime, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, engine, display_name, model, created_at, updated_at
		FROM runtimes WHERE org_id=$1 ORDER BY display_name`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Runtime{}
	for rows.Next() {
		var r Runtime
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Engine, &r.DisplayName, &r.Model,
			&r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].Credentials, err = s.credentials(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) Get(ctx context.Context, orgID, id uuid.UUID) (Runtime, error) {
	var r Runtime
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, engine, display_name, model, created_at, updated_at
		FROM runtimes WHERE org_id=$1 AND id=$2`, orgID, id).
		Scan(&r.ID, &r.OrgID, &r.Engine, &r.DisplayName, &r.Model, &r.CreatedAt, &r.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Runtime{}, ErrNotFound
	}
	if err != nil {
		return Runtime{}, err
	}
	r.Credentials, err = s.credentials(ctx, r.ID)
	return r, err
}

func (s *Store) credentials(ctx context.Context, runtimeID uuid.UUID) ([]Credential, error) {
	rows, err := s.pool.Query(ctx, `SELECT ord, kind, secret_key, secret_slot, label,
			cooldown_until, cooldown_reason, limit_amount, limit_unit, limit_window_secs
		FROM runtime_credentials WHERE runtime_id=$1 ORDER BY ord`, runtimeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Credential{}
	for rows.Next() {
		var c Credential
		if err := rows.Scan(&c.Ord, &c.Kind, &c.SecretKey, &c.SecretSlot, &c.Label,
			&c.CooldownUntil, &c.CooldownReason,
			&c.Limit.Amount, &c.Limit.Unit, &c.Limit.WindowSecs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// AddCredential appends capacity and returns its place in the merit order. The
// kind has to be one the engine actually declares — otherwise the runtime would
// carry a credential nothing knows how to deliver.
func (s *Store) AddCredential(ctx context.Context, orgID, runtimeID uuid.UUID, kind, secretKey string, secretSlot int, label string) (int, error) {
	rt, err := s.Get(ctx, orgID, runtimeID)
	if err != nil {
		return 0, err
	}
	d, ok := daemon.Describe(rt.Engine)
	if !ok {
		return 0, errors.New("unknown engine: " + rt.Engine)
	}
	if _, ok := d.Credential(kind); !ok {
		return 0, errors.New("engine " + rt.Engine + " knows no credential of kind " + kind)
	}
	var ord int
	if err := s.pool.QueryRow(ctx,
		"SELECT COALESCE(MAX(ord)+1, 0) FROM runtime_credentials WHERE runtime_id=$1",
		runtimeID).Scan(&ord); err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO runtime_credentials
		(runtime_id, ord, kind, secret_key, secret_slot, label) VALUES ($1,$2,$3,$4,$5,$6)`,
		runtimeID, ord, kind, secretKey, secretSlot, label)
	return ord, err
}

// RemoveCredential drops one unit of capacity and the seats on it; the agents
// concerned get a new one on their next choice. A home seat pointing at it is
// cleared too — it points at capacity that no longer exists.
//
// The ords of the remaining credentials are NOT renumbered. They are referenced
// by bindings and by every booked cost entry; closing the gap would silently
// re-attribute history.
func (s *Store) RemoveCredential(ctx context.Context, orgID, runtimeID uuid.UUID, ord int) error {
	if _, err := s.Get(ctx, orgID, runtimeID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM runtime_credentials WHERE runtime_id=$1 AND ord=$2", runtimeID, ord)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := s.pool.Exec(ctx,
		"DELETE FROM runtime_bindings WHERE runtime_id=$1 AND ord=$2", runtimeID, ord); err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		"UPDATE runtime_bindings SET home_ord=NULL WHERE runtime_id=$1 AND home_ord=$2",
		runtimeID, ord)
	return err
}

// Reorder sets the merit order. Given as the ords in their new sequence.
func (s *Store) Reorder(ctx context.Context, orgID, runtimeID uuid.UUID, order []int) error {
	rt, err := s.Get(ctx, orgID, runtimeID)
	if err != nil {
		return err
	}
	if len(order) != len(rt.Credentials) {
		return errors.New("the new order has to name every credential exactly once")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	// Two passes through a temporary range: the primary key forbids two rows
	// sharing an ord even for the instant of a swap.
	for i, ord := range order {
		if _, err := tx.Exec(ctx,
			"UPDATE runtime_credentials SET ord=$3 WHERE runtime_id=$1 AND ord=$2",
			runtimeID, ord, 1000+i); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			"UPDATE runtime_bindings SET ord=$3 WHERE runtime_id=$1 AND ord=$2",
			runtimeID, ord, 1000+i); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx,
			"UPDATE runtime_bindings SET home_ord=$3 WHERE runtime_id=$1 AND home_ord=$2",
			runtimeID, ord, 1000+i); err != nil {
			return err
		}
	}
	for i := range order {
		for _, q := range []string{
			"UPDATE runtime_credentials SET ord=$3 WHERE runtime_id=$1 AND ord=$2",
			"UPDATE runtime_bindings SET ord=$3 WHERE runtime_id=$1 AND ord=$2",
			"UPDATE runtime_bindings SET home_ord=$3 WHERE runtime_id=$1 AND home_ord=$2",
		} {
			if _, err := tx.Exec(ctx, q, runtimeID, 1000+i, i); err != nil {
				return err
			}
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) SetLimit(ctx context.Context, orgID, runtimeID uuid.UUID, ord int, l Limit) error {
	if l.Unit == "" {
		l.Unit = "usd"
	}
	if l.Unit != "usd" && l.Unit != "tokens" {
		return errors.New(`limit unit must be "usd" or "tokens"`)
	}
	if _, err := s.Get(ctx, orgID, runtimeID); err != nil {
		return err
	}
	// Lifting the limit also frees a value the limit itself parked — otherwise
	// it would stay out of play over a rule that no longer exists. A cooldown
	// from a REJECTION survives: that one was not the limit's doing.
	tag, err := s.pool.Exec(ctx, `UPDATE runtime_credentials
		SET limit_amount=$3, limit_unit=$4, limit_window_secs=$5,
			cooldown_until  = CASE WHEN cooldown_reason=$6 THEN NULL ELSE cooldown_until END,
			cooldown_reason = CASE WHEN cooldown_reason=$6 THEN '' ELSE cooldown_reason END
		WHERE runtime_id=$1 AND ord=$2`,
		runtimeID, ord, l.Amount, l.Unit, l.WindowSecs, ReasonLimit)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetLabel(ctx context.Context, orgID, runtimeID uuid.UUID, ord int, label string) error {
	if _, err := s.Get(ctx, orgID, runtimeID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE runtime_credentials SET label=$3 WHERE runtime_id=$1 AND ord=$2",
		runtimeID, ord, label)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// Cooldown parks a credential, or with a zero time frees it again.
func (s *Store) Cooldown(ctx context.Context, runtimeID uuid.UUID, ord int, until time.Time, reason string) error {
	var (
		u *time.Time
		r string
	)
	if !until.IsZero() {
		u, r = &until, reason
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE runtime_credentials SET cooldown_until=$3, cooldown_reason=$4 WHERE runtime_id=$1 AND ord=$2",
		runtimeID, ord, u, r)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) Bindings(ctx context.Context, runtimeID uuid.UUID) ([]Binding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT agent_id, ord, home_ord, reason, bound_at FROM runtime_bindings
		 WHERE runtime_id=$1 ORDER BY ord, bound_at`, runtimeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Binding{}
	for rows.Next() {
		var b Binding
		if err := rows.Scan(&b.AgentID, &b.Ord, &b.HomeOrd, &b.Reason, &b.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// Assign puts an agent on a runtime.
//
// It refuses when the engine cannot carry that agent: an agent whose work
// blocks needs an engine that can resume a session (spec/03), and finding that
// out when the first customer reply comes back is far too late. The check
// belongs here, at the assignment, because that is the moment a person is
// making the decision.
func (s *Store) Assign(ctx context.Context, orgID, agentID, runtimeID uuid.UUID) error {
	rt, err := s.Get(ctx, orgID, runtimeID)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx,
		"UPDATE agents SET runtime_id=$3, runtime=$4, updated_at=now() WHERE id=$1 AND org_id=$2",
		agentID, orgID, runtimeID, rt.Engine)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	// Moving across engines orphans what the old engine owned: a parked task
	// holds ITS session id, and the seat on the old runtime means nothing here.
	_, err = s.pool.Exec(ctx,
		"DELETE FROM runtime_bindings WHERE agent_id=$1 AND runtime_id<>$2", agentID, runtimeID)
	return err
}

// CanCarryBlocking reports whether an engine can hold an agent that waits for
// an answer. Read from the engine's declaration, not guessed.
func CanCarryBlocking(engine string) bool {
	d, ok := daemon.Describe(engine)
	return ok && d.Capabilities.Resume
}

// EnsureDefault gets or creates the organisation's runtime for an engine and
// attaches whatever capacity is already deposited under the names that engine
// declares.
//
// This exists so that the SIMPLE case needs no extra step. Whoever installs
// covey deposits one token and creates one agent; being told to also create a
// seat and hang the token on it would be three steps for what is one
// decision — and the contract model only starts paying off with the second
// credential. The first one it should carry silently.
//
// Idempotent in both directions: an existing runtime keeps its name and order,
// and a secret already attached is not attached twice. So it can be called from
// anywhere the state might have just become completable — creating an agent,
// depositing a secret — without anybody tracking who called it before.
func (s *Store) EnsureDefault(ctx context.Context, orgID uuid.UUID, engine string) (Runtime, error) {
	d, ok := daemon.Describe(engine)
	if !ok {
		return Runtime{}, errors.New("unknown engine: " + engine)
	}

	list, err := s.List(ctx, orgID)
	if err != nil {
		return Runtime{}, err
	}
	var rt Runtime
	for _, r := range list {
		if r.Engine == engine {
			rt = r
			break
		}
	}
	if rt.ID == uuid.Nil {
		label := d.Label
		if label == "" {
			label = engine
		}
		if rt, err = s.Create(ctx, orgID, engine, label, ""); err != nil {
			return Runtime{}, err
		}
	}

	// Attach every declared credential the organisation has actually deposited,
	// in the engine's order of precedence — that order IS the merit order, and
	// the engine states it for exactly this reason.
	attached := map[string]bool{}
	for _, c := range rt.Credentials {
		attached[c.SecretKey+"#"+strconv.Itoa(c.SecretSlot)] = true
	}
	for _, want := range d.Credentials {
		slots, err := s.depositedSlots(ctx, orgID, want.Secret)
		if err != nil {
			return Runtime{}, err
		}
		for _, slot := range slots {
			if attached[want.Secret+"#"+strconv.Itoa(slot)] {
				continue
			}
			if _, err := s.AddCredential(ctx, orgID, rt.ID, want.Kind, want.Secret, slot, ""); err != nil {
				return Runtime{}, err
			}
		}
	}

	// And whoever has no seat at all moves in here. An agent without an
	// assignment reaches no credential (spec/18) and fails every run at the
	// login — with the token sitting right there under Secrets. That is not a
	// decision anybody made, it is a gap, and the default is the only sensible
	// place to close it.
	//
	// Deliberately only agents WITHOUT an assignment: whoever was put on a
	// different contract by hand made a commercial decision, and this function
	// does not overrule it. That is also what makes the call repeatable — it
	// takes in orphans and touches nothing else.
	if _, err := s.pool.Exec(ctx, `UPDATE agents SET runtime_id=$3, updated_at=now()
		WHERE org_id=$1 AND runtime=$2 AND runtime_id IS NULL`, orgID, engine, rt.ID); err != nil {
		return Runtime{}, err
	}
	return s.Get(ctx, orgID, rt.ID)
}

// depositedSlots lists the values an org-wide secret actually holds. Reading
// the secrets table directly is deliberate: the capacity layer needs to know
// WHICH values exist in order to offer them, and asking the store for each
// possible slot would be a guess loop.
func (s *Store) depositedSlots(ctx context.Context, orgID uuid.UUID, key string) ([]int, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT slot FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL ORDER BY slot",
		orgID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int
	for rows.Next() {
		var slot int
		if err := rows.Scan(&slot); err != nil {
			return nil, err
		}
		out = append(out, slot)
	}
	return out, rows.Err()
}
