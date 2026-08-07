// Pools: several values under one secret key, and the rule by which an agent
// gets one of them.
//
// The whole point of the rule is that it is STICKY. An agent keeps its value as
// long as that value is healthy — that keeps the prompt cache warm (Claude Code
// caches the prompt prefix per credential) and its identity in the target
// system stable (a comment appears under the bot account whose token wrote it).
// It moves only for a reason, and the reason is recorded.
package builtin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/secrets"
)

// candidate is one value of a pool during the choice.
type candidate struct {
	slot           int
	label          string
	nonce          []byte
	ct             []byte
	cooldown       *time.Time
	cooldownReason string
	limit          secrets.Limit
}

// Pick — see the port documentation. The order:
//
//  1. an agent-owned secret beats the pool (that agent does not take part in
//     the distribution at all),
//  2. an existing binding, if its value is healthy — the sticky normal case and
//     nothing but an index lookup,
//  3. otherwise the least loaded healthy value, and the move is recorded,
//  4. nothing healthy → ErrPoolExhausted, with the moment it frees up again.
func (s *Store) Pick(ctx context.Context, orgID, agentID uuid.UUID, key string, usage secrets.UsageFunc) (secrets.Value, error) {
	// 1. The agent's own secret. Slot 0 — PutAgent writes nothing else, and an
	// agent that brings its own credential needs no distribution.
	var (
		nonce, ct []byte
		label     string
	)
	err := s.pool.QueryRow(ctx,
		"SELECT nonce, ciphertext, label FROM secrets WHERE org_id=$1 AND agent_id=$2 AND key=$3 AND slot=0",
		orgID, agentID, key).Scan(&nonce, &ct, &label)
	if err == nil {
		plain, oerr := s.open(key, aad(orgID, &agentID, key, 0), nonce, ct)
		return secrets.Value{Value: plain, Slot: 0, Label: label}, oerr
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return secrets.Value{}, err
	}

	cands, err := s.candidates(ctx, orgID, agentID, key)
	if err != nil {
		return secrets.Value{}, err
	}
	if len(cands) == 0 {
		return secrets.Value{}, secrets.ErrNotFound
	}

	// Health check. A value is unusable if it is parked (hard signal) or has
	// used up its limit in the current window (soft signal). Both end up in the
	// same place, but only the hard one is a measurement — hence the order:
	// whoever is parked is not even measured.
	now := time.Now()
	healthy := make([]candidate, 0, len(cands))
	var freeAt time.Time // earliest moment any value becomes usable again
	note := func(t time.Time) {
		if !t.IsZero() && (freeAt.IsZero() || t.Before(freeAt)) {
			freeAt = t
		}
	}
	load := map[int]float64{} // slot → share of its limit already used
	for _, c := range cands {
		if c.cooldown != nil && c.cooldown.After(now) {
			note(*c.cooldown)
			continue
		}
		if usage != nil && c.limit.Active() {
			window := time.Duration(c.limit.WindowSecs) * time.Second
			usd, tokens, uerr := usage(ctx, orgID, key, c.slot, window)
			if uerr != nil {
				// The measurement failed — do not park the value for that.
				// Fail-open on purpose: a broken query must not stall the
				// fleet, which is exactly what a limit assumed to be reached
				// would do.
				healthy = append(healthy, c)
				continue
			}
			used := usd
			if c.limit.Unit == "tokens" {
				used = float64(tokens)
			}
			if used >= c.limit.Amount {
				// The window is rolling, so it frees up gradually; without the
				// individual entries the closest honest statement is "when the
				// current window has passed".
				note(now.Add(window))
				continue
			}
			load[c.slot] = used / c.limit.Amount
		}
		healthy = append(healthy, c)
	}
	if len(healthy) == 0 {
		return secrets.Value{}, &secrets.PoolExhausted{Key: key, Until: freeAt}
	}
	// A key with a single value is the normal case — for a target-system token
	// it is practically always the case, and Resolve runs through here on every
	// brokered credential. There is no choice to record then, so nothing is
	// written: no binding row, no write amplification on a path that is only
	// reading.
	if len(cands) == 1 {
		return s.value(orgID, key, healthy[0])
	}

	pick := func(slot int) (candidate, bool) {
		for _, c := range healthy {
			if c.slot == slot {
				return c, true
			}
		}
		return candidate{}, false
	}

	// 2. The seat the agent already has.
	bound, home, err := s.binding(ctx, orgID, agentID, key)
	if err != nil {
		return secrets.Value{}, err
	}
	if bound != nil {
		// Standing in somewhere and the HOME seat is healthy again: go back.
		// Checked BEFORE the current seat — otherwise an agent that dodged once
		// would stay away for good, since its stand-in seat is usually healthy
		// too. home_slot is only ever set while standing in, and the return
		// clears it, so this cannot flap.
		if home != nil {
			if c, ok := pick(*home); ok {
				if err := s.bind(ctx, orgID, agentID, key, c.slot, nil, secrets.ReasonReturn); err != nil {
					return secrets.Value{}, err
				}
				return s.value(orgID, key, c)
			}
		}
		if c, ok := pick(*bound); ok {
			return s.value(orgID, key, c)
		}
	}

	// 3. The least loaded healthy value, in two stages.
	//
	// First the share of its own limit — comparing absolute consumption would
	// push everything onto the value with the largest limit. Then, at equal
	// share, the number of agents already sitting on it. The second stage is
	// what actually distributes in the common case: without configured limits
	// every share is zero, and without the head count the first value would win
	// every time and the whole pool would sit on one seat.
	seats, err := s.seatCount(ctx, orgID, key)
	if err != nil {
		return secrets.Value{}, err
	}
	best := healthy[0]
	for _, c := range healthy[1:] {
		switch {
		case load[c.slot] < load[best.slot]:
			best = c
		case load[c.slot] == load[best.slot] && seats[c.slot] < seats[best.slot]:
			best = c
		}
	}
	// Whoever had a seat keeps it as its home, so it can return. Whoever is
	// arriving for the first time makes this one its home.
	reason := secrets.ReasonInitial
	var newHome *int
	if bound != nil {
		// Why it is moving is worth distinguishing: a value used up by the
		// limit is normal operation, a value rejected by the target system is a
		// fault somebody has to look at. Both stand in the pool view.
		reason = secrets.ReasonLimit
		for _, c := range cands {
			if c.slot == *bound && c.cooldownReason == secrets.ReasonError {
				reason = secrets.ReasonError
			}
		}
		newHome = bound
		if home != nil {
			newHome = home // already standing in — the home seat stays the old one
		}
	}
	if err := s.bind(ctx, orgID, agentID, key, best.slot, newHome, reason); err != nil {
		return secrets.Value{}, err
	}
	return s.value(orgID, key, best)
}

// candidates loads the org-wide values of a key that this agent may use at all.
// The explicit assignment applies unchanged at KEY level (spec/04): without it
// an org secret reaches no agent — a pool changes nothing about that.
func (s *Store) candidates(ctx context.Context, orgID, agentID uuid.UUID, key string) ([]candidate, error) {
	rows, err := s.pool.Query(ctx, `SELECT slot, label, nonce, ciphertext,
			cooldown_until, cooldown_reason, limit_amount, limit_unit, limit_window_secs
		FROM secrets s
		WHERE s.org_id=$1 AND s.key=$2 AND s.agent_id IS NULL
			AND EXISTS (SELECT 1 FROM secret_assignments a
				WHERE a.org_id=$1 AND a.key=$2 AND a.agent_id=$3)
		ORDER BY slot`, orgID, key, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.slot, &c.label, &c.nonce, &c.ct, &c.cooldown, &c.cooldownReason,
			&c.limit.Amount, &c.limit.Unit, &c.limit.WindowSecs); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) value(orgID uuid.UUID, key string, c candidate) (secrets.Value, error) {
	plain, err := s.open(key, aad(orgID, nil, key, c.slot), c.nonce, c.ct)
	if err != nil {
		return secrets.Value{}, err
	}
	return secrets.Value{Value: plain, Slot: c.slot, Label: c.label}, nil
}

// seatCount is how many agents already sit on each value — the tiebreak that
// distributes a pool without configured limits.
func (s *Store) seatCount(ctx context.Context, orgID uuid.UUID, key string) (map[int]int, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT slot, COUNT(*) FROM secret_bindings WHERE org_id=$1 AND key=$2 GROUP BY slot",
		orgID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var slot, n int
		if err := rows.Scan(&slot, &n); err != nil {
			return nil, err
		}
		out[slot] = n
	}
	return out, rows.Err()
}

func (s *Store) binding(ctx context.Context, orgID, agentID uuid.UUID, key string) (slot, home *int, err error) {
	err = s.pool.QueryRow(ctx,
		"SELECT slot, home_slot FROM secret_bindings WHERE org_id=$1 AND key=$2 AND agent_id=$3",
		orgID, key, agentID).Scan(&slot, &home)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	return slot, home, err
}

func (s *Store) bind(ctx context.Context, orgID, agentID uuid.UUID, key string, slot int, home *int, reason string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO secret_bindings (org_id, key, agent_id, slot, home_slot, reason)
		VALUES ($1,$2,$3,$4,$5,$6)
		ON CONFLICT (org_id, key, agent_id)
		DO UPDATE SET slot=$4, home_slot=$5, reason=$6, bound_at=now()`,
		orgID, key, agentID, slot, home, reason)
	return err
}

// --- Pool administration ---

// AddValue appends another value to an existing org-wide key. The slot is
// assigned by the store (lowest free number), not by the caller: a slot is an
// internal position, and letting it be chosen from outside would only invite
// collisions with values somebody else added in the meantime.
func (s *Store) AddValue(ctx context.Context, orgID uuid.UUID, key, value, label string) (int, error) {
	var (
		slot      int
		sensitive bool
	)
	// The new value inherits the key's protection level. Anything else would be
	// a trap: a pool half of which is readable and half write-only.
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(slot)+1, 0), COALESCE(BOOL_OR(sensitive), false)
		FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL`, orgID, key).Scan(&slot, &sensitive)
	if err != nil {
		return 0, err
	}
	if slot == 0 {
		// No row at all — the key does not exist. A pool grows out of a secret;
		// creating one here would bypass Put and its live check.
		return 0, secrets.ErrNotFound
	}
	nonce, ct, err := s.seal(aad(orgID, nil, key, slot), value)
	if err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO secrets (org_id, key, slot, label, nonce, ciphertext, sensitive)
		 VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		orgID, key, slot, label, nonce, ct, sensitive)
	return slot, err
}

// DeleteValue removes a single value out of the pool.
//
// The last value cannot go this way — that is Delete's job, which also clears
// assignments and bindings. Otherwise a key would be left standing with no
// value at all: not gone, but unusable, and a state nothing else in the store
// expects.
//
// Bindings to this value are dropped along with it; the agents concerned get a
// new seat on their next choice. Their home seat is deliberately dropped too —
// it points at a value that no longer exists.
func (s *Store) DeleteValue(ctx context.Context, orgID uuid.UUID, key string, slot int) error {
	var n int
	if err := s.pool.QueryRow(ctx,
		"SELECT COUNT(*) FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL",
		orgID, key).Scan(&n); err != nil {
		return err
	}
	if n <= 1 {
		return secrets.ErrLastValue
	}
	tag, err := s.pool.Exec(ctx,
		"DELETE FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL AND slot=$3",
		orgID, key, slot)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	_, err = s.pool.Exec(ctx,
		"DELETE FROM secret_bindings WHERE org_id=$1 AND key=$2 AND slot=$3", orgID, key, slot)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx,
		"UPDATE secret_bindings SET home_slot=NULL WHERE org_id=$1 AND key=$2 AND home_slot=$3",
		orgID, key, slot)
	return err
}

// SetLimit sets the cap of a single value; secrets.Limit{} lifts it. Lifting it
// also clears a cooldown that the limit itself had imposed — otherwise the
// value would stay parked over a rule that no longer exists.
func (s *Store) SetLimit(ctx context.Context, orgID uuid.UUID, key string, slot int, l secrets.Limit) error {
	if l.Unit == "" {
		l.Unit = "usd"
	}
	if l.Unit != "usd" && l.Unit != "tokens" {
		return errors.New(`limit unit must be "usd" or "tokens"`)
	}
	tag, err := s.pool.Exec(ctx, `UPDATE secrets
		SET limit_amount=$4, limit_unit=$5, limit_window_secs=$6,
			cooldown_until = CASE WHEN cooldown_reason='limit' THEN NULL ELSE cooldown_until END,
			cooldown_reason = CASE WHEN cooldown_reason='limit' THEN '' ELSE cooldown_reason END
		WHERE org_id=$1 AND key=$2 AND agent_id IS NULL AND slot=$3`,
		orgID, key, slot, l.Amount, l.Unit, l.WindowSecs)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) SetLabel(ctx context.Context, orgID uuid.UUID, key string, slot int, label string) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE secrets SET label=$4 WHERE org_id=$1 AND key=$2 AND agent_id IS NULL AND slot=$3",
		orgID, key, slot, label)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

// Cooldown parks a value, or with a zero time frees it again.
func (s *Store) Cooldown(ctx context.Context, orgID uuid.UUID, key string, slot int, until time.Time, reason string) error {
	var (
		u *time.Time
		r string
	)
	if !until.IsZero() {
		u, r = &until, reason
	}
	tag, err := s.pool.Exec(ctx, `UPDATE secrets SET cooldown_until=$4, cooldown_reason=$5
		WHERE org_id=$1 AND key=$2 AND agent_id IS NULL AND slot=$3`,
		orgID, key, slot, u, r)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) Bindings(ctx context.Context, orgID uuid.UUID, key string) ([]secrets.Binding, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT agent_id, slot, home_slot, reason, bound_at FROM secret_bindings
		 WHERE org_id=$1 AND key=$2 ORDER BY slot, bound_at`, orgID, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []secrets.Binding{}
	for rows.Next() {
		var b secrets.Binding
		if err := rows.Scan(&b.AgentID, &b.Slot, &b.HomeSlot, &b.Reason, &b.BoundAt); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}
