package runtimes

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/daemon"
)

// Pick chooses the credential an agent runs on for one waking phase.
//
// Three rules, in this order, and each is there for a reason that costs money
// if it is dropped:
//
//  1. STICKY. An agent keeps its credential as long as that one is healthy. The
//     engine caches its prompt prefix per credential, so a value swapped at
//     every wake throws the cache away each time — and on a target system the
//     token is a visible identity, so rotating it makes the trail unreadable.
//
//  2. MERIT ORDER between unlike capacity. A subscription seat is paid for
//     already; unused quota is wasted money, so it is filled before anything
//     metered is touched. Metered capacity covers the peak, like the expensive
//     plant in a power grid.
//
//  3. SPREAD within like capacity. Three subscription seats are
//     interchangeable and each has its own window. Stacking agents onto the
//     first one would make it hit its limit while the others idle, and every
//     agent that then dodges loses its cache. So among equals the least loaded
//     one wins — at equal load, the one fewest agents sit on.
//
// Both signals are optional; with neither, only cooldowns apply.
func (s *Store) Pick(ctx context.Context, orgID, agentID, runtimeID uuid.UUID, sig Signals) (Picked, error) {
	rt, err := s.Get(ctx, orgID, runtimeID)
	if err != nil {
		return Picked{}, err
	}
	d, ok := daemon.Describe(rt.Engine)
	if !ok {
		return Picked{}, errors.New("unknown engine: " + rt.Engine)
	}
	if len(rt.Credentials) == 0 {
		if d.NeedsCredential() {
			return Picked{}, ErrNotFound
		}
		return Picked{RuntimeID: rt.ID, Ord: -1}, nil // the mock needs none
	}

	now := time.Now()
	healthy := make([]Credential, 0, len(rt.Credentials))
	load := map[int]float64{}
	var freeAt time.Time
	note := func(t time.Time) {
		if !t.IsZero() && (freeAt.IsZero() || t.Before(freeAt)) {
			freeAt = t
		}
	}
	for _, c := range rt.Credentials {
		if c.parked(now) {
			note(*c.CooldownUntil)
			continue
		}
		// What the PROVIDER says beats what we inferred, and it applies without
		// a configured limit: an exhausted seat is a fact, not a policy. This
		// is the difference between an agent that moves to the free seat and
		// one that walks into the same wall every fifteen minutes.
		//
		// A stale figure is deliberately NOT acted on. It is up to an hour old
		// (the engine serves it from its own cache when the provider's endpoint
		// is rate limited), and a window that has since reset would take a
		// working seat out of play.
		if sig.Reported != nil {
			if pct, ok := sig.Reported(rt.ID, c.Ord); ok && pct >= reportedFull {
				// We do not know WHEN it frees up — the message states a reset
				// time, but reading it is a guess we decline to make. The
				// caller then falls back on its own interval.
				continue
			}
		}
		if sig.Usage != nil && c.Limit.Active() {
			window := time.Duration(c.Limit.WindowSecs) * time.Second
			usd, tokens, uerr := sig.Usage(ctx, rt.ID, c.Ord, window)
			if uerr != nil {
				// Fail-open: a broken measurement must not stall the fleet,
				// which is exactly what a limit assumed to be reached would do.
				healthy = append(healthy, c)
				continue
			}
			used := usd
			if c.Limit.Unit == "tokens" {
				used = float64(tokens)
			}
			if used >= c.Limit.Amount {
				note(now.Add(window)) // the window rolls; this is the honest bound
				continue
			}
			load[c.Ord] = used / c.Limit.Amount
		}
		healthy = append(healthy, c)
	}
	if len(healthy) == 0 {
		return Picked{}, &Exhausted{Runtime: rt.DisplayName, Until: freeAt}
	}

	resolve := func(c Credential) (Picked, error) {
		cred, ok := d.Credential(c.Kind)
		if !ok {
			return Picked{}, errors.New("engine " + rt.Engine + " knows no credential of kind " + c.Kind)
		}
		value, err := s.secrets.Value(ctx, orgID, c.SecretKey, c.SecretSlot)
		if err != nil {
			return Picked{}, err
		}
		return Picked{RuntimeID: rt.ID, Ord: c.Ord, Kind: c.Kind, Label: c.Label,
			Value: value, EnvVar: cred.EnvVar, Path: cred.Path}, nil
	}
	find := func(ord int) (Credential, bool) {
		for _, c := range healthy {
			if c.Ord == ord {
				return c, true
			}
		}
		return Credential{}, false
	}

	// A single credential is the common case; there is no choice to record, so
	// nothing is written on what is otherwise a read path.
	if len(rt.Credentials) == 1 {
		return resolve(healthy[0])
	}

	bound, home, err := s.binding(ctx, runtimeID, agentID)
	if err != nil {
		return Picked{}, err
	}
	if bound != nil {
		// Standing in somewhere and the HOME seat is healthy again: go back.
		// Checked BEFORE the current seat, because a stand-in seat is usually
		// healthy too and the agent would otherwise never return.
		if home != nil {
			if c, ok := find(*home); ok {
				if err := s.bind(ctx, runtimeID, agentID, c.Ord, nil, ReasonReturn); err != nil {
					return Picked{}, err
				}
				return resolve(c)
			}
		}
		if c, ok := find(*bound); ok {
			return resolve(c)
		}
	}

	seats, err := s.seatCount(ctx, runtimeID)
	if err != nil {
		return Picked{}, err
	}
	best := bestOf(healthy, load, seats)

	reason := ReasonInitial
	var newHome *int
	if bound != nil {
		// Why it moved is worth keeping apart: a credential used up by its
		// limit is normal operation, one rejected by the provider is a fault
		// somebody has to look at.
		reason = ReasonLimit
		for _, c := range rt.Credentials {
			if c.Ord == *bound && c.CooldownReason == ReasonError {
				reason = ReasonError
			}
		}
		newHome = bound
		if home != nil {
			newHome = home // already standing in — the home seat stays the old one
		}
	}
	if err := s.bind(ctx, runtimeID, agentID, best.Ord, newHome, reason); err != nil {
		return Picked{}, err
	}
	return resolve(best)
}

// bestOf applies rules 2 and 3: the cheapest class that still has healthy
// capacity, and within it the least loaded credential.
//
// "Cheapest" is not a price but a property of the kind — quota is paid for
// either way, so using it costs nothing on the margin, while every metered
// token does. Comparing actual prices would need a price list here and would
// still answer the same question.
func bestOf(healthy []Credential, load map[int]float64, seats map[int]int) Credential {
	quota := make([]Credential, 0, len(healthy))
	for _, c := range healthy {
		if c.Kind != daemon.CredAPIKey {
			quota = append(quota, c)
		}
	}
	pool := quota
	if len(pool) == 0 {
		pool = healthy
	}
	best := pool[0]
	for _, c := range pool[1:] {
		switch {
		case load[c.Ord] < load[best.Ord]:
			best = c
		case load[c.Ord] == load[best.Ord] && seats[c.Ord] < seats[best.Ord]:
			best = c
		case load[c.Ord] == load[best.Ord] && seats[c.Ord] == seats[best.Ord] && c.Ord < best.Ord:
			best = c
		}
	}
	return best
}

func (s *Store) seatCount(ctx context.Context, runtimeID uuid.UUID) (map[int]int, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT ord, COUNT(*) FROM runtime_bindings WHERE runtime_id=$1 GROUP BY ord", runtimeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var ord, n int
		if err := rows.Scan(&ord, &n); err != nil {
			return nil, err
		}
		out[ord] = n
	}
	return out, rows.Err()
}

func (s *Store) binding(ctx context.Context, runtimeID, agentID uuid.UUID) (ord, home *int, err error) {
	err = s.pool.QueryRow(ctx,
		"SELECT ord, home_ord FROM runtime_bindings WHERE runtime_id=$1 AND agent_id=$2",
		runtimeID, agentID).Scan(&ord, &home)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil, nil
	}
	return ord, home, err
}

func (s *Store) bind(ctx context.Context, runtimeID, agentID uuid.UUID, ord int, home *int, reason string) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO runtime_bindings (runtime_id, agent_id, ord, home_ord, reason)
		VALUES ($1,$2,$3,$4,$5)
		ON CONFLICT (runtime_id, agent_id)
		DO UPDATE SET ord=$3, home_ord=$4, reason=$5, bound_at=now()`,
		runtimeID, agentID, ord, home, reason)
	return err
}
