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
	Pool *pgxpool.Pool
	// Sender is a fixed sender; Source, where set, answers one per round, so
	// that a change of the settings applies without a restart (#431).
	Sender Sender
	Source Source
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
	key            string
	orgID          uuid.UUID
	conversationID uuid.UUID
	authorKind     string
	authorID       *uuid.UUID
	at             time.Time
	text           string
	kind           string
	name           string
	preview        bool
	// agentID is the agent of a direct conversation with one — where the
	// app opens the thread. Empty for anything else.
	agentID string
}

// Round pushes what is new and answers how many notifications went out.
func (n *Notifier) Round(ctx context.Context) (int, error) {
	sender := n.Sender
	if n.Source != nil {
		var err error
		if sender, err = n.Source.Sender(ctx); err != nil {
			return 0, err
		}
	}
	evs, err := n.claim(ctx)
	// With push off the entries are passed over, not kept: switching it on
	// later must not deliver a backlog of old news at once.
	if err != nil || len(evs) == 0 || sender == nil {
		return 0, err
	}
	sent := 0
	conversations := chat.New(n.Pool)
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
			badge, _ := conversations.Unread(ctx, h)
			for _, d := range devices {
				title, body := Compose(d.lang, ev.kind, ev.name, FirstLine(ev.text, MaxBody), ev.preview)
				m := Message{
					Token: d.token, Platform: d.platform, Environment: d.env, Title: title, Body: body, Badge: badge,
					AgentID: ev.agentID, Sound: SoundFor(d.sound, ev.kind),
				}
				switch err := sender.Send(ctx, m); {
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
	/* Every message of a conversation is news to its other members (#440) —
	   and only messages: what a task reports back is a message in the
	   conversation it came from, and the rest of the backlog is in no
	   conversation and pushes nothing. A told result (#411) is written only
	   once it is told, so the notification carries the sentence.

	   The kind a notification says is the message's; a line somebody wrote
	   reads as "replied", whoever wrote it. */
	rows, err := tx.Query(ctx, `SELECT 'msg:' || m.id, m.org_id, m.conversation_id, m.author_kind, m.author_id,
	       m.created_at, m.text, CASE WHEN m.kind = 'text' THEN 'answer' ELSE m.kind END,
	       coalesce(h.display_name, a.display_name, ''), o.push_preview,
	       CASE WHEN c.kind = 'direct' THEN (SELECT cm.member_id::text FROM conversation_members cm
	            WHERE cm.conversation_id = c.id AND cm.member_kind = 'agent' LIMIT 1) END
	  FROM conversation_messages m
	  JOIN conversations c ON c.id = m.conversation_id
	  JOIN organizations o ON o.id = m.org_id
	  LEFT JOIN humans h ON m.author_kind = 'human' AND h.id = m.author_id
	  LEFT JOIN agents a ON m.author_kind = 'agent' AND a.id = m.author_id
	 WHERE m.created_at > $1 AND m.created_at <= $2
	   AND NOT EXISTS (SELECT 1 FROM push_sent s WHERE s.key = 'msg:' || m.id)
	 ORDER BY m.created_at`, from, to)
	if err != nil {
		return nil, err
	}
	var evs []event
	for rows.Next() {
		var e event
		var agent *string
		if err := rows.Scan(&e.key, &e.orgID, &e.conversationID, &e.authorKind, &e.authorID, &e.at, &e.text, &e.kind,
			&e.name, &e.preview, &agent); err != nil {
			rows.Close()
			return nil, err
		}
		if agent != nil {
			e.agentID = *agent
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

// recipients are the people a message concerns (#440): the conversation's
// human members, except its author, except who muted the conversation, and
// except who has read past it — with a device to send to.
func (n *Notifier) recipients(ctx context.Context, ev event) ([]uuid.UUID, error) {
	rows, err := n.Pool.Query(ctx, `SELECT cm.member_id FROM conversation_members cm
		 WHERE cm.conversation_id = $1 AND cm.member_kind = 'human' AND cm.left_at IS NULL AND NOT cm.muted
		   AND NOT ($2 = 'human' AND cm.member_id = $3::uuid)
		   AND (cm.last_read_at IS NULL OR cm.last_read_at < $4)
		   AND EXISTS (SELECT 1 FROM push_devices d WHERE d.human_id = cm.member_id)`,
		ev.conversationID, ev.authorKind, ev.authorID, ev.at)
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

type device struct{ token, platform, env, lang, sound string }

func (n *Notifier) devices(ctx context.Context, human uuid.UUID) ([]device, error) {
	rows, err := n.Pool.Query(ctx, `SELECT token, platform, environment, lang, sound FROM push_devices WHERE human_id=$1`, human)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []device
	for rows.Next() {
		var d device
		if err := rows.Scan(&d.token, &d.platform, &d.env, &d.lang, &d.sound); err != nil {
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
