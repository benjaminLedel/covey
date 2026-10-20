package push

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"covey/internal/workflow"
)

// What waits for a person's decision is news too (#622): a guard rail's
// approval (spec/06), an open point of a review (spec/21) and a workflow's
// approval step (#564). None of them is a conversation message, so the
// notifier reads them from where they are written down — approvals,
// improvement_items, workflow_runs — in the same round, under the same
// cursor and the same push_sent ledger as the messages.
//
// The recipients are the people who may decide, not a conversation's
// members: for an approval or an open point the agent's owner when their
// seat may decide, and the organisation's admins (and security, where an
// open point touches access) — the same people a configuration proposal
// names as its approvers; for a workflow step the person the step names.

// Kinds of a decision's notification. They have their own words (texts.go)
// and the question's sound.
const (
	KindApproval = "approval"
	KindProposal = "proposal" // a configuration change drafted from the chat (#491)
	KindReview   = "review"   // an open point of a review
	KindDecision = "decision" // a workflow's approval step
)

const (
	sourceApproval = "approval"
	sourceItem     = "item"
	sourceWorkflow = "workflow"
)

// decisionLookback reaches behind the cursor: a workflow run is written by
// the engine after it reached its step, and a row whose transaction
// committed late must not slip past. push_sent keeps the overlap from
// repeating anything.
const decisionLookback = 10 * time.Minute

// The seat roles that may decide an approval or an open point
// (internal/httpapi: manage and security), and the narrower set that
// accepts what touches ACCESS.md or EGRESS.md. platform_admin is the old
// name of org_admin.
var (
	decidingRoles   = []string{"org_admin", "platform_admin", "agent_owner", "security"}
	restrictedRoles = []string{"org_admin", "platform_admin", "security"}
	adminRoles      = []string{"org_admin", "platform_admin"}
)

// claimDecisions reads the decisions that started waiting in the round's
// window and are not yet announced.
func (n *Notifier) claimDecisions(ctx context.Context, tx pgx.Tx, from, to time.Time) ([]event, error) {
	back := from.Add(-decisionLookback)
	var evs []event

	rows, err := tx.Query(ctx, `SELECT 'apr:' || ap.id, ap.org_id, ap.agent_id, ap.requested_at, ap.action,
	       a.display_name, o.push_preview
	  FROM approvals ap
	  JOIN agents a ON a.id = ap.agent_id
	  JOIN organizations o ON o.id = ap.org_id
	 WHERE ap.status = 'pending' AND ap.requested_at > $1 AND ap.requested_at <= $2
	   AND NOT EXISTS (SELECT 1 FROM push_sent s WHERE s.key = 'apr:' || ap.id)`, back, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		e := event{source: sourceApproval, kind: KindApproval, authorKind: "agent"}
		var agent uuid.UUID
		if err := rows.Scan(&e.key, &e.orgID, &agent, &e.at, &e.text, &e.name, &e.preview); err != nil {
			rows.Close()
			return nil, err
		}
		e.agent, e.agentID = &agent, agent.String()
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	/* An open point; one drafted from a chat message (#491) is that
	   conversation's config_proposal message too, which its members hear
	   about already — the approvers outside it hear it from here. */
	rows, err = tx.Query(ctx, `SELECT 'imp:' || i.id, i.org_id, i.agent_id, i.created_at, i.title,
	       a.display_name, o.push_preview, i.origin_message_id IS NOT NULL,
	       (i.files ? 'ACCESS.md' OR i.files ? 'EGRESS.md'), m.conversation_id
	  FROM improvement_items i
	  JOIN agents a ON a.id = i.agent_id
	  JOIN organizations o ON o.id = i.org_id
	  LEFT JOIN conversation_messages m ON m.id = i.origin_message_id
	 WHERE i.status = 'pending' AND i.created_at > $1 AND i.created_at <= $2
	   AND NOT EXISTS (SELECT 1 FROM push_sent s WHERE s.key = 'imp:' || i.id)`, back, to)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		e := event{source: sourceItem, kind: KindReview, authorKind: "agent"}
		var agent uuid.UUID
		var fromChat bool
		if err := rows.Scan(&e.key, &e.orgID, &agent, &e.at, &e.text, &e.name, &e.preview, &fromChat,
			&e.restricted, &e.origin); err != nil {
			rows.Close()
			return nil, err
		}
		if fromChat {
			e.kind = KindProposal
		}
		e.agent, e.agentID = &agent, agent.String()
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	wf, err := n.claimWorkflowSteps(ctx, tx, back, to)
	if err != nil {
		return nil, err
	}
	return append(evs, wf...), nil
}

// claimWorkflowSteps reads the runs that wait at an approval step. Which
// step is one, and whom it names, is the definition's — the stored text,
// read the way the engine reads it. A run is announced once per step it
// waits at: the key carries the step and since when.
func (n *Notifier) claimWorkflowSteps(ctx context.Context, tx pgx.Tx, back, to time.Time) ([]event, error) {
	rows, err := tx.Query(ctx, `SELECT r.id, r.step, r.since, r.case_ref, r.vars, w.org_id, w.slug, v.source,
	       o.push_preview
	  FROM workflow_runs r
	  JOIN workflows w ON w.id = r.workflow_id
	  JOIN workflow_versions v ON v.workflow_id = w.id AND v.version = w.version
	  JOIN organizations o ON o.id = w.org_id
	 WHERE r.state = 'waiting' AND r.updated_at > $1 AND r.updated_at <= $2`, back, to)
	if err != nil {
		return nil, err
	}
	type run struct {
		id, step, caseRef, slug, source string
		since                           time.Time
		vars                            []byte
		org                             uuid.UUID
		preview                         bool
	}
	var runs []run
	for rows.Next() {
		var r run
		var id uuid.UUID
		if err := rows.Scan(&id, &r.step, &r.since, &r.caseRef, &r.vars, &r.org, &r.slug, &r.source, &r.preview); err != nil {
			rows.Close()
			return nil, err
		}
		r.id = id.String()
		runs = append(runs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	defs := map[string]*workflow.Definition{}
	var evs []event
	for _, r := range runs {
		key := fmt.Sprintf("wf:%s:%s:%d", r.id, r.step, r.since.UnixMicro())
		var sent bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM push_sent WHERE key = $1)`, key).Scan(&sent); err != nil {
			return nil, err
		}
		if sent {
			continue
		}
		def, ok := defs[r.source]
		if !ok {
			// A definition that does not parse has no step to announce.
			def, _ = workflow.Parse([]byte(r.source))
			defs[r.source] = def
		}
		e := event{key: key, source: sourceWorkflow, kind: KindDecision, authorKind: "system", orgID: r.org,
			at: r.since, preview: r.preview}
		if def != nil {
			if s := def.Steps[r.step]; s != nil && s.Kind == workflow.KindApproval {
				var vars map[string]map[string]string
				_ = json.Unmarshal(r.vars, &vars)
				e.who = s.Who
				e.name = def.Name
				e.text = fill(s.Ask, vars)
				if e.text == "" {
					e.text = r.caseRef
				}
			}
		}
		if e.name == "" {
			e.name = r.slug
		}
		// Marked as sent either way: a step that asks nobody is not read
		// again every round.
		evs = append(evs, e)
	}
	return evs, nil
}

// fill puts a run's values into a question's {{step.field}}, as the
// engine did when it asked.
func fill(text string, vars map[string]map[string]string) string {
	for step, fields := range vars {
		for k, v := range fields {
			text = strings.ReplaceAll(text, "{{"+step+"."+k+"}}", v)
		}
	}
	return text
}

// deciders are the people who may decide what ev announces and have a
// device to hear it on.
func (n *Notifier) deciders(ctx context.Context, ev event) ([]uuid.UUID, error) {
	var rows pgx.Rows
	var err error
	switch ev.source {
	case sourceWorkflow:
		who := strings.ToLower(strings.TrimSpace(ev.who))
		if who == "" {
			return nil, nil
		}
		// The person the step names — by e-mail, by the part of it before
		// the @, or by display name (httpapi's named).
		rows, err = n.Pool.Query(ctx, `SELECT h.id FROM humans h
			 WHERE h.org_id = $1
			   AND (lower(h.email) = $2 OR split_part(lower(h.email), '@', 1) = $2 OR lower(trim(h.display_name)) = $2)
			   AND EXISTS (SELECT 1 FROM push_devices d WHERE d.human_id = h.id)`, ev.orgID, who)
	default:
		admins, owners := adminRoles, decidingRoles
		if ev.restricted {
			admins, owners = restrictedRoles, restrictedRoles
		}
		/* The owner when their seat may decide, and the admins. Who is a
		   member of the conversation a proposal was drafted in hears of it
		   from its message already. */
		rows, err = n.Pool.Query(ctx, `SELECT h.id FROM humans h
			 WHERE h.org_id = $1
			   AND (h.role = ANY($2) OR (h.id = (SELECT owner_id FROM agents WHERE id = $3) AND h.role = ANY($4)))
			   AND EXISTS (SELECT 1 FROM push_devices d WHERE d.human_id = h.id)
			   AND NOT EXISTS (SELECT 1 FROM conversation_members cm
			        WHERE cm.conversation_id = $5 AND cm.member_kind = 'human' AND cm.member_id = h.id
			          AND cm.left_at IS NULL)`, ev.orgID, admins, ev.agent, owners, ev.origin)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// soundKind is the kind whose sound a notification plays: the app's bundle
// carries one per question, answer, result and error, and a decision that
// waits sounds like the question it is.
func soundKind(kind string) string {
	switch kind {
	case KindApproval, KindProposal, KindReview, KindDecision, "config_proposal":
		return "question"
	case "question", "answer", "result", "error":
		return kind
	}
	return "answer"
}
