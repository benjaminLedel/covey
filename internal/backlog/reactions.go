package backlog

// Reactions on a task.
//
// They hang on the TASK and not on a single entry of the thread, and that is
// not a simplification: a thread's entries are derived — a message IS the
// task, a question is a transition, a result is a column. The task is the
// only one of them with an identifier that is still the same tomorrow.
//
// The author is the same string as on a note ("agent", "human:<mail>"), so
// that "who reacted" has one shape everywhere in this package.

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Reaction is one mark by one author. The surface groups them; the store
// deliberately does not, because "who" is the half that gets lost in a count
// and it is the half somebody asks about.
type Reaction struct {
	TaskID    uuid.UUID `json:"task_id"`
	Emoji     string    `json:"emoji"`
	Author    string    `json:"author"`
	CreatedAt time.Time `json:"created_at"`
}

// ReactionMax caps how many marks one task can carry. Without it a thread is
// one script away from a row of four hundred emoji, and the limit belongs
// here rather than in the surface — a second client would not know about it.
const ReactionMax = 60

// React toggles one mark: if this author already set this emoji on this task
// it goes away, otherwise it appears. Returns whether it is set afterwards.
//
// Toggling and not "add" is what a reaction is: the same click takes it back,
// and a separate delete endpoint would be a second way to say the same thing.
func (s *Store) React(ctx context.Context, taskID uuid.UUID, emoji, author string) (bool, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM task_reactions WHERE task_id=$1 AND emoji=$2 AND author=$3`,
		taskID, emoji, author)
	if err != nil {
		return false, err
	}
	if tag.RowsAffected() > 0 {
		return false, nil
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM task_reactions WHERE task_id=$1`, taskID).Scan(&n); err != nil {
		return false, err
	}
	if n >= ReactionMax {
		return false, nil
	}
	/* ON CONFLICT DO NOTHING statt eines Fehlers: Zwei Klicks im selben
	   Augenblick sind keine Verletzung, sondern zwei Klicks. */
	_, err = s.pool.Exec(ctx,
		`INSERT INTO task_reactions (id, task_id, emoji, author) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (task_id, emoji, author) DO NOTHING`,
		uuid.New(), taskID, emoji, author)
	return err == nil, err
}

// MarkReaction sets a mark without taking it back — the form the platform
// itself uses. An agent that picks a task up marks it once; picking the same
// task up again after a retry must not un-mark it, which is exactly what
// React would do.
func (s *Store) MarkReaction(ctx context.Context, taskID uuid.UUID, emoji, author string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO task_reactions (id, task_id, emoji, author) VALUES ($1,$2,$3,$4)
		 ON CONFLICT (task_id, emoji, author) DO NOTHING`,
		uuid.New(), taskID, emoji, author)
	return err
}

// ReactionsByTasks reads the marks of several tasks at once — the thread asks
// for twenty and would otherwise ask twenty times.
func (s *Store) ReactionsByTasks(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]Reaction, error) {
	out := map[uuid.UUID][]Reaction{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT task_id, emoji, author, created_at FROM task_reactions
		 WHERE task_id = ANY($1) ORDER BY created_at`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r Reaction
		if err := rows.Scan(&r.TaskID, &r.Emoji, &r.Author, &r.CreatedAt); err != nil {
			return nil, err
		}
		out[r.TaskID] = append(out[r.TaskID], r)
	}
	return out, rows.Err()
}
