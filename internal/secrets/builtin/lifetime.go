package builtin

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"covey/internal/secrets"
)

// The life of a value: what the platform learns about a stored credential
// beyond the value itself (see secrets.Lifetime). The columns are read and
// written in one order everywhere here, so that a column added later is added
// in one place and missed nowhere.

const lifetimeColumns = `expires_at, rejected_at, rejected_reason, probed_at, probe_error,
	probe_identity, credential_id, rotatable, warned_at`

// lifetimeReset is what a new value starts with: nothing known. Used by every
// write that replaces a value — the state of the old one would be a lie
// beside the new.
const lifetimeReset = `expires_at=NULL, rejected_at=NULL, rejected_reason='', probed_at=NULL,
	probe_error='', probe_identity='', credential_id='', rotatable=false, warned_at=NULL`

// lifetimeDest lines up the scan targets in the column order above.
func lifetimeDest(l *secrets.Lifetime) []any {
	return []any{&l.ExpiresAt, &l.RejectedAt, &l.RejectedReason, &l.ProbedAt, &l.ProbeError,
		&l.ProbeIdentity, &l.CredentialID, &l.Rotatable, &l.WarnedAt}
}

// where addresses one row by its Ref. Org-wide rows have no agent, and an
// agent's own carry theirs — the two partial unique indexes see to it that
// this names at most one row.
const refWhere = `org_id=$1 AND key=$2 AND slot=$3 AND agent_id IS NOT DISTINCT FROM $4`

func refArgs(ref secrets.Ref, more ...any) []any {
	return append([]any{ref.OrgID, ref.Key, ref.Slot, ref.AgentID}, more...)
}

func (s *Store) Lookup(ctx context.Context, orgID, agentID uuid.UUID, key string) (secrets.Stored, error) {
	st := secrets.Stored{Ref: secrets.Ref{OrgID: orgID, Key: key}}
	dest := append([]any{&st.AgentID, &st.Slot, &st.Sensitive}, lifetimeDest(&st.Lifetime)...)
	// The same choice Resolve makes — the agent's own before the assigned
	// org-wide one, the lowest slot — so that what is reported is what the
	// run got.
	err := s.pool.QueryRow(ctx, `SELECT agent_id, slot, sensitive, `+lifetimeColumns+` FROM secrets s
		WHERE s.org_id=$1 AND s.key=$2 AND (
			s.agent_id=$3
			OR (s.agent_id IS NULL AND EXISTS (SELECT 1 FROM secret_assignments a
				WHERE a.org_id=$1 AND a.key=$2 AND a.agent_id=$3)))
		ORDER BY s.agent_id NULLS LAST, s.slot LIMIT 1`, orgID, key, agentID).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return secrets.Stored{}, secrets.ErrNotFound
	}
	return st, err
}

func (s *Store) List(ctx context.Context, orgID uuid.UUID, suffix string) ([]secrets.Stored, error) {
	var org *uuid.UUID
	if orgID != uuid.Nil {
		org = &orgID
	}
	// LIKE narrows the rows; the exact check happens below, because "_" is a
	// wildcard to LIKE and the suffix in question is "_token".
	rows, err := s.pool.Query(ctx, `SELECT org_id, agent_id, key, slot, sensitive, `+lifetimeColumns+`
		FROM secrets WHERE ($1::uuid IS NULL OR org_id=$1) AND key LIKE '%' || $2
		ORDER BY org_id, key, agent_id NULLS FIRST, slot`, org, suffix)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secrets.Stored
	for rows.Next() {
		var st secrets.Stored
		dest := append([]any{&st.OrgID, &st.AgentID, &st.Key, &st.Slot, &st.Sensitive}, lifetimeDest(&st.Lifetime)...)
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		if strings.HasSuffix(st.Key, suffix) {
			out = append(out, st)
		}
	}
	return out, rows.Err()
}

func (s *Store) Open(ctx context.Context, ref secrets.Ref) (string, error) {
	var nonce, ct []byte
	err := s.pool.QueryRow(ctx, `SELECT nonce, ciphertext FROM secrets WHERE `+refWhere, refArgs(ref)...).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", secrets.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(ref.Key, aad(ref.OrgID, ref.AgentID, ref.Key, ref.Slot), nonce, ct)
}

func (s *Store) Replace(ctx context.Context, ref secrets.Ref, value string, lt secrets.Lifetime) error {
	nonce, ct, err := s.seal(aad(ref.OrgID, ref.AgentID, ref.Key, ref.Slot), value)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE secrets SET nonce=$5, ciphertext=$6, updated_at=now(),
		expires_at=$7, rejected_at=NULL, rejected_reason='', probed_at=NULL, probe_error='',
		probe_identity='', credential_id=$8, rotatable=$9, warned_at=NULL
		WHERE `+refWhere, refArgs(ref, nonce, ct, lt.ExpiresAt, lt.CredentialID, lt.Rotatable)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) RecordProbe(ctx context.Context, ref secrets.Ref, p secrets.Probe) error {
	var tag pgconn.CommandTag
	var err error
	if p.Err == "" {
		// A credential that works is not rejected, whatever it was before —
		// and whoever was warned about THAT has nothing left to do. A warning
		// about the expiry stands: the date has not moved.
		tag, err = s.pool.Exec(ctx, `UPDATE secrets SET probed_at=$5, probe_error='', probe_identity=$6,
			warned_at=CASE WHEN rejected_at IS NULL THEN warned_at ELSE NULL END,
			rejected_at=NULL, rejected_reason='',
			expires_at=COALESCE($7, expires_at),
			credential_id=CASE WHEN $8 <> '' THEN $8 ELSE credential_id END,
			rotatable=$9
			WHERE `+refWhere, refArgs(ref, p.At, p.Identity, p.ExpiresAt, p.CredentialID, p.Rotatable)...)
	} else {
		// A failed probe says what failed; only a rejection says the
		// credential is dead. The first rejection keeps its date — that is the
		// moment somebody will want to know about.
		tag, err = s.pool.Exec(ctx, `UPDATE secrets SET probed_at=$5, probe_error=$6,
			rejected_at=CASE WHEN $7 THEN COALESCE(rejected_at, $5) ELSE rejected_at END,
			rejected_reason=CASE WHEN $7 THEN $6 ELSE rejected_reason END
			WHERE `+refWhere, refArgs(ref, p.At, p.Err, p.Rejected)...)
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) MarkRejected(ctx context.Context, orgID, agentID uuid.UUID, key, reason string) (secrets.Stored, bool, error) {
	st, err := s.Lookup(ctx, orgID, agentID, key)
	if err != nil {
		return secrets.Stored{}, false, err
	}
	news := st.RejectedAt == nil
	now := time.Now()
	_, err = s.pool.Exec(ctx, `UPDATE secrets SET rejected_at=COALESCE(rejected_at, $5), rejected_reason=$6
		WHERE `+refWhere, refArgs(st.Ref, now, reason)...)
	if err != nil {
		return secrets.Stored{}, false, err
	}
	if news {
		st.RejectedAt = &now
	}
	st.RejectedReason = reason
	return st, news, nil
}

func (s *Store) SetExpiry(ctx context.Context, ref secrets.Ref, at *time.Time) error {
	// A new date is a new state: whoever was warned about the old one has
	// not been told about this one.
	tag, err := s.pool.Exec(ctx, `UPDATE secrets SET expires_at=$5, warned_at=NULL WHERE `+refWhere, refArgs(ref, at)...)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) MarkWarned(ctx context.Context, ref secrets.Ref) error {
	_, err := s.pool.Exec(ctx, `UPDATE secrets SET warned_at=now() WHERE `+refWhere, refArgs(ref)...)
	return err
}
