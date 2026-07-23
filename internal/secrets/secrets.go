// Package secrets definiert den SecretStore-Port (spec/10): get/put/delete.
// Implementierungen: builtin (AES-GCM-Spalte in Postgres) — vault folgt post-MVP.
package secrets

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

var ErrNotFound = errors.New("secret nicht gefunden")

// KeyPreview ist der Name plus ein kurzes, absichtlich begrenztes Präfix des
// Werts (Wiedererkennungshilfe wie bei GitHub/Stripe). Prefix ist leer, wenn
// der Wert zu kurz ist, um ihn ohne nennenswerte Offenlegung anzureißen.
// Ist Revealed=true, enthält Value den vollständigen Klartext — gedacht für
// nicht-sensible Werte wie Servernamen oder URLs.
// AgentIDs sind die expliziten Zuweisungen eines org-weiten Secrets — leer
// heißt: noch keinem Agenten zugewiesen, das Secret erreicht niemanden.
type KeyPreview struct {
	Key      string   `json:"key"`
	Prefix   string   `json:"prefix"`
	Revealed bool     `json:"revealed"`
	Value    string   `json:"value,omitempty"` // nur gesetzt wenn Revealed=true
	AgentIDs []string `json:"agent_ids"`
}

const (
	// previewMinLen: kürzere Werte bleiben komplett maskiert.
	previewMinLen = 12
	// previewChars: so viele führende Zeichen dürfen sichtbar werden.
	previewChars = 4
)

// Preview reißt einen Wert für die UI an — write-only bleibt die Regel, dies
// ist die eng begrenzte Ausnahme. Leerer String heißt „vollständig maskieren".
func Preview(value string) string {
	r := []rune(value)
	if len(r) > previewMinLen {
		return string(r[:previewChars])
	}
	return ""
}

type Store interface {
	// Get/Put/Delete arbeiten auf org-weiten Secrets (ohne Agent-Kontext,
	// z. B. Bootstrap und Webhooks).
	Get(ctx context.Context, orgID uuid.UUID, key string) (string, error)
	Put(ctx context.Context, orgID uuid.UUID, key, value string) error
	Delete(ctx context.Context, orgID uuid.UUID, key string) error
	// Resolve löst ein Secret für einen Agenten auf: agent-eigenes Secret vor
	// org-weitem. Org-Secrets erreichen einen Agenten nur bei expliziter
	// Zuweisung — ohne Zuweisung erreicht ein Org-Secret keinen Agenten.
	Resolve(ctx context.Context, orgID, agentID uuid.UUID, key string) (string, error)
	// PutAgent/DeleteAgent verwalten agent-eigene Secrets.
	PutAgent(ctx context.Context, orgID, agentID uuid.UUID, key, value string) error
	DeleteAgent(ctx context.Context, orgID, agentID uuid.UUID, key string) error
	// Keys listet nur die Namen — Werte bleiben write-only für die API.
	Keys(ctx context.Context, orgID uuid.UUID) ([]string, error)
	// Previews liefert Namen plus begrenztes Wert-Präfix (siehe Preview) der
	// org-weiten Secrets, inklusive ihrer Zuweisungen.
	Previews(ctx context.Context, orgID uuid.UUID) ([]KeyPreview, error)
	// AgentPreviews liefert die agent-eigenen Secrets eines Agenten.
	AgentPreviews(ctx context.Context, orgID, agentID uuid.UUID) ([]KeyPreview, error)
	// Assign/Unassign pflegen die explizite Zuweisung org-weiter Secrets.
	Assign(ctx context.Context, orgID uuid.UUID, key string, agentID uuid.UUID) error
	Unassign(ctx context.Context, orgID uuid.UUID, key string, agentID uuid.UUID) error
	// SetRevealed markiert ein org-weites Secret als einsehbar (true) oder
	// wieder geschützt (false). Einsehbare Secrets geben den vollen Klartext
	// in Previews zurück — gedacht für Servernamen, URLs, nicht für Tokens.
	SetRevealed(ctx context.Context, orgID uuid.UUID, key string, revealed bool) error
}
