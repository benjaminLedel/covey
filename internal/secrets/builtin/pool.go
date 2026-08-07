// Several values under one key — the storage half.
//
// That a key can carry several values is a property of the STORE: encrypted,
// org-bound, listable, and the AAD binds each value to its slot so two of them
// cannot be swapped inside one pool. Which of the values an agent gets is
// capacity policy and lives in internal/runtimes (spec/18) — it needs what a
// run consumed, that a provider rejected a token, and which agent sits where,
// none of which a secret store has any business knowing.
package builtin

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/secrets"
)

// Value reads one exact value of an org-wide key — the resolution step of the
// capacity layer, which has already decided which slot it wants.
func (s *Store) Value(ctx context.Context, orgID uuid.UUID, key string, slot int) (string, error) {
	var nonce, ct []byte
	err := s.pool.QueryRow(ctx,
		"SELECT nonce, ciphertext FROM secrets WHERE org_id=$1 AND key=$2 AND slot=$3 AND agent_id IS NULL",
		orgID, key, slot).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", secrets.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(key, aad(orgID, nil, key, slot), nonce, ct)
}

// AddValue appends another value to an existing org-wide key. The slot is
// assigned by the store (lowest free number), not by the caller: a slot is an
// internal position, and letting it be chosen from outside would only invite
// collisions with values somebody else added in the meantime.
func (s *Store) AddValue(ctx context.Context, orgID uuid.UUID, key, value string) (int, error) {
	var (
		slot      int
		sensitive bool
	)
	// The new value inherits the key's protection level. Anything else would be
	// a trap: a key half of which is readable and half write-only.
	err := s.pool.QueryRow(ctx, `SELECT COALESCE(MAX(slot)+1, 0), COALESCE(BOOL_OR(sensitive), false)
		FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL`, orgID, key).Scan(&slot, &sensitive)
	if err != nil {
		return 0, err
	}
	if slot == 0 {
		// No row at all — the key does not exist. A second value grows out of a
		// secret; creating one here would bypass Put and its live check.
		return 0, secrets.ErrNotFound
	}
	nonce, ct, err := s.seal(aad(orgID, nil, key, slot), value)
	if err != nil {
		return 0, err
	}
	_, err = s.pool.Exec(ctx,
		`INSERT INTO secrets (org_id, key, slot, nonce, ciphertext, sensitive)
		 VALUES ($1,$2,$3,$4,$5,$6)`,
		orgID, key, slot, nonce, ct, sensitive)
	return slot, err
}

// DeleteValue removes a single value.
//
// The last value cannot go this way — that is Delete's job, which also clears
// the assignments. Otherwise a key would be left standing with no value at all:
// not gone, but unusable, and a state nothing else in the store expects.
//
// Existence is checked before the guard, and the order matters for the answer:
// asked to remove a slot that is not there, "this value does not exist" is the
// truth and "you cannot remove the last one" is a misleading accident of the
// key happening to hold exactly one.
func (s *Store) DeleteValue(ctx context.Context, orgID uuid.UUID, key string, slot int) error {
	var (
		n      int
		exists bool
	)
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*), COALESCE(BOOL_OR(slot=$3), false)
		 FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL`,
		orgID, key, slot).Scan(&n, &exists); err != nil {
		return err
	}
	if !exists {
		return secrets.ErrNotFound
	}
	if n <= 1 {
		return secrets.ErrLastValue
	}
	_, err := s.pool.Exec(ctx,
		"DELETE FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL AND slot=$3",
		orgID, key, slot)
	return err
}
