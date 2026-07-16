// Package store verwaltet Zielsystem-Plugins pro Organisation: die
// Aktivierung der kompilierten Built-ins und die hochgeladenen
// Manifest-Plugins (kind=custom). Control-Plane-seitig — der Daemon
// bekommt Manifeste über das Daemon-Protokoll gebrokert.
package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/target"
)

var ErrNotFound = errors.New("zielsystem nicht gefunden")

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Plugin ist die UI-/API-Sicht auf ein Zielsystem-Plugin.
type Plugin struct {
	Name        string          `json:"name"`
	Label       string          `json:"label"`
	Description string          `json:"description"`
	Kind        string          `json:"kind"` // builtin | custom
	Enabled     bool            `json:"enabled"`
	Manifest    json.RawMessage `json:"manifest,omitempty"`
	UpdatedAt   *time.Time      `json:"updated_at,omitempty"`
}

// List mergt die kompilierte Registry mit den DB-Zeilen der Organisation.
// Built-ins ohne Zeile gelten als aktiviert (Bestandsschutz).
func (s *Store) List(ctx context.Context, orgID uuid.UUID) ([]Plugin, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, kind, enabled, manifest, updated_at FROM target_plugins WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	stored := map[string]Plugin{}
	for rows.Next() {
		var p Plugin
		var manifest []byte
		if err := rows.Scan(&p.Name, &p.Kind, &p.Enabled, &manifest, &p.UpdatedAt); err != nil {
			return nil, err
		}
		p.Manifest = manifest
		stored[p.Name] = p
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}

	var out []Plugin
	for _, d := range target.All() {
		p := Plugin{Name: d.Name, Label: d.Label, Description: d.Description, Kind: "builtin", Enabled: true}
		if row, ok := stored[d.Name]; ok {
			p.Enabled = row.Enabled
			p.UpdatedAt = row.UpdatedAt
			delete(stored, d.Name)
		}
		out = append(out, p)
	}
	for _, p := range stored {
		if p.Kind != "custom" {
			continue // Zeile eines Built-ins, das dieses Binary nicht mitkompiliert hat
		}
		if m, err := target.ParseManifest(p.Manifest); err == nil {
			p.Label, p.Description = m.Label, m.Description
			if p.Label == "" {
				p.Label = m.Name
			}
		}
		out = append(out, p)
	}
	return out, nil
}

// SetEnabled schaltet ein Plugin für die Organisation an/aus. Für Built-ins
// wird die Zeile bei Bedarf angelegt; unbekannte Namen sind ein Fehler.
func (s *Store) SetEnabled(ctx context.Context, orgID uuid.UUID, name string, enabled bool) error {
	if _, ok := target.Get(name); ok {
		_, err := s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled)
			VALUES ($1,$2,'builtin',$3)
			ON CONFLICT (org_id, name) DO UPDATE SET enabled=$3, updated_at=now()`,
			orgID, name, enabled)
		return err
	}
	tag, err := s.pool.Exec(ctx, `UPDATE target_plugins SET enabled=$3, updated_at=now()
		WHERE org_id=$1 AND name=$2 AND kind='custom'`, orgID, name, enabled)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// PutManifest validiert und speichert ein hochgeladenes Manifest-Plugin.
// Der Name eines kompilierten Built-ins ist tabu — kein stilles Überschatten.
func (s *Store) PutManifest(ctx context.Context, orgID uuid.UUID, raw []byte) (target.Manifest, error) {
	m, err := target.ParseManifest(raw)
	if err != nil {
		return m, err
	}
	if _, ok := target.Get(m.Name); ok {
		return m, fmt.Errorf("name %q ist durch ein eingebautes Plugin belegt", m.Name)
	}
	// Normalisiert (kompaktes JSON) speichern, nicht den Roh-Upload.
	norm, err := json.Marshal(m)
	if err != nil {
		return m, err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO target_plugins (org_id, name, kind, enabled, manifest)
		VALUES ($1,$2,'custom',TRUE,$3)
		ON CONFLICT (org_id, name) DO UPDATE SET kind='custom', manifest=$3, updated_at=now()`,
		orgID, m.Name, norm)
	return m, err
}

// DeleteManifest entfernt ein Custom-Plugin. Built-ins lassen sich nur
// deaktivieren, nicht löschen.
func (s *Store) DeleteManifest(ctx context.Context, orgID uuid.UUID, name string) error {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM target_plugins WHERE org_id=$1 AND name=$2 AND kind='custom'`, orgID, name)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// System löst ein Zielsystem für eine Organisation auf — nur wenn aktiviert
// (fail-closed): erst die kompilierte Registry, dann die Custom-Manifeste.
func (s *Store) System(ctx context.Context, orgID uuid.UUID, name string) (target.System, error) {
	var kind string
	var enabled bool
	var manifest []byte
	err := s.pool.QueryRow(ctx, `SELECT kind, enabled, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2`, orgID, name).Scan(&kind, &enabled, &manifest)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		// Keine Zeile: Built-ins gelten als aktiviert, alles andere gibt es nicht.
		if sys, ok := target.Get(name); ok {
			return sys, nil
		}
		return nil, ErrNotFound
	case err != nil:
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("%w: %s ist deaktiviert", ErrNotFound, name)
	}
	if kind == "builtin" {
		if sys, ok := target.Get(name); ok {
			return sys, nil
		}
		return nil, ErrNotFound
	}
	m, err := target.ParseManifest(manifest)
	if err != nil {
		return nil, fmt.Errorf("gespeichertes manifest %s: %w", name, err)
	}
	return target.NewManifestSystem(m), nil
}

// Manifest liefert das gespeicherte Manifest eines aktivierten Custom-Plugins
// (für den Daemon, der es zur Ausführung braucht).
func (s *Store) Manifest(ctx context.Context, orgID uuid.UUID, name string) (target.Manifest, error) {
	var enabled bool
	var manifest []byte
	err := s.pool.QueryRow(ctx, `SELECT enabled, manifest FROM target_plugins
		WHERE org_id=$1 AND name=$2 AND kind='custom'`, orgID, name).Scan(&enabled, &manifest)
	if errors.Is(err, pgx.ErrNoRows) {
		return target.Manifest{}, ErrNotFound
	}
	if err != nil {
		return target.Manifest{}, err
	}
	if !enabled {
		return target.Manifest{}, fmt.Errorf("%w: %s ist deaktiviert", ErrNotFound, name)
	}
	return target.ParseManifest(manifest)
}

// EnabledDocs sammelt die Prompt-Dokus aller für die Organisation
// aktivierten Zielsysteme (Built-ins + Custom-Manifeste).
func (s *Store) EnabledDocs(ctx context.Context, orgID uuid.UUID) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT name, kind, enabled, manifest FROM target_plugins WHERE org_id=$1`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	disabled := map[string]bool{}
	var docs []string
	for rows.Next() {
		var name, kind string
		var enabled bool
		var manifest []byte
		if err := rows.Scan(&name, &kind, &enabled, &manifest); err != nil {
			return nil, err
		}
		if !enabled {
			disabled[name] = true
			continue
		}
		if kind == "custom" {
			if m, err := target.ParseManifest(manifest); err == nil {
				docs = append(docs, target.NewManifestSystem(m).PromptDoc())
			}
		}
	}
	if rows.Err() != nil {
		return nil, rows.Err()
	}
	for _, d := range target.All() {
		if !disabled[d.Name] && d.System != nil {
			docs = append(docs, d.System.PromptDoc())
		}
	}
	return docs, nil
}
