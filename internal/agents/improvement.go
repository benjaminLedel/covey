package agents

// Der offene Punkt: was ein Review hinterlässt, und was ein Mensch damit tut
// (spec/21-operations-and-improvement.md).
//
// Der Kern ist die Config-Version, die GESPEICHERT und NICHT IN KRAFT ist. Bis
// hierher kannte die Plattform nur die eine Ordnung: agent_config_versions
// nummeriert pro Agent, die höchste Nummer läuft. Ein Vorschlag ist eine
// Zeile, die in dieser Folge nicht mitzählt — er trägt den Agenten, gegen
// welche Version er geschrieben wurde, die Dateien, die er ändert, die
// Aufgabe, aus der er kam, und einen Status. Angenommen wird er über den
// normalen Schreibweg, mit dem Menschen als Urheber.
//
// Zwei Eigenschaften fallen dabei ab, und beide sind gewollt:
//
//   - Ein Vorschlag ist ein Diff gegen eine Basis. Wird der Agent zwischen dem
//     Schreiben und dem Annehmen von Hand geändert, darf die Annahme diese
//     Änderung nicht still überschreiben (ProposalConflicts).
//   - Ein Vorschlag läuft nicht. Ein kompromittierter Betriebsingenieur
//     erzeugt eine Warteschlange schlechter Vorschläge, die ein Mensch
//     ablehnt — ein Ärgernis, kein Vorfall.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Die drei Ergebnisse eines Reviews. Sie liegen in einer Tabelle und in einer
// Liste, weil sie denselben Menschen brauchen: die Config ist falsch, der
// Auftrag ist falsch, die Plattform ist falsch (spec/21).
const (
	// KindProposal trägt einen Diff und wird durch Annahme zu einer Version.
	KindProposal = "proposal"
	// KindFinding hat keinen Diff: den Auftrag eines Kollegen kann die
	// Plattform nicht umschreiben, das kann nur der Mensch, der ihn
	// verantwortet.
	KindFinding = "finding"
	// KindIssue ist ein Bericht, der schon im Tracker liegt — hier steht er,
	// damit der Mensch ihn sieht, nicht damit er ihn ausführt.
	KindIssue = "issue"
)

const (
	ImprovementPending  = "pending"
	ImprovementAccepted = "accepted"
	ImprovementRejected = "rejected"
)

var (
	// ErrProposalEmpty: ein Vorschlag ohne Dateien ist kein Vorschlag.
	ErrProposalEmpty = errors.New("a proposal has to change at least one file")
	// ErrSelfReview: er begutachtet sich nicht selbst (spec/21, Regel 2).
	// Dieselbe Linie, die spec/20 für die Personalabteilung zieht.
	ErrSelfReview = errors.New("an agent cannot propose its own configuration")
	// ErrNotPending: entschieden wird einmal. Der zweite Klick ist kein
	// zweiter Beschluss.
	ErrNotPending = errors.New("this item has already been decided")
	// ErrProposalConflict: die Basis ist unter dem Vorschlag weggewandert.
	ErrProposalConflict = errors.New("the proposal conflicts with the current configuration")
)

// ImprovementItem ist ein offener Punkt zu einem Kollegen.
type ImprovementItem struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Rationale string    `json:"rationale"`
	// BaseVersion ist die Config-Version, gegen die geschrieben wurde
	// (0 = keine/kein Vorschlag). Von der Plattform gesetzt, nicht gemeldet.
	BaseVersion int `json:"base_version"`
	// Files sind NUR die geänderten Dateien. Beim Annehmen wird gemergt,
	// nie ersetzt — dieselbe Semantik wie bei set_agent_config, und aus
	// demselben Grund: nichts hier muss eine Datei löschen können.
	Files map[string]string `json:"files"`
	// AuthorAgentID ist der Absender; nil = ein Mensch hat den Punkt angelegt.
	AuthorAgentID  *uuid.UUID `json:"author_agent_id,omitempty"`
	TaskID         *uuid.UUID `json:"task_id,omitempty"`
	Status         string     `json:"status"`
	DecidedBy      *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecisionNote   string     `json:"decision_note"`
	AppliedVersion int        `json:"applied_version"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ImprovementFilter grenzt die Liste ein. Der Nullwert heißt „alles".
type ImprovementFilter struct {
	Status string
	Kind   string
	// AgentIDs schränkt auf bestimmte Kollegen ein. nil = keine
	// Einschränkung; die LEERE (nicht-nil) Liste heißt „keiner" und liefert
	// nichts — der Unterschied trägt die Sicht des Agent-Owners, der keinen
	// Agenten besitzt.
	AgentIDs []uuid.UUID
}

// CreateImprovement legt einen offenen Punkt an. Die Herkunft schreibt die
// Plattform: die Basisversion wird hier gelesen und nicht übergeben — ein
// Modell, das seine eigene Basis benennen darf, kann einen Konflikt
// wegdefinieren.
func (r *Registry) CreateImprovement(ctx context.Context, item ImprovementItem) (ImprovementItem, error) {
	target, err := r.Get(ctx, item.AgentID)
	if err != nil {
		return item, err
	}
	if target.OrgID != item.OrgID {
		return item, ErrNotFound
	}
	// Er begutachtet sich nicht selbst. Hier und nicht im Prompt: eine Grenze,
	// die ein Agent selbst einhalten soll, ist keine.
	if item.AuthorAgentID != nil && *item.AuthorAgentID == item.AgentID {
		return item, ErrSelfReview
	}
	switch item.Kind {
	case KindProposal:
		if len(item.Files) == 0 {
			return item, ErrProposalEmpty
		}
		cur, err := r.CurrentConfig(ctx, item.AgentID)
		switch {
		case err == nil:
			item.BaseVersion = cur.Version
		case errors.Is(err, ErrNotFound):
			item.BaseVersion = 0
		default:
			return item, err
		}
	case KindFinding, KindIssue:
		// Ohne Diff: der Befund ist der Text, der Punkt bleibt offen, bis ein
		// Mensch ihn schließt.
		item.Files = nil
		item.BaseVersion = 0
	default:
		return item, fmt.Errorf("unknown kind %q", item.Kind)
	}
	if strings.TrimSpace(item.Title) == "" {
		return item, errors.New("title is missing")
	}

	item.ID = uuid.New()
	item.Status = ImprovementPending
	filesJSON, err := json.Marshal(nonNilFiles(item.Files))
	if err != nil {
		return item, err
	}
	err = r.pool.QueryRow(ctx, `INSERT INTO improvement_items
		(id, org_id, agent_id, kind, title, rationale, base_version, files, author_agent_id, task_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10) RETURNING created_at`,
		item.ID, item.OrgID, item.AgentID, item.Kind, strings.TrimSpace(item.Title), item.Rationale,
		item.BaseVersion, filesJSON, item.AuthorAgentID, item.TaskID).Scan(&item.CreatedAt)
	return item, err
}

const improvementCols = `id, org_id, agent_id, kind, title, rationale, base_version, files,
	author_agent_id, task_id, status, decided_by, decided_at, decision_note, applied_version, created_at`

func scanImprovement(row pgx.Row) (ImprovementItem, error) {
	var it ImprovementItem
	var filesJSON []byte
	if err := row.Scan(&it.ID, &it.OrgID, &it.AgentID, &it.Kind, &it.Title, &it.Rationale,
		&it.BaseVersion, &filesJSON, &it.AuthorAgentID, &it.TaskID, &it.Status,
		&it.DecidedBy, &it.DecidedAt, &it.DecisionNote, &it.AppliedVersion, &it.CreatedAt); err != nil {
		return it, err
	}
	if len(filesJSON) > 0 {
		if err := json.Unmarshal(filesJSON, &it.Files); err != nil {
			return it, fmt.Errorf("proposal files: %w", err)
		}
	}
	return it, nil
}

// ListImprovements liefert die offenen (und entschiedenen) Punkte einer
// Organisation, neueste zuerst.
func (r *Registry) ListImprovements(ctx context.Context, orgID uuid.UUID, f ImprovementFilter) ([]ImprovementItem, error) {
	if f.AgentIDs != nil && len(f.AgentIDs) == 0 {
		return []ImprovementItem{}, nil
	}
	rows, err := r.pool.Query(ctx, `SELECT `+improvementCols+` FROM improvement_items
		WHERE org_id=$1
		  AND ($2='' OR status=$2)
		  AND ($3='' OR kind=$3)
		  AND ($4::uuid[] IS NULL OR agent_id = ANY($4))
		ORDER BY created_at DESC`, orgID, f.Status, f.Kind, f.AgentIDs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ImprovementItem{}
	for rows.Next() {
		it, err := scanImprovement(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

// GetImprovement liest einen Punkt.
func (r *Registry) GetImprovement(ctx context.Context, id uuid.UUID) (ImprovementItem, error) {
	it, err := scanImprovement(r.pool.QueryRow(ctx,
		`SELECT `+improvementCols+` FROM improvement_items WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// DecideImprovement hält die Entscheidung fest. Das UPDATE ist gegen
// status='pending' geführt: zwei Menschen, die gleichzeitig auf annehmen
// klicken, erzeugen eine Version und einen Fehler, nicht zwei Versionen.
func (r *Registry) DecideImprovement(ctx context.Context, id uuid.UUID, status string,
	by uuid.UUID, note string, appliedVersion int) (ImprovementItem, error) {

	it, err := scanImprovement(r.pool.QueryRow(ctx, `UPDATE improvement_items
		SET status=$2, decided_by=$3, decided_at=now(), decision_note=$4, applied_version=$5
		WHERE id=$1 AND status='pending'
		RETURNING `+improvementCols, id, status, by, note, appliedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		// Entweder es gibt ihn nicht, oder er ist schon entschieden — die
		// zweite Lesart ist die häufigere, also fragen wir nach.
		if _, gerr := r.GetImprovement(ctx, id); gerr == nil {
			return it, ErrNotPending
		}
		return it, ErrNotFound
	}
	return it, err
}

// --- Die reine Rechnerei: mergen, vergleichen, Konflikte finden ---

// RestrictedConfigFiles sind die Dateien, deren Schreibweg spec/02 bei
// platform_admin/security reserviert: ACCESS.md und EGRESS.md sind die
// Textansicht auf Zustand, den sonst nur diese Rollen ändern dürfen. Ein
// Vorschlag erbt diese Grenze, statt sie zu umgehen — ein Review-Dialog, der
// alles durchlässt, weil der VORSCHLAG harmlos war, verschöbe die
// Zugriffsentscheidung von der Security zu dem, der zuerst geklickt hat.
var RestrictedConfigFiles = []string{"ACCESS.md", "EGRESS.md"}

// MergeConfig legt die geänderten Dateien auf den bestehenden Satz. Gemergt
// und nicht ersetzt: was der Vorschlag nicht anfasst, bleibt stehen. Wer eine
// Datei loswerden will, schreibt sie leer.
func MergeConfig(current, changes map[string]string) map[string]string {
	out := make(map[string]string, len(current)+len(changes))
	for name, content := range current {
		out[name] = content
	}
	for name, content := range changes {
		out[name] = content
	}
	return out
}

// ChangedFiles sind die Dateien, die der Vorschlag gegenüber dem aktuellen
// Stand wirklich ändert. Ein Vorschlag, der eine Datei unverändert
// mitschickt, ändert sie nicht — das entscheidet mit darüber, wer ihn
// annehmen darf.
func ChangedFiles(current, changes map[string]string) []string {
	var out []string
	for name, content := range changes {
		if cur, ok := current[name]; !ok || cur != content {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// RestrictedChanges beantwortet die Frage der Annahme-Oberfläche: fasst
// dieser Vorschlag ACCESS.md oder EGRESS.md an? Wenn ja, darf ihn der
// Teamleiter, dem der Agent gehört, nicht annehmen.
//
// Bewusst „berührt" und nicht „würde etwas umschalten": der Schreibweg für
// Config prüft heute, ob sich Tools oder Egress-Ziele tatsächlich ändern —
// eine Zeile `scope:` mehr in ACCESS.md fällt da nicht auf, weitet den Zugang
// aber sehr wohl. spec/21 sagt, die Oberfläche liest die DATEIEN des
// Vorschlags. Genau das tut sie hier.
func RestrictedChanges(current, changes map[string]string) []string {
	var out []string
	for _, name := range RestrictedConfigFiles {
		content, ok := changes[name]
		if !ok {
			continue
		}
		if cur, exists := current[name]; !exists || cur != content {
			out = append(out, name)
		}
	}
	return out
}

// ProposalConflicts sind die Dateien, die der Vorschlag ändert und die sich
// seit seiner Basis unter ihm verändert haben.
//
// Der Unterschied zu „veraltet" trägt die Bedienbarkeit: dass jemand
// zwischenzeitlich die KPIS.md bearbeitet hat, macht einen Vorschlag zur
// SOUL.md nicht falsch. Erst wenn dieselbe Datei angefasst wurde, würde die
// Annahme eine fremde Änderung überschreiben — und nur dann steht sie still.
func ProposalConflicts(base, current, changes map[string]string) []string {
	var out []string
	for name := range changes {
		if base[name] != current[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func nonNilFiles(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
