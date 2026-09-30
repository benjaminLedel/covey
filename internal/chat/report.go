package chat

import (
	"context"
	"strconv"
	"time"

	"github.com/google/uuid"
)

/* What the backlog says into a conversation (#440) — and it is only this.
 *
 * A task opened from a conversation (backlog_tasks.conversation_id) reports
 * its result or its error there, and its question when it parks. A task that
 * came from anywhere else — the backlog by hand, a webhook, Jira, Zendesk, a
 * heartbeat — reports nothing into any conversation, with one exception: a
 * question it parks on goes to the direct conversation between the agent and
 * its supervisor, because somebody has to answer it. An agent whose
 * supervisor is another agent, or who has none, has nobody to ask in a
 * conversation; the question stays on the task, visible in the backlog and
 * in the inbox.
 *
 * Both are read as due work from the database, not handed over by the run
 * that ends: a restart between the end of a run and the report must not lose
 * the report. The message ids are derived from what they report, so a round
 * that runs twice writes each report once. */

// reportSpace is the namespace the ids of reports are derived in.
var reportSpace = uuid.MustParse("5f1b3c0e-7a52-4b8e-9c1d-2e0f6a4d8b17")

// ReportID is the id of the message that reports a task's outcome. One per
// task and outcome: a task that failed and then succeeded reports both.
func ReportID(taskID uuid.UUID, kind string) uuid.UUID {
	return uuid.NewSHA1(reportSpace, []byte(taskID.String()+":"+kind))
}

// QuestionID is the id of the message that delivers the question on one
// blocked edge.
func QuestionID(transitionID int64) uuid.UUID {
	return uuid.NewSHA1(reportSpace, []byte("question:"+strconv.FormatInt(transitionID, 10)))
}

// Report is a finished task that has not said so in its conversation yet.
type Report struct {
	TaskID, OrgID, AgentID, ConversationID uuid.UUID
	Title, Body, Origin                    string
	// State is done or failed; Outcome is the result or the error.
	State, Outcome string
	// Said is the narrated sentence (#411), SaidAt whether it was tried.
	Said   string
	Told   bool
	Triage bool
	// ChatAnswer: the task is a chat message nobody triaged (#483). Its
	// result is the reply itself and is posted as a message of the agent —
	// neither retold nor shown as a report.
	ChatAnswer bool
}

// ReportsMeta is the meta key that names which outcome a message reports
// when it is not written as that kind — the reply of a chat answer is a
// plain message and still reports the result.
const ReportsMeta = "reports"

// Message is how the report is written into the conversation: as a result
// or an error, or — for a chat answer that is done — as the agent's plain
// reply, which still says in its meta which outcome it reports, so that the
// round finds it written.
func (r Report) Message(text string, meta map[string]string) Message {
	kind := r.Kind()
	if r.ChatAnswer && kind == MessageResult {
		out := map[string]string{ReportsMeta: kind}
		for k, v := range meta {
			out[k] = v
		}
		meta, kind = out, MessageText
	}
	return Message{
		ID: ReportID(r.TaskID, r.Kind()), ConversationID: r.ConversationID,
		AuthorKind: MemberAgent, AuthorID: &r.AgentID, Text: text, Kind: kind, TaskID: &r.TaskID,
		Meta: meta,
	}
}

// Kind is the message kind the report is written as.
func (r Report) Kind() string {
	if r.State == "failed" {
		return MessageError
	}
	return MessageResult
}

// DueReports reads the finished tasks of conversations that have not
// reported, within the window.
func (s *Store) DueReports(ctx context.Context, window time.Duration, limit int) ([]Report, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.org_id, t.agent_id, t.conversation_id, t.title, coalesce(t.body, ''), t.origin, t.state,
		       CASE WHEN t.state = 'done' THEN coalesce(t.result, '') ELSE coalesce(t.error, '') END,
		       coalesce(t.said, ''), t.said_at IS NOT NULL, o.chat_triage = 'on', t.chat_answer
		  FROM backlog_tasks t JOIN organizations o ON o.id = t.org_id
		 WHERE t.conversation_id IS NOT NULL AND t.state IN ('done', 'failed') AND t.archived_at IS NULL
		   AND t.updated_at > now() - make_interval(secs => $1)
		   AND CASE WHEN t.state = 'done' THEN coalesce(t.result, '') ELSE coalesce(t.error, '') END <> ''
		   AND NOT EXISTS (SELECT 1 FROM conversation_messages m
		                    WHERE m.task_id = t.id
		                      AND CASE WHEN t.state = 'done' THEN 'result' ELSE 'error' END IN (m.kind, m.meta->>'reports'))
		 ORDER BY t.updated_at
		 LIMIT $2`, window.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Report
	for rows.Next() {
		var r Report
		if err := rows.Scan(&r.TaskID, &r.OrgID, &r.AgentID, &r.ConversationID, &r.Title, &r.Body, &r.Origin,
			&r.State, &r.Outcome, &r.Said, &r.Told, &r.Triage, &r.ChatAnswer); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Question is a parked task's question that has not been delivered yet.
type Question struct {
	// TransitionID is the blocked edge the question stands on.
	TransitionID           int64
	TaskID, OrgID, AgentID uuid.UUID
	Text                   string
	// ConversationID is set for a task from a conversation; Supervisor, the
	// agent's human supervisor, for every other.
	ConversationID *uuid.UUID
	Supervisor     *uuid.UUID
	At             time.Time
}

// DueQuestions reads the questions of the window that have somewhere to go
// and have not gone there: from a task of a conversation, or from any other
// task of an agent with a human supervisor. Questions from before the
// organisation's conversations began are not delivered.
func (s *Store) DueQuestions(ctx context.Context, window time.Duration, limit int) ([]Question, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tr.id, t.id, t.org_id, t.agent_id,
		       trim(regexp_replace(coalesce(tr.note, ''), '^blocked:', '')),
		       t.conversation_id,
		       CASE WHEN t.conversation_id IS NULL THEN h.id END,
		       tr.created_at
		  FROM task_transitions tr
		  JOIN backlog_tasks t ON t.id = tr.task_id
		  JOIN organizations o ON o.id = t.org_id
		  JOIN agents a ON a.id = t.agent_id
		  LEFT JOIN humans h ON h.id = a.supervisor_id AND h.org_id = t.org_id
		 WHERE tr.to_state = 'blocked'
		   AND tr.created_at > now() - make_interval(secs => $1)
		   AND tr.created_at >= o.conversations_since
		   AND t.archived_at IS NULL
		   AND (t.conversation_id IS NOT NULL OR h.id IS NOT NULL)
		   AND trim(regexp_replace(coalesce(tr.note, ''), '^blocked:', '')) <> ''
		   AND NOT EXISTS (SELECT 1 FROM conversation_messages m
		                    WHERE m.task_id = t.id AND m.kind = 'question' AND m.meta->>'transition' = tr.id::text)
		 ORDER BY tr.created_at
		 LIMIT $2`, window.Seconds(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Question
	for rows.Next() {
		var q Question
		if err := rows.Scan(&q.TransitionID, &q.TaskID, &q.OrgID, &q.AgentID, &q.Text,
			&q.ConversationID, &q.Supervisor, &q.At); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
