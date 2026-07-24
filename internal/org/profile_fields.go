package org

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Konfigurierbare Profilfelder (spec/09): die Organisation definiert selbst,
// welche zusätzlichen Felder ein Mitarbeiter-Profil hat. Die Definitionen
// liegen in profile_fields, die Werte pro Person in humans.custom — beides
// generisch, ein neues Feld ist reine Konfiguration.

var ErrFieldExists = errors.New("ein Profilfeld mit diesem Namen existiert bereits")

type ProfileField struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	Key       string    `json:"key"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"created_at"`
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// FieldKey leitet den Map-Schlüssel aus dem Anzeigenamen ab ("Slack-Handle"
// → "slack-handle"). Der Schlüssel bleibt beim Umbenennen stabil, damit die
// Werte in humans.custom nicht verwaisen.
func FieldKey(label string) string {
	k := strings.ToLower(strings.TrimSpace(label))
	for _, r := range []struct{ from, to string }{{"ä", "ae"}, {"ö", "oe"}, {"ü", "ue"}, {"ß", "ss"}} {
		k = strings.ReplaceAll(k, r.from, r.to)
	}
	return strings.Trim(nonSlug.ReplaceAllString(k, "-"), "-")
}

func (s *Store) ListProfileFields(ctx context.Context, orgID uuid.UUID) ([]ProfileField, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, key, label, created_at
		FROM profile_fields WHERE org_id=$1 ORDER BY created_at`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var list []ProfileField
	for rows.Next() {
		var f ProfileField
		if err := rows.Scan(&f.ID, &f.OrgID, &f.Key, &f.Label, &f.CreatedAt); err != nil {
			return nil, err
		}
		list = append(list, f)
	}
	return list, rows.Err()
}

func (s *Store) CreateProfileField(ctx context.Context, orgID uuid.UUID, label string) (ProfileField, error) {
	f := ProfileField{ID: uuid.New(), OrgID: orgID, Key: FieldKey(label), Label: strings.TrimSpace(label)}
	if f.Key == "" || f.Label == "" {
		return ProfileField{}, errors.New("label ist Pflicht")
	}
	err := s.pool.QueryRow(ctx, `INSERT INTO profile_fields (id, org_id, key, label)
		VALUES ($1,$2,$3,$4) RETURNING created_at`, f.ID, orgID, f.Key, f.Label).Scan(&f.CreatedAt)
	if isUniqueViolation(err) {
		return ProfileField{}, ErrFieldExists
	}
	return f, err
}

// RenameProfileField ändert nur das Label — der Key (und damit die Werte in
// humans.custom) bleibt stabil.
func (s *Store) RenameProfileField(ctx context.Context, orgID, id uuid.UUID, label string) error {
	if strings.TrimSpace(label) == "" {
		return errors.New("label ist Pflicht")
	}
	tag, err := s.pool.Exec(ctx, `UPDATE profile_fields SET label=$1 WHERE id=$2 AND org_id=$3`,
		strings.TrimSpace(label), id, orgID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteProfileField entfernt die Definition und räumt die zugehörigen Werte
// aus allen Profilen der Organisation (Menschen wie Agenten) — keine
// verwaisten Einträge.
func (s *Store) DeleteProfileField(ctx context.Context, orgID, id uuid.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var key string
	err = tx.QueryRow(ctx, `DELETE FROM profile_fields WHERE id=$1 AND org_id=$2 RETURNING key`, id, orgID).Scan(&key)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE humans SET custom = custom - $1 WHERE org_id=$2`, key, orgID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agents SET custom = custom - $1 WHERE org_id=$2`, key, orgID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
