package notify

// The sender: one pass over what is due, one mail per recipient and class.
//
// It runs as a loop beside the orchestrator's, and it is deliberately dumb —
// no queue, no worker pool, no retries in memory. Everything it needs to know
// is in the table, so a restart in the middle costs at most one duplicate mail
// and never a lost one.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/mail"
	"covey/internal/settings"
)

// Interval is how often the sender looks. Finer than the damping window would
// only produce empty passes; coarser would add its own delay on top of it.
const Interval = time.Minute

// MaxAttempts before a row is given up on. A mail server that has refused five
// times over five minutes is not going to accept the sixth try either, and a
// row that is retried forever hides the fact that nothing gets through.
const MaxAttempts = 5

// Sender turns due rows into mail.
type Sender struct {
	Pool     *pgxpool.Pool
	Mail     mail.Sender
	Settings *settings.Store
	// SiteURL is the address the links are built from. Empty = the mail lists
	// what is waiting without links: a path alone helps nobody, and guessing a
	// host would send people somewhere.
	SiteURL string
	Log     *slog.Logger
}

// Run keeps sending until the context ends.
func (s *Sender) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.Once(ctx); err != nil {
				s.Log.Warn("notification mails could not be sent", "err", err)
			} else if n > 0 {
				s.Log.Info("notification mails sent", "mails", n)
			}
		}
	}
}

// group is one mail to be: everything of one class that one person is owed.
type group struct {
	account uuid.UUID
	class   string
}

// Once makes one pass and reports how many mails went out.
func (s *Sender) Once(ctx context.Context) (int, error) {
	if s.Pool == nil || s.Mail == nil {
		return 0, nil
	}
	// No mailer, no attempt — and no rows burned either: they keep their
	// state and go out when somebody has configured a mail server.
	if !s.Mail.Configured(ctx) {
		return 0, nil
	}

	rows, err := s.Pool.Query(ctx,
		`SELECT account_id, class FROM notifications
		 WHERE state='pending' AND due_at <= now()
		 GROUP BY account_id, class`)
	if err != nil {
		return 0, err
	}
	var groups []group
	for rows.Next() {
		var g group
		if err := rows.Scan(&g.account, &g.class); err != nil {
			rows.Close()
			return 0, err
		}
		groups = append(groups, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	sent := 0
	for _, g := range groups {
		ok, err := s.sendGroup(ctx, g)
		if err != nil {
			s.Log.Warn("notification mail failed", "class", g.class, "err", err)
			continue
		}
		if ok {
			sent++
		}
	}
	return sent, nil
}

// item is one line of the mail.
type item struct {
	id        uuid.UUID
	kind      string
	subjectID *uuid.UUID
	title     string
	link      string
	createdAt time.Time
}

func (s *Sender) sendGroup(ctx context.Context, g group) (bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)

	// SKIP LOCKED so a second process (or a second pass that overlaps) takes
	// the next group instead of waiting on this one — the same pattern the
	// task queue uses.
	rows, err := tx.Query(ctx,
		`SELECT id, kind, subject_id, title, link, created_at FROM notifications
		 WHERE account_id=$1 AND class=$2 AND state='pending' AND due_at <= now()
		 ORDER BY created_at
		 FOR UPDATE SKIP LOCKED`, g.account, g.class)
	if err != nil {
		return false, err
	}
	var items []item
	for rows.Next() {
		var it item
		if err := rows.Scan(&it.id, &it.kind, &it.subjectID, &it.title, &it.link, &it.createdAt); err != nil {
			rows.Close()
			return false, err
		}
		items = append(items, it)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(items) == 0 {
		return false, nil
	}

	// What has been dealt with in the meantime does not need a mail. This is
	// what the damping window is really for: on a busy instance most of the
	// decisions asked for are made before anybody could have read about them.
	var live []item
	var obsolete []uuid.UUID
	for _, it := range items {
		still, err := s.stillOpen(ctx, tx, it)
		if err != nil {
			return false, err
		}
		if still {
			live = append(live, it)
		} else {
			obsolete = append(obsolete, it.id)
		}
	}
	if len(obsolete) > 0 {
		if _, err := tx.Exec(ctx,
			`UPDATE notifications SET state='obsolete', sent_at=now() WHERE id = ANY($1)`,
			obsolete); err != nil {
			return false, err
		}
	}
	if len(live) == 0 {
		return false, tx.Commit(ctx)
	}

	var email, name, lang string
	if err := tx.QueryRow(ctx,
		`SELECT email, display_name, lang FROM accounts WHERE id=$1`, g.account).
		Scan(&email, &name, &lang); err != nil {
		return false, err
	}
	site, _ := s.Settings.Get(ctx, settings.SiteName)
	msg := s.compose(mail.Lang(lang), site, name, g.class, live)
	msg.To = email

	// Sent first, marked afterwards. The other order loses a mail whenever the
	// process dies in between; this one repeats it. A notification that
	// arrives twice is a nuisance, one that never arrives is the bug this
	// package exists against.
	sendErr := s.Mail.Send(ctx, msg)
	ids := make([]uuid.UUID, 0, len(live))
	for _, it := range live {
		ids = append(ids, it.id)
	}
	if sendErr != nil {
		if _, err := tx.Exec(ctx,
			`UPDATE notifications
			 SET attempts = attempts + 1,
			     last_error = $2,
			     due_at = now() + interval '2 minutes',
			     state = CASE WHEN attempts + 1 >= $3 THEN 'failed' ELSE 'pending' END
			 WHERE id = ANY($1)`, ids, sendErr.Error(), MaxAttempts); err != nil {
			return false, err
		}
		if err := tx.Commit(ctx); err != nil {
			return false, err
		}
		return false, sendErr
	}
	if _, err := tx.Exec(ctx,
		`UPDATE notifications SET state='sent', sent_at=now() WHERE id = ANY($1)`, ids); err != nil {
		return false, err
	}
	return true, tx.Commit(ctx)
}

// stillOpen asks whether the thing the row is about still needs somebody.
//
// Only the two kinds that CAN resolve themselves are asked; an event that
// happened (a task ended, a budget was hit) stays true no matter how long the
// window is.
func (s *Sender) stillOpen(ctx context.Context, tx pgx.Tx, it item) (bool, error) {
	if it.subjectID == nil {
		return true, nil
	}
	var table string
	switch it.kind {
	case KindApproval:
		table = "approvals"
	case KindImprovement:
		table = "improvement_items"
	case KindRunnerGone:
		// A host that came back needs no mail about having been away. The
		// timestamp is written when a runner connects (runner store, Seen), so
		// "seen since we noticed it was gone" is exactly the question.
		var seen *time.Time
		err := tx.QueryRow(ctx,
			`SELECT last_seen_at FROM runners WHERE id=$1`, *it.subjectID).Scan(&seen)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil // deleted in the meantime
		}
		if err != nil {
			return false, err
		}
		return seen == nil || !seen.After(it.createdAt), nil
	default:
		return true, nil
	}
	var status string
	err := tx.QueryRow(ctx,
		fmt.Sprintf(`SELECT status FROM %s WHERE id=$1`, table), *it.subjectID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil // deleted in the meantime — nothing to report
	}
	if err != nil {
		return false, err
	}
	return status == "pending", nil
}

// compose builds the message. Subject and the framing sentences come from the
// interface's language catalogues; the lines themselves were rendered when the
// event was written.
func (s *Sender) compose(lang, site, name, class string, items []item) mail.Message {
	vars := map[string]string{"site": site, "name": name}
	var b strings.Builder
	b.WriteString(mail.Text(lang, "mails.notify."+class+".intro", vars))
	b.WriteString("\n\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it.title)
		b.WriteString("\n")
		if s.SiteURL != "" && it.link != "" {
			b.WriteString("  ")
			b.WriteString(strings.TrimRight(s.SiteURL, "/") + it.link)
			b.WriteString("\n")
		}
	}
	b.WriteString("\n")
	footer := map[string]string{"site": site, "link": ""}
	if s.SiteURL != "" {
		footer["link"] = strings.TrimRight(s.SiteURL, "/") + "/profile"
	}
	b.WriteString(mail.Text(lang, "mails.notify.footer", footer))
	return mail.Message{
		// To is filled by the caller, which is the only place that has the
		// address — compose builds what everybody gets to see.
		Subject: mail.Text(lang, "mails.notify."+class+".subject", vars),
		Body:    b.String(),
	}
}
