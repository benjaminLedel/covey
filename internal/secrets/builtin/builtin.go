// Package builtin is the DB-backed SecretStore: AES-256-GCM encrypted column in
// Postgres, master key from ENV (spec/10). Fully covers "store a legacy API
// token and hand it through short-lived".
package builtin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/secrets"
)

type Store struct {
	pool *pgxpool.Pool
	aead cipher.AEAD
}

// New expects the master key as 64 hex characters (32 bytes → AES-256).
func New(pool *pgxpool.Pool, masterKeyHex string) (*Store, error) {
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("COVEY_MASTER_KEY must be 32 bytes of hex (64 characters)")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool, aead: aead}, nil
}

// GenerateMasterKey creates a new master key for bootstrapping.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// aad binds the ciphertext to its place (no row swapping): org+key for org-wide
// secrets, org+agent+key for an agent's own — plus the slot, since a key can
// hold several values and swapping two of them within one pool would otherwise
// go unnoticed.
//
// Slot 0 stays without a suffix on purpose: everything written before the pools
// is slot 0, and its AAD has to keep matching, otherwise every existing secret
// would become undecryptable in one migration.
func aad(orgID uuid.UUID, agentID *uuid.UUID, key string, slot int) []byte {
	base := orgID.String() + "/" + key
	if agentID != nil {
		base = orgID.String() + "/" + agentID.String() + "/" + key
	}
	if slot != 0 {
		base += "#" + strconv.Itoa(slot)
	}
	return []byte(base)
}

func (s *Store) seal(aad []byte, value string) (nonce, ciphertext []byte, err error) {
	nonce = make([]byte, s.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	return nonce, s.aead.Seal(nil, nonce, []byte(value), aad), nil
}

func (s *Store) open(key string, aad, nonce, ciphertext []byte) (string, error) {
	plain, err := s.aead.Open(nil, nonce, ciphertext, aad)
	if err != nil {
		return "", fmt.Errorf("secret %q: decryption failed: %w", key, err)
	}
	return string(plain), nil
}

// Put writes the key's FIRST value (slot 0) — creating the secret and, for a
// key that already carries a pool, correcting its first value. Further values
// come from AddValue; overwriting a whole pool from a single Put would silently
// destroy values nobody named here.
func (s *Store) Put(ctx context.Context, orgID uuid.UUID, key, value string) error {
	nonce, ct, err := s.seal(aad(orgID, nil, key, 0), value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO secrets (org_id, key, slot, nonce, ciphertext)
		VALUES ($1,$2,0,$3,$4)
		ON CONFLICT (org_id, key, slot) WHERE agent_id IS NULL
		DO UPDATE SET nonce=$3, ciphertext=$4, updated_at=now()`,
		orgID, key, nonce, ct)
	return err
}

// PutAgent creates an agent's own secret. The agent must belong to the org —
// otherwise ErrNotFound (no cross-org writing via guessed agent IDs).
func (s *Store) PutAgent(ctx context.Context, orgID, agentID uuid.UUID, key, value string) error {
	nonce, ct, err := s.seal(aad(orgID, &agentID, key, 0), value)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO secrets (org_id, agent_id, key, slot, nonce, ciphertext)
		SELECT $1, $2, $3, 0, $4, $5
		WHERE EXISTS (SELECT 1 FROM agents WHERE id=$2 AND org_id=$1)
		ON CONFLICT (org_id, agent_id, key, slot) WHERE agent_id IS NOT NULL
		DO UPDATE SET nonce=$4, ciphertext=$5, updated_at=now()`,
		orgID, agentID, key, nonce, ct)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

// Get reads an org-wide secret without an agent context (bootstrap, webhooks,
// the org's own LLM calls). Out of several values it takes the lowest: there is
// no agent here whose seat could be kept, and no capacity decision to make —
// this caller has no run it could postpone.
func (s *Store) Get(ctx context.Context, orgID uuid.UUID, key string) (string, error) {
	var (
		nonce, ct []byte
		slot      int
	)
	err := s.pool.QueryRow(ctx, `SELECT nonce, ciphertext, slot FROM secrets
		WHERE org_id=$1 AND key=$2 AND agent_id IS NULL
		ORDER BY slot LIMIT 1`, orgID, key).Scan(&nonce, &ct, &slot)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", secrets.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(key, aad(orgID, nil, key, slot), nonce, ct)
}

// Resolve prefers the agent's own secret; otherwise the org-wide one applies —
// but only on explicit assignment. An org secret without an assignment reaches
// no agent.
//
// Out of several values it takes the lowest. Which one an agent SHOULD get when
// there are several is a capacity decision and is made one layer up
// (internal/runtimes, spec/18); this path serves target systems, where a key
// carries exactly one value.
func (s *Store) Resolve(ctx context.Context, orgID, agentID uuid.UUID, key string) (string, error) {
	var (
		nonce, ct []byte
		rowAgent  *uuid.UUID
		slot      int
	)
	err := s.pool.QueryRow(ctx, `SELECT nonce, ciphertext, agent_id, slot FROM secrets s
		WHERE s.org_id=$1 AND s.key=$2 AND (
			s.agent_id=$3
			OR (s.agent_id IS NULL AND EXISTS (SELECT 1 FROM secret_assignments a
				WHERE a.org_id=$1 AND a.key=$2 AND a.agent_id=$3)))
		ORDER BY s.agent_id NULLS LAST, s.slot LIMIT 1`,
		orgID, key, agentID).Scan(&nonce, &ct, &rowAgent, &slot)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", secrets.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(key, aad(orgID, rowAgent, key, slot), nonce, ct)
}

func (s *Store) Delete(ctx context.Context, orgID uuid.UUID, key string) error {
	// Assignments hang off the org-wide secret — they disappear with it.
	if _, err := s.pool.Exec(ctx,
		"DELETE FROM secret_assignments WHERE org_id=$1 AND key=$2", orgID, key); err != nil {
		return err
	}
	_, err := s.pool.Exec(ctx,
		"DELETE FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL", orgID, key)
	return err
}

func (s *Store) DeleteAgent(ctx context.Context, orgID, agentID uuid.UUID, key string) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM secrets WHERE org_id=$1 AND agent_id=$2 AND key=$3", orgID, agentID, key)
	return err
}

func (s *Store) Keys(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		"SELECT key FROM secrets WHERE org_id=$1 AND agent_id IS NULL ORDER BY key", orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, err
		}
		out = append(out, k)
	}
	return out, rows.Err()
}

// Previews decrypts every value: non-sensitive secrets are variables and yield
// the full plaintext, sensitive ones only their limited prefix. A key with
// several values yields ONE entry whose Values carries the whole pool; the
// fields at the top describe its lowest value.
//
// Deliberately two queries instead of one join: the assignments hang off the
// key, the values off the slot — joined in one go, every assignment would be
// multiplied by the pool size and would have to be de-duplicated afterwards.
func (s *Store) Previews(ctx context.Context, orgID uuid.UUID) ([]secrets.KeyPreview, error) {
	assigned := map[string][]string{}
	arows, err := s.pool.Query(ctx,
		"SELECT key, agent_id::text FROM secret_assignments WHERE org_id=$1 ORDER BY created_at", orgID)
	if err != nil {
		return nil, err
	}
	for arows.Next() {
		var k, a string
		if err := arows.Scan(&k, &a); err != nil {
			arows.Close()
			return nil, err
		}
		assigned[k] = append(assigned[k], a)
	}
	arows.Close()
	if err := arows.Err(); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `SELECT key, slot, nonce, ciphertext, sensitive, updated_at
		FROM secrets WHERE org_id=$1 AND agent_id IS NULL ORDER BY key, slot`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secrets.KeyPreview
	for rows.Next() {
		var (
			k  string
			pv secrets.PoolValue
			// nonce/ciphertext stay local — nothing encrypted leaves this loop.
			nonce, ct []byte
		)
		if err := rows.Scan(&k, &pv.Slot, &nonce, &ct, &pv.Sensitive, &pv.UpdatedAt); err != nil {
			return nil, err
		}
		pv.Value, pv.Prefix = s.expose(k, aad(orgID, nil, k, pv.Slot), nonce, ct, pv.Sensitive)
		if n := len(out); n > 0 && out[n-1].Key == k {
			out[n-1].Values = append(out[n-1].Values, pv)
			continue
		}
		ids := assigned[k]
		if ids == nil {
			ids = []string{}
		}
		out = append(out, secrets.KeyPreview{
			Key: k, Sensitive: pv.Sensitive, Value: pv.Value, Prefix: pv.Prefix,
			AgentIDs: ids, Values: []secrets.PoolValue{pv},
		})
	}
	return out, rows.Err()
}

// MarkSensitive is deliberately one-way (see the port documentation): only
// false→true. It covers the whole pool — half a key being readable and the
// other half write-only would be a trap for whoever administers it.
func (s *Store) MarkSensitive(ctx context.Context, orgID uuid.UUID, key string) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE secrets SET sensitive=true WHERE org_id=$1 AND key=$2 AND agent_id IS NULL",
		orgID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) MarkAgentSensitive(ctx context.Context, orgID, agentID uuid.UUID, key string) error {
	tag, err := s.pool.Exec(ctx,
		"UPDATE secrets SET sensitive=true WHERE org_id=$1 AND agent_id=$2 AND key=$3",
		orgID, agentID, key)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return secrets.ErrNotFound
	}
	return nil
}

func (s *Store) AgentPreviews(ctx context.Context, orgID, agentID uuid.UUID) ([]secrets.KeyPreview, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT key, slot, nonce, ciphertext, sensitive, updated_at
		 FROM secrets WHERE org_id=$1 AND agent_id=$2 ORDER BY key, slot`,
		orgID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secrets.KeyPreview
	for rows.Next() {
		var (
			k         string
			pv        secrets.PoolValue
			nonce, ct []byte
		)
		if err := rows.Scan(&k, &pv.Slot, &nonce, &ct, &pv.Sensitive, &pv.UpdatedAt); err != nil {
			return nil, err
		}
		pv.Value, pv.Prefix = s.expose(k, aad(orgID, &agentID, k, pv.Slot), nonce, ct, pv.Sensitive)
		out = append(out, secrets.KeyPreview{
			Key: k, Sensitive: pv.Sensitive, Value: pv.Value, Prefix: pv.Prefix,
			AgentIDs: []string{}, Values: []secrets.PoolValue{pv},
		})
	}
	return out, rows.Err()
}

// expose decrypts best-effort for display: non-sensitive values in full,
// sensitive ones only as a prefix. A secret that cannot be decrypted (e.g. with
// an old master key) must not topple the list — then it is fully masked.
func (s *Store) expose(key string, aad, nonce, ct []byte, sensitive bool) (value, prefix string) {
	plain, err := s.open(key, aad, nonce, ct)
	if err != nil {
		return "", ""
	}
	if sensitive {
		return "", secrets.Preview(plain)
	}
	return plain, ""
}

// Assign explicitly assigns an org-wide secret to an agent. Secret and agent
// must exist and belong to the org — otherwise ErrNotFound.
func (s *Store) Assign(ctx context.Context, orgID uuid.UUID, key string, agentID uuid.UUID) error {
	var secretOK, agentOK bool
	if err := s.pool.QueryRow(ctx, `SELECT
			EXISTS (SELECT 1 FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL),
			EXISTS (SELECT 1 FROM agents WHERE id=$3 AND org_id=$1)`,
		orgID, key, agentID).Scan(&secretOK, &agentOK); err != nil {
		return err
	}
	if !secretOK || !agentOK {
		return secrets.ErrNotFound
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO secret_assignments (org_id, key, agent_id)
		VALUES ($1,$2,$3) ON CONFLICT DO NOTHING`, orgID, key, agentID)
	return err
}

func (s *Store) Unassign(ctx context.Context, orgID uuid.UUID, key string, agentID uuid.UUID) error {
	_, err := s.pool.Exec(ctx,
		"DELETE FROM secret_assignments WHERE org_id=$1 AND key=$2 AND agent_id=$3",
		orgID, key, agentID)
	return err
}
