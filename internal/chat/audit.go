package chat

import (
	"context"
	"time"

	"github.com/google/uuid"
)

/* The audit's view of the conversations (#440): auditor and org admin read
 * and export them, like the rest of what an agent did. Nobody else reads a
 * conversation they are not in. The threads from before #440 — one per
 * agent, shared by everybody (chat_messages) — are readable only here; they
 * belong to no conversation. */

// AuditEntry is one conversation in the audit's list.
type AuditEntry struct {
	Conversation
	Messages int `json:"messages"`
}

// AuditList reads the organisation's conversations, newest first,
// optionally only those an agent or a person is (or was) in.
func (s *Store) AuditList(ctx context.Context, orgID uuid.UUID, member *Ref, limit int) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 500
	}
	var kind *string
	var id *uuid.UUID
	if member != nil {
		kind, id = &member.Kind, &member.ID
	}
	rows, err := s.pool.Query(ctx, `SELECT c.id,
		       (SELECT count(*) FROM conversation_messages m WHERE m.conversation_id = c.id)
		  FROM conversations c
		 WHERE c.org_id = $1
		   AND ($2::text IS NULL OR EXISTS (SELECT 1 FROM conversation_members cm
		        WHERE cm.conversation_id = c.id AND cm.member_kind = $2 AND cm.member_id = $3))
		 ORDER BY c.last_message_at DESC LIMIT $4`, orgID, kind, id, limit)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	zahl := map[uuid.UUID]int{}
	for rows.Next() {
		var cid uuid.UUID
		var n int
		if err := rows.Scan(&cid, &n); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, cid)
		zahl[cid] = n
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []AuditEntry{}
	if len(ids) == 0 {
		return out, nil
	}
	convs, err := s.many(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, cid := range ids {
		out = append(out, AuditEntry{Conversation: convs[cid], Messages: zahl[cid]})
	}
	return out, nil
}

// All reads every message of a conversation, oldest first — for the audit's
// export, which is the one reader that wants all of it.
func (s *Store) All(ctx context.Context, id uuid.UUID) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+messageCols+` FROM conversation_messages m`+messageJoins+`
		 WHERE m.conversation_id = $1 ORDER BY m.created_at, m.id`, id)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// LegacyThread is one agent's shared thread from before #440.
type LegacyThread struct {
	AgentID   uuid.UUID `json:"agent_id"`
	AgentName string    `json:"agent_name"`
	Messages  int       `json:"messages"`
	FirstAt   time.Time `json:"first_at"`
	LastAt    time.Time `json:"last_at"`
}

// LegacyThreads lists the shared threads of the organisation's agents.
func (s *Store) LegacyThreads(ctx context.Context, orgID uuid.UUID, agentID *uuid.UUID) ([]LegacyThread, error) {
	rows, err := s.pool.Query(ctx, `SELECT m.agent_id, coalesce(a.display_name, ''), count(*), min(m.created_at), max(m.created_at)
		  FROM chat_messages m LEFT JOIN agents a ON a.id = m.agent_id
		 WHERE m.org_id = $1 AND ($2::uuid IS NULL OR m.agent_id = $2)
		 GROUP BY m.agent_id, a.display_name
		 ORDER BY max(m.created_at) DESC`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LegacyThread{}
	for rows.Next() {
		var l LegacyThread
		if err := rows.Scan(&l.AgentID, &l.AgentName, &l.Messages, &l.FirstAt, &l.LastAt); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LegacyMessage is a line of a shared thread, as it was written: the author
// is 'chat:<mail>' or 'agent'.
type LegacyMessage struct {
	ID        uuid.UUID  `json:"id"`
	AgentID   uuid.UUID  `json:"agent_id"`
	Author    string     `json:"author"`
	Text      string     `json:"text"`
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

// LegacyMessages reads one agent's shared thread, oldest first.
func (s *Store) LegacyMessages(ctx context.Context, orgID, agentID uuid.UUID) ([]LegacyMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id, agent_id, author, text, task_id, created_at
		  FROM chat_messages WHERE org_id = $1 AND agent_id = $2 ORDER BY created_at, id`, orgID, agentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LegacyMessage{}
	for rows.Next() {
		var m LegacyMessage
		if err := rows.Scan(&m.ID, &m.AgentID, &m.Author, &m.Text, &m.TaskID, &m.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
