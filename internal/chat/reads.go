package chat

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// What a person has not read yet (#378). A thread is the organisation's,
// shared by everyone who talks to the agent; how far each person has read it
// is their own, one row per person and agent.
//
// Unread is what came from the agent's side — its answers, its notes, its
// questions, a result, an error — after that point. What people wrote is
// never unread: somebody said it, and whoever reads the thread next sees it
// between the agent's lines anyway.

// ThreadState is one agent's thread as a person's list shows it.
type ThreadState struct {
	AgentID uuid.UUID `json:"agent_id"`
	Unread  int       `json:"unread"`
	// The newest entry from the agent's side, read or not.
	LastAt   time.Time `json:"last_at"`
	LastText string    `json:"last_text"`
	LastKind string    `json:"last_kind"`
}

// Threads answers for every agent of the organisation that has said
// anything how much of it the person has not read. Without a row the
// baseline is when the person's seat was created: a new colleague does not
// inherit the organisation's whole history as unread.
func (s *Store) Threads(ctx context.Context, orgID, humanID uuid.UUID) ([]ThreadState, error) {
	// What the platform starts on its own counts as nothing unread (#410),
	// the same rule the thread follows; a question it asks still does.
	rows, err := s.pool.Query(ctx, `WITH RECURSIVE `+MachineryCTE("org_id = $1")+`, ev AS (
		SELECT m.agent_id, m.created_at AS at, m.text, 'answer' AS kind
		  FROM chat_messages m WHERE m.org_id = $1 AND m.author = 'agent'
		UNION ALL
		SELECT t.agent_id, n.created_at, n.content, 'note'
		  FROM task_notes n JOIN backlog_tasks t ON t.id = n.task_id
		 WHERE t.org_id = $1 AND t.archived_at IS NULL AND n.author = 'agent'
		   AND t.id NOT IN (SELECT id FROM maschinerie)
		UNION ALL
		SELECT t.agent_id, tr.created_at, coalesce(tr.note, ''), 'question'
		  FROM task_transitions tr JOIN backlog_tasks t ON t.id = tr.task_id
		 WHERE t.org_id = $1 AND t.archived_at IS NULL AND tr.to_state = 'blocked'
		UNION ALL
		SELECT t.agent_id, t.updated_at, coalesce(nullif(t.said, ''), t.result), 'result'
		  FROM backlog_tasks t
		 WHERE t.org_id = $1 AND t.archived_at IS NULL AND t.state = 'done' AND coalesce(t.result, '') <> ''
		   AND t.id NOT IN (SELECT id FROM maschinerie)
		UNION ALL
		SELECT t.agent_id, t.updated_at, coalesce(nullif(t.said, ''), t.error), 'error'
		  FROM backlog_tasks t
		 WHERE t.org_id = $1 AND t.archived_at IS NULL AND t.state = 'failed' AND coalesce(t.error, '') <> ''
		   AND t.id NOT IN (SELECT id FROM maschinerie)
	), seen AS (
		SELECT ev.*, coalesce(r.read_at, h.created_at) AS since
		  FROM ev
		  JOIN humans h ON h.id = $2
		  LEFT JOIN chat_reads r ON r.human_id = $2 AND r.agent_id = ev.agent_id
	)
	SELECT DISTINCT ON (agent_id) agent_id,
	       count(*) FILTER (WHERE at > since) OVER (PARTITION BY agent_id),
	       at, text, kind
	  FROM seen
	 ORDER BY agent_id, at DESC`, orgID, humanID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ThreadState{}
	for rows.Next() {
		var t ThreadState
		if err := rows.Scan(&t.AgentID, &t.Unread, &t.LastAt, &t.LastText, &t.LastKind); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// MarkRead moves the person's point in the agent's thread to at — forward
// only, and never past now. The reader sends the time of the newest entry it
// has shown, so an answer that arrives in between stays unread.
func (s *Store) MarkRead(ctx context.Context, humanID, agentID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO chat_reads (human_id, agent_id, read_at)
		VALUES ($1, $2, least($3::timestamptz, now()))
		ON CONFLICT (human_id, agent_id)
		DO UPDATE SET read_at = greatest(chat_reads.read_at, excluded.read_at)`, humanID, agentID, at)
	return err
}
