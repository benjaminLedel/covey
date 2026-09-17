package agents

// The open point: what a review leaves behind, and what a human does with it
// (spec/21-operations-and-improvement.md).
//
// The core is the config version that is SAVED and NOT IN EFFECT. Until this
// point the platform only knew one order: agent_config_versions numbers per
// agent, and the highest number runs. A proposal is a row that does not count
// in that sequence — it carries the agent, the version it was written
// against, the files it changes, the task it came from, and a status. It is
// accepted through the normal write path, the one that attributes it to a
// human as the author.
//
// Two properties fall out of this, and both are intended:
//
//   - A proposal is a diff against a base. If the agent is edited by hand
//     between the write and the acceptance, the acceptance must not silently
//     overwrite that change (ProposalConflicts).
//   - A proposal does not run. A compromised covey Doctor produces a
//     queue of bad proposals that a human rejects — an annoyance, not an
//     incident.

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

// The three outcomes of a review. They sit in one table and one list,
// because they need the same human: the config is wrong, the assignment is
// wrong, the platform is wrong (spec/21).
const (
	// KindProposal carries a diff and becomes a version once accepted.
	KindProposal = "proposal"
	// KindFinding has no diff: the platform cannot rewrite a
	// colleague's assignment, only the human responsible for it
	// can do that.
	KindFinding = "finding"
	// KindIssue is a report that already lies in the tracker — it stands here
	// so the human sees it, not so they carry it out.
	KindIssue = "issue"
	// KindToolRequest is the request for a tool: the agent lacks a package and
	// is nowhere root. Without this path it rebuilt apt inside its own home —
	// unreproducible, unrecorded, and carried along by every sync. Like a
	// finding: no diff, a human decides.
	KindToolRequest = "tool_request"
)

const (
	ImprovementPending  = "pending"
	ImprovementAccepted = "accepted"
	ImprovementRejected = "rejected"
)

var (
	// ErrProposalEmpty: a proposal without files is not a proposal.
	ErrProposalEmpty = errors.New("a proposal has to change at least one file")
	// ErrNotPending: the decision is made once. The second click is not a
	// second decision.
	ErrNotPending = errors.New("this item has already been decided")
	// ErrProposalConflict: the base has moved out from under the proposal.
	ErrProposalConflict = errors.New("the proposal conflicts with the current configuration")
)

// ImprovementItem is an open point about a colleague.
type ImprovementItem struct {
	ID        uuid.UUID `json:"id"`
	OrgID     uuid.UUID `json:"org_id"`
	AgentID   uuid.UUID `json:"agent_id"`
	Kind      string    `json:"kind"`
	Title     string    `json:"title"`
	Rationale string    `json:"rationale"`
	// Link is the address of an issue that already lies in the tracker (only
	// with KindIssue). A report without the link back there forces every
	// reader to search.
	Link string `json:"link,omitempty"`
	// BaseVersion is the config version the proposal was written against
	// (0 = none / not a proposal). Set by the platform, not reported in.
	BaseVersion int `json:"base_version"`
	// Files are ONLY the changed files. Acceptance merges, never replaces —
	// the same semantics as set_agent_config, and for the same
	// reason: nothing here has to be able to delete a file.
	Files map[string]string `json:"files"`
	// AuthorAgentID is the sender; nil = a human created the point.
	AuthorAgentID  *uuid.UUID `json:"author_agent_id,omitempty"`
	TaskID         *uuid.UUID `json:"task_id,omitempty"`
	Status         string     `json:"status"`
	DecidedBy      *uuid.UUID `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	DecisionNote   string     `json:"decision_note"`
	AppliedVersion int        `json:"applied_version"`
	CreatedAt      time.Time  `json:"created_at"`
}

// ImprovementFilter narrows the list. The zero value means "everything".
type ImprovementFilter struct {
	Status string
	Kind   string
	// AgentIDs restricts to particular colleagues. nil = no
	// restriction; the EMPTY (non-nil) list means "none" and returns
	// nothing — the difference carries the view of the agent owner who owns no
	// agent.
	AgentIDs []uuid.UUID
}

// CreateImprovement creates an open point. The platform writes the
// provenance: the base version is read here, not passed in — a model that
// may name its own base can define away a
// conflict.
//
// Deliberately WITHOUT a check for whether an agent writes about itself. A
// proposal to itself is allowed and was the point of the whole exercise: the
// People department may propose its own configuration after its
// self-onboarding (spec/20), and what would be dangerous about it — an agent
// rewriting itself at night — cannot happen here, because nothing from here
// runs. A human accepts it, or it stays. The scope decides who may do
// that, and the scope lives in the orchestrator.
func (r *Registry) CreateImprovement(ctx context.Context, item ImprovementItem) (ImprovementItem, error) {
	target, err := r.Get(ctx, item.AgentID)
	if err != nil {
		return item, err
	}
	if target.OrgID != item.OrgID {
		return item, ErrNotFound
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
	case KindFinding, KindIssue, KindToolRequest:
		// No diff: the finding is the text, the point stays open until a
		// human closes it.
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
		(id, org_id, agent_id, kind, title, rationale, link, base_version, files, author_agent_id, task_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) RETURNING created_at`,
		item.ID, item.OrgID, item.AgentID, item.Kind, strings.TrimSpace(item.Title), item.Rationale,
		strings.TrimSpace(item.Link), item.BaseVersion, filesJSON, item.AuthorAgentID, item.TaskID).Scan(&item.CreatedAt)
	return item, err
}

const improvementCols = `id, org_id, agent_id, kind, title, rationale, link, base_version, files,
	author_agent_id, task_id, status, decided_by, decided_at, decision_note, applied_version, created_at`

func scanImprovement(row pgx.Row) (ImprovementItem, error) {
	var it ImprovementItem
	var filesJSON []byte
	if err := row.Scan(&it.ID, &it.OrgID, &it.AgentID, &it.Kind, &it.Title, &it.Rationale, &it.Link,
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

// ListImprovements returns the open (and decided) points of an
// organisation, newest first.
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

// GetImprovement reads one point.
func (r *Registry) GetImprovement(ctx context.Context, id uuid.UUID) (ImprovementItem, error) {
	it, err := scanImprovement(r.pool.QueryRow(ctx,
		`SELECT `+improvementCols+` FROM improvement_items WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return it, ErrNotFound
	}
	return it, err
}

// DecideImprovement records the decision. The UPDATE is issued against
// status='pending': two humans who click accept at the same time produce one
// version and one error, not two versions.
func (r *Registry) DecideImprovement(ctx context.Context, id uuid.UUID, status string,
	by uuid.UUID, note string, appliedVersion int) (ImprovementItem, error) {

	it, err := scanImprovement(r.pool.QueryRow(ctx, `UPDATE improvement_items
		SET status=$2, decided_by=$3, decided_at=now(), decision_note=$4, applied_version=$5
		WHERE id=$1 AND status='pending'
		RETURNING `+improvementCols, id, status, by, note, appliedVersion))
	if errors.Is(err, pgx.ErrNoRows) {
		// Either it does not exist, or it is already decided — the second
		// reading is the more common one, so we ask again.
		if _, gerr := r.GetImprovement(ctx, id); gerr == nil {
			return it, ErrNotPending
		}
		return it, ErrNotFound
	}
	return it, err
}

// --- The pure computation: merging, comparing, finding conflicts ---

// RestrictedConfigFiles are the files whose write path spec/02 reserves for
// org_admin/security: ACCESS.md and EGRESS.md are the text view of state
// that otherwise only these roles may change. A proposal inherits this
// boundary instead of bypassing it — a review dialog that lets everything
// through because the PROPOSAL was harmless would move the access decision
// from security to whoever clicked first.
var RestrictedConfigFiles = []string{"ACCESS.md", "EGRESS.md"}

// MergeConfig lays the changed files over the existing set. Merged and not
// replaced: what the proposal does not touch stays. Whoever wants to get rid
// of a file writes it empty.
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

// ChangedFiles are the files the proposal really changes compared to the
// current state. A proposal that sends a file along unchanged does not
// change it — this partly decides who may accept
// it.
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

// RestrictedChanges answers the acceptance UI's question: does this
// proposal touch ACCESS.md or EGRESS.md? If so, the team leader who owns the
// agent may not accept it.
//
// Deliberately "touches" and not "would switch something": the config write
// path today checks whether tools or egress targets actually change — one
// more `scope:` line in ACCESS.md does not register there, yet it does widen
// access. spec/21 says the UI reads the FILES of the
// proposal. That is exactly what it does here.
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

// ProposalConflicts are the files the proposal changes and that have changed
// under it since its base.
//
// The difference to "stale" carries the usability: that somebody edited
// KPIS.md in the meantime does not make a proposal to the SOUL.md wrong.
// Only when the same file was touched would the acceptance overwrite a
// foreign change — and only then does it stop.
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

// --- The review: the assessment itself, dated ---

// Review is what the operation has written about a colleague. It waits for
// nothing — unlike an open point it needs no decision, only a reader. That
// is why it sits in its own table and not in the inbox: what lies there is
// meant to disappear once someone has decided.
//
// It reaches the REVIEWED agent by no path, and that is structural and not a
// rule (spec/21): the prompt carries only the active config version, these
// rows are not one; an agent's memory is scoped to itself, so a colleague
// cannot write into it; and there is no action that reads reviews. Whoever
// builds one turns three properties into a policy — and policies get
// forgotten.
type Review struct {
	ID            uuid.UUID  `json:"id"`
	OrgID         uuid.UUID  `json:"org_id"`
	AgentID       uuid.UUID  `json:"agent_id"`
	AuthorAgentID *uuid.UUID `json:"author_agent_id,omitempty"`
	TaskID        *uuid.UUID `json:"task_id,omitempty"`
	PeriodFrom    time.Time  `json:"period_from"`
	PeriodTo      time.Time  `json:"period_to"`
	Summary       string     `json:"summary"`
	CreatedAt     time.Time  `json:"created_at"`
}

// CreateReview records an assessment.
func (r *Registry) CreateReview(ctx context.Context, rev Review) (Review, error) {
	target, err := r.Get(ctx, rev.AgentID)
	if err != nil {
		return rev, err
	}
	if target.OrgID != rev.OrgID {
		return rev, ErrNotFound
	}
	if strings.TrimSpace(rev.Summary) == "" {
		return rev, errors.New("summary is missing")
	}
	rev.ID = uuid.New()
	err = r.pool.QueryRow(ctx, `INSERT INTO agent_reviews
		(id, org_id, agent_id, author_agent_id, task_id, period_from, period_to, summary)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING created_at`,
		rev.ID, rev.OrgID, rev.AgentID, rev.AuthorAgentID, rev.TaskID,
		rev.PeriodFrom, rev.PeriodTo, strings.TrimSpace(rev.Summary)).Scan(&rev.CreatedAt)
	return rev, err
}

// Reviews returns the history of a colleague, newest first.
func (r *Registry) Reviews(ctx context.Context, agentID uuid.UUID, limit int) ([]Review, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `SELECT id, org_id, agent_id, author_agent_id, task_id,
			period_from, period_to, summary, created_at
		FROM agent_reviews WHERE agent_id=$1 ORDER BY created_at DESC LIMIT $2`, agentID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var rev Review
		if err := rows.Scan(&rev.ID, &rev.OrgID, &rev.AgentID, &rev.AuthorAgentID, &rev.TaskID,
			&rev.PeriodFrom, &rev.PeriodTo, &rev.Summary, &rev.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, rev)
	}
	return out, rows.Err()
}

// LastReviewedAt says when a colleague was last assessed (zero time = never).
// This is the number from which it follows who is due next — and it stands
// here so the cycle does not have to guess it.
func (r *Registry) LastReviewedAt(ctx context.Context, agentID uuid.UUID) (time.Time, error) {
	var at *time.Time
	if err := r.pool.QueryRow(ctx,
		"SELECT max(created_at) FROM agent_reviews WHERE agent_id=$1", agentID).Scan(&at); err != nil {
		return time.Time{}, err
	}
	if at == nil {
		return time.Time{}, nil
	}
	return *at, nil
}
