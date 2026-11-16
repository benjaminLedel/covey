package httpapi

/* What a task from a conversation shows, and to whom (spec/28, section 3).

   A message to an agent becomes a task, and the task carries the message, the
   conversation before it (gespraechFuerLauf) and, when it is done, the agent's
   reply. That is the conversation's content in a second place. Only members
   read a conversation; the audit roles read it in the audit; nobody reads it
   because they may manage the agent. The backlog is open to every role of the
   organisation, so the rule has to travel with the copy: wherever the API
   hands out a task's text, a task whose conversation the reader is not in
   comes without it (backlog.Task.Redact) — still on the board, with its state,
   origin and times, so the board stays whole. */

import (
	"context"
	"slices"

	"github.com/google/uuid"

	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/identity"
)

// gespraechsLeser answers, for one reader and one request, whether they read
// the conversation a task came from. It remembers each conversation it looked
// at: a board holds many tasks from few conversations.
type gespraechsLeser struct {
	s     *Server
	p     identity.Principal
	audit bool
	known map[uuid.UUID]bool
}

func (s *Server) gespraechsLeser(p identity.Principal) *gespraechsLeser {
	return &gespraechsLeser{s: s, p: p,
		audit: slices.Contains(auditConversationRoles(), p.Role),
		known: map[uuid.UUID]bool{}}
}

// liest: a task without a conversation is the backlog's own and readable by
// whoever reads the backlog; one from a conversation only by its members and
// the audit roles. A conversation that cannot be read is not readable.
func (l *gespraechsLeser) liest(ctx context.Context, conv *uuid.UUID) bool {
	if conv == nil || l.audit {
		return true
	}
	if v, ok := l.known[*conv]; ok {
		return v
	}
	v := false
	if l.s.Chat != nil {
		c, err := l.s.Chat.Get(ctx, *conv)
		v = err == nil && c.OrgID == l.p.OrgID && c.Has(chat.Human(l.p.ID))
	}
	l.known[*conv] = v
	return v
}

// schwaerzen redacts, in place, every task the reader may not read.
func (l *gespraechsLeser) schwaerzen(ctx context.Context, tasks []backlog.Task) {
	for i := range tasks {
		if !l.liest(ctx, tasks[i].ConversationID) {
			tasks[i].Redact()
		}
	}
}

// lesbare keeps only the tasks the reader may read. For a search: a redacted
// hit would still say that the words stand in a conversation the reader is
// not in.
func (l *gespraechsLeser) lesbare(ctx context.Context, tasks []backlog.Task) []backlog.Task {
	out := tasks[:0]
	for _, t := range tasks {
		if l.liest(ctx, t.ConversationID) {
			out = append(out, t)
		}
	}
	return out
}

// aufgabeLesbar loads the conversation of one task by id and answers for it.
// A task that cannot be loaded is not readable.
func (l *gespraechsLeser) aufgabeLesbar(ctx context.Context, taskID uuid.UUID) bool {
	t, err := l.s.Backlog.Get(ctx, taskID)
	if err != nil {
		return false
	}
	return l.liest(ctx, t.ConversationID)
}

// gespraecheVon maps task ids to the conversation each came from, for the
// reads that carry only a task's id and title (the thread's open tasks, the
// work record). Tasks without a conversation are left out.
func (s *Server) gespraecheVon(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]uuid.UUID, error) {
	out := map[uuid.UUID]uuid.UUID{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT id, conversation_id FROM backlog_tasks
		WHERE id = ANY($1) AND conversation_id IS NOT NULL`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, conv uuid.UUID
		if err := rows.Scan(&id, &conv); err != nil {
			return nil, err
		}
		out[id] = conv
	}
	return out, rows.Err()
}
