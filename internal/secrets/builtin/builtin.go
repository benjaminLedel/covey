// Package builtin ist der DB-gestützte SecretStore: AES-256-GCM-verschlüsselte
// Spalte in Postgres, Master-Key aus ENV (spec/10). Deckt "lagere ein
// Legacy-API-Token und reiche es kurzlebig durch" vollständig ab.
package builtin

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/secrets"
)

type Store struct {
	pool *pgxpool.Pool
	aead cipher.AEAD
}

// New erwartet den Master-Key als 64 Hex-Zeichen (32 Bytes → AES-256).
func New(pool *pgxpool.Pool, masterKeyHex string) (*Store, error) {
	key, err := hex.DecodeString(masterKeyHex)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("COVEY_MASTER_KEY muss 32 Bytes hex sein (64 Zeichen)")
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

// GenerateMasterKey erzeugt einen neuen Master-Key fürs Bootstrapping.
func GenerateMasterKey() (string, error) {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	return hex.EncodeToString(key), nil
}

// aad bindet das Chiffrat an seinen Platz (kein Row-Swapping): org+key für
// org-weite Secrets, org+agent+key für agent-eigene.
func aad(orgID uuid.UUID, agentID *uuid.UUID, key string) []byte {
	if agentID != nil {
		return []byte(orgID.String() + "/" + agentID.String() + "/" + key)
	}
	return []byte(orgID.String() + "/" + key)
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
		return "", fmt.Errorf("secret %q: entschlüsselung fehlgeschlagen: %w", key, err)
	}
	return string(plain), nil
}

func (s *Store) Put(ctx context.Context, orgID uuid.UUID, key, value string) error {
	nonce, ct, err := s.seal(aad(orgID, nil, key), value)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO secrets (org_id, key, nonce, ciphertext)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (org_id, key) WHERE agent_id IS NULL
		DO UPDATE SET nonce=$3, ciphertext=$4, updated_at=now()`,
		orgID, key, nonce, ct)
	return err
}

// PutAgent legt ein agent-eigenes Secret an. Der Agent muss zur Org gehören —
// sonst ErrNotFound (kein Cross-Org-Schreiben über geratene Agent-IDs).
func (s *Store) PutAgent(ctx context.Context, orgID, agentID uuid.UUID, key, value string) error {
	nonce, ct, err := s.seal(aad(orgID, &agentID, key), value)
	if err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `INSERT INTO secrets (org_id, agent_id, key, nonce, ciphertext)
		SELECT $1, $2, $3, $4, $5
		WHERE EXISTS (SELECT 1 FROM agents WHERE id=$2 AND org_id=$1)
		ON CONFLICT (org_id, agent_id, key) WHERE agent_id IS NOT NULL
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

func (s *Store) Get(ctx context.Context, orgID uuid.UUID, key string) (string, error) {
	var nonce, ct []byte
	err := s.pool.QueryRow(ctx,
		"SELECT nonce, ciphertext FROM secrets WHERE org_id=$1 AND key=$2 AND agent_id IS NULL",
		orgID, key).Scan(&nonce, &ct)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", secrets.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(key, aad(orgID, nil, key), nonce, ct)
}

// Resolve bevorzugt das agent-eigene Secret; sonst greift das org-weite —
// aber nur bei expliziter Zuweisung. Ein Org-Secret ohne Zuweisungen
// erreicht keinen Agenten.
func (s *Store) Resolve(ctx context.Context, orgID, agentID uuid.UUID, key string) (string, error) {
	var (
		nonce, ct []byte
		rowAgent  *uuid.UUID
	)
	err := s.pool.QueryRow(ctx, `SELECT nonce, ciphertext, agent_id FROM secrets s
		WHERE s.org_id=$1 AND s.key=$2 AND (
			s.agent_id=$3
			OR (s.agent_id IS NULL AND EXISTS (SELECT 1 FROM secret_assignments a
				WHERE a.org_id=$1 AND a.key=$2 AND a.agent_id=$3)))
		ORDER BY s.agent_id NULLS LAST LIMIT 1`,
		orgID, key, agentID).Scan(&nonce, &ct, &rowAgent)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", secrets.ErrNotFound
	}
	if err != nil {
		return "", err
	}
	return s.open(key, aad(orgID, rowAgent, key), nonce, ct)
}

func (s *Store) Delete(ctx context.Context, orgID uuid.UUID, key string) error {
	// Zuweisungen hängen am org-weiten Secret — mit ihm verschwinden sie.
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

// Previews entschlüsselt jeden Wert: nicht-sensible Secrets sind Variablen
// und liefern den vollständigen Klartext, sensible nur ihr begrenztes Präfix.
func (s *Store) Previews(ctx context.Context, orgID uuid.UUID) ([]secrets.KeyPreview, error) {
	rows, err := s.pool.Query(ctx, `SELECT s.key, s.nonce, s.ciphertext, s.sensitive,
			COALESCE(array_agg(a.agent_id::text ORDER BY a.created_at)
				FILTER (WHERE a.agent_id IS NOT NULL), '{}')
		FROM secrets s
		LEFT JOIN secret_assignments a ON a.org_id=s.org_id AND a.key=s.key
		WHERE s.org_id=$1 AND s.agent_id IS NULL
		GROUP BY s.key, s.nonce, s.ciphertext, s.sensitive ORDER BY s.key`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secrets.KeyPreview
	for rows.Next() {
		var (
			k         string
			nonce, ct []byte
			sensitive bool
			agentIDs  []string
		)
		if err := rows.Scan(&k, &nonce, &ct, &sensitive, &agentIDs); err != nil {
			return nil, err
		}
		kp := secrets.KeyPreview{
			Key:       k,
			Sensitive: sensitive,
			AgentIDs:  agentIDs,
		}
		kp.Value, kp.Prefix = s.expose(k, aad(orgID, nil, k), nonce, ct, sensitive)
		out = append(out, kp)
	}
	return out, rows.Err()
}

// MarkSensitive ist bewusst einweg (siehe Port-Doku): nur false→true.
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
		"SELECT key, nonce, ciphertext, sensitive FROM secrets WHERE org_id=$1 AND agent_id=$2 ORDER BY key",
		orgID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secrets.KeyPreview
	for rows.Next() {
		var (
			k         string
			nonce, ct []byte
			sensitive bool
		)
		if err := rows.Scan(&k, &nonce, &ct, &sensitive); err != nil {
			return nil, err
		}
		kp := secrets.KeyPreview{Key: k, Sensitive: sensitive, AgentIDs: []string{}}
		kp.Value, kp.Prefix = s.expose(k, aad(orgID, &agentID, k), nonce, ct, sensitive)
		out = append(out, kp)
	}
	return out, rows.Err()
}

// expose entschlüsselt best-effort für die Anzeige: nicht-sensible Werte
// vollständig, sensible nur als Präfix. Ein nicht entschlüsselbares Secret
// (z. B. mit altem Master-Key) darf die Liste nicht kippen — dann eben
// voll maskiert.
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

// Assign weist ein org-weites Secret einem Agenten explizit zu. Secret und
// Agent müssen existieren und zur Org gehören — sonst ErrNotFound.
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
