package push

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/chat"
)

// Notifier finds what came from the agents' side since its last round and
// tells the people it concerns.
//
// It reads the same sources as the thread, not an event stream: a
// notification is a view on what is already written down, and a restart
// neither loses nor repeats one. The cursor row, locked with SKIP LOCKED,
// makes one process do a round at a time; push_sent makes an entry go out
// at most once, even when a task's updated_at moves again later.
type Notifier struct {
	Pool   *pgxpool.Pool
	Sender Sender
	Log    *slog.Logger
	// Every is the pause between rounds; Lag keeps a round away from rows
	// whose transaction may not have committed yet.
	Every time.Duration
	Lag   time.Duration

	lastPurge time.Time
}

// Run does rounds until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	every := n.Every
	if every == 0 {
		every = 5 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		if _, err := n.Round(ctx); err != nil && ctx.Err() == nil && n.Log != nil {
			n.Log.Warn("push: round failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type event struct {
	key     string
	orgID   uuid.UUID
	agentID uuid.UUID
	taskID  *uuid.UUID
	at      time.Time
	text    string
	kind    string
	name    string
	preview bool
}

// Round pushes what is new and answers how many notifications went out.
func (n *Notifier) Round(ctx context.Context) (int, error) {
	evs, err := n.claim(ctx)
	if err != nil || len(evs) == 0 {
		return 0, err
	}
	sent := 0
	threads := chat.New(n.Pool)
	for _, ev := range evs {
		humans, err := n.recipients(ctx, ev)
		if err != nil {
			return sent, err
		}
		for _, h := range humans {
			devices, err := n.devices(ctx, h)
			if err != nil || len(devices) == 0 {
				continue
			}
			badge := 0
			if ts, err := threads.Threads(ctx, ev.orgID, h); err == nil {
				for _, t := range ts {
					badge += t.Unread
				}
			}
			for _, d := range devices {
				title, body := Compose(d.lang, ev.kind, ev.name, FirstLine(ev.text, MaxBody), ev.preview)
				m := Message{
					Token: d.token, Environment: d.env, Title: title, Body: body, Badge: badge,
					AgentID: ev.agentID.String(), Sound: SoundFor(d.sound, ev.kind),
				}
				switch err := n.Sender.Send(ctx, m); {
				case errors.Is(err, ErrGone):
					_, _ = n.Pool.Exec(ctx, `DELETE FROM push_devices WHERE token=$1`, d.token)
				case err != nil:
					if n.Log != nil {
						n.Log.Warn("push: not delivered", "kind", ev.kind, "err", err)
					}
				default:
					sent++
				}
			}
		}
	}
	return sent, nil
}

// claim takes the entries since the cursor, marks them sent and moves the
// cursor, in one transaction. Sending happens after it: a notification that
// fails is lost rather than repeated.
func (n *Notifier) claim(ctx context.Context) ([]event, error) {
	tx, err := n.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var from time.Time
	err = tx.QueryRow(ctx, `SELECT at FROM push_cursor WHERE id=1 FOR UPDATE SKIP LOCKED`).Scan(&from)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil // another process is doing this round
	}
	if err != nil {
		return nil, err
	}
	lag := n.Lag
	if lag == 0 {
		lag = 3 * time.Second
	}
	var to time.Time
	if err := tx.QueryRow(ctx, `SELECT now() - make_interval(secs => $1)`, lag.Seconds()).Scan(&to); err != nil {
		return nil, err
	}
	if !to.After(from) {
		return nil, nil
	}
	// Results and errors of tasks that came from somebody's message; a
	// schedule's hourly run is nobody's news. Questions whatever their
	// origin: they hold an agent still until a person answers.
	rows, err := tx.Query(ctx, `WITH ev AS (
		SELECT 'answer:' || m.id AS key, m.org_id, m.agent_id, NULL::uuid AS task_id, m.created_at AS at, m.text, 'answer' AS kind
		  FROM chat_messages m
		 WHERE m.author = 'agent' AND m.created_at > $1 AND m.created_at <= $2
		UNION ALL
		SELECT 'question:' || tr.id, t.org_id, t.agent_id, t.id, tr.created_at, coalesce(tr.note, ''), 'question'
		  FROM task_transitions tr JOIN backlog_tasks t ON t.id = tr.task_id
		 WHERE tr.to_state = 'blocked' AND tr.created_at > $1 AND tr.created_at <= $2
		UNION ALL
		/* With the triage on, a chat task's result is told in the chat
		   (#411): the notification waits for that and carries the sentence,
		   not the report. said_at is set even when there was nothing to tell
		   with, and then the report goes out as before. */
		SELECT 'result:' || t.id, t.org_id, t.agent_id, t.id,
		       CASE WHEN o2.chat_triage = 'on' THEN t.said_at ELSE t.updated_at END,
		       coalesce(nullif(t.said, ''), t.result), 'result'
		  FROM backlog_tasks t JOIN organizations o2 ON o2.id = t.org_id
		 WHERE t.state = 'done' AND coalesce(t.result, '') <> '' AND t.origin LIKE 'chat:%'
		   AND CASE WHEN o2.chat_triage = 'on' THEN t.said_at ELSE t.updated_at END > $1
		   AND CASE WHEN o2.chat_triage = 'on' THEN t.said_at ELSE t.updated_at END <= $2
		UNION ALL
		SELECT 'error:' || t.id, t.org_id, t.agent_id, t.id,
		       CASE WHEN o2.chat_triage = 'on' THEN t.said_at ELSE t.updated_at END,
		       coalesce(nullif(t.said, ''), t.error), 'error'
		  FROM backlog_tasks t JOIN organizations o2 ON o2.id = t.org_id
		 WHERE t.state = 'failed' AND coalesce(t.error, '') <> '' AND t.origin LIKE 'chat:%'
		   AND CASE WHEN o2.chat_triage = 'on' THEN t.said_at ELSE t.updated_at END > $1
		   AND CASE WHEN o2.chat_triage = 'on' THEN t.said_at ELSE t.updated_at END <= $2
	)
	SELECT ev.key, ev.org_id, ev.agent_id, ev.task_id, ev.at, ev.text, ev.kind, a.display_name, o.push_preview
	  FROM ev JOIN agents a ON a.id = ev.agent_id JOIN organizations o ON o.id = ev.org_id
	 WHERE NOT EXISTS (SELECT 1 FROM push_sent s WHERE s.key = ev.key)
	 ORDER BY ev.at`, from, to)
	if err != nil {
		return nil, err
	}
	var evs []event
	for rows.Next() {
		var e event
		if err := rows.Scan(&e.key, &e.orgID, &e.agentID, &e.taskID, &e.at, &e.text, &e.kind, &e.name, &e.preview); err != nil {
			rows.Close()
			return nil, err
		}
		evs = append(evs, e)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, e := range evs {
		if _, err := tx.Exec(ctx, `INSERT INTO push_sent (key) VALUES ($1) ON CONFLICT DO NOTHING`, e.key); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE push_cursor SET at=$1 WHERE id=1`, to); err != nil {
		return nil, err
	}
	if time.Since(n.lastPurge) > time.Hour {
		if _, err := tx.Exec(ctx, `DELETE FROM push_sent WHERE at < now() - interval '30 days'`); err != nil {
			return nil, err
		}
		n.lastPurge = time.Now()
	}
	return evs, tx.Commit(ctx)
}

// recipients are the people an entry concerns and who have not read it
// yet: whoever wrote in the agent's thread in the last two weeks (for an
// answer or a question), the person a task came from, and for a question the
// agent's human supervisor.
func (n *Notifier) recipients(ctx context.Context, ev event) ([]uuid.UUID, error) {
	conversation := ev.kind == "answer" || ev.kind == "question"
	rows, err := n.Pool.Query(ctx, `SELECT h.id FROM humans h
		 WHERE h.org_id = $1
		   AND (
		        ($4 AND 'chat:' || h.email IN (
		            SELECT author FROM chat_messages
		             WHERE agent_id = $2 AND author LIKE 'chat:%' AND created_at > now() - interval '14 days'))
		     OR ($3::uuid IS NOT NULL AND 'chat:' || h.email = (SELECT origin FROM backlog_tasks WHERE id = $3))
		     OR ($5 = 'question' AND h.id = (SELECT supervisor_id FROM agents WHERE id = $2))
		   )
		   AND NOT EXISTS (SELECT 1 FROM chat_reads r
		                    WHERE r.human_id = h.id AND r.agent_id = $2 AND r.read_at >= $6)
		   AND EXISTS (SELECT 1 FROM push_devices d WHERE d.human_id = h.id)`,
		ev.orgID, ev.agentID, ev.taskID, conversation, ev.kind, ev.at)
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

type device struct{ token, env, lang, sound string }

func (n *Notifier) devices(ctx context.Context, human uuid.UUID) ([]device, error) {
	rows, err := n.Pool.Query(ctx, `SELECT token, environment, lang, sound FROM push_devices WHERE human_id=$1`, human)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []device
	for rows.Next() {
		var d device
		if err := rows.Scan(&d.token, &d.env, &d.lang, &d.sound); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// Register keeps a device's token for the person; a token that moves to
// another seat (a shared phone, somebody signed in anew) goes with it.
func Register(ctx context.Context, pool *pgxpool.Pool, human uuid.UUID, token, platform, env, lang, sound string) error {
	_, err := pool.Exec(ctx, `INSERT INTO push_devices (token, human_id, platform, environment, lang, sound)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (token) DO UPDATE SET human_id = excluded.human_id, platform = excluded.platform,
		    environment = excluded.environment, lang = excluded.lang, sound = excluded.sound, seen_at = now()`,
		token, human, platform, env, lang, sound)
	return err
}

// Unregister forgets the person's device.
func Unregister(ctx context.Context, pool *pgxpool.Pool, human uuid.UUID, token string) error {
	_, err := pool.Exec(ctx, `DELETE FROM push_devices WHERE token=$1 AND human_id=$2`, token, human)
	return err
}
