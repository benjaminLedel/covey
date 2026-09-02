// Package notify is what the platform tells a person who is not looking at
// the tab (#169).
//
// Everything covey notices it kept to itself: a guard rail catches an action,
// the task stands blocked and THE AGENT WAITS — and the only way anybody
// learns of it is by opening the inbox on their own initiative. The agent's
// waiting is not free: its session is held open, its work is half done.
//
// Three decisions carry this package.
//
// AN OUTBOX, NOT A CHANNEL. Every event becomes a row. A mail that could not
// go out because SMTP was down is still there afterwards; one that did go out
// does not go again after a restart. Neither is answerable in a goroutine.
//
// IMMEDIATE, BUT DAMPED. A row is not sent when it appears but when its window
// closes, together with everything that joined it. Ten tasks blocked by the
// same egress rule are one mail with ten lines. The window is per recipient
// AND class, so a cost alert does not queue behind task results.
//
// AN EVENT THAT RESOLVES ITSELF IS DROPPED. An approval decided two minutes
// after it was raised needs no mail. The check runs when the mail is about to
// go out, not when the event is written — which is exactly what the window
// buys.
package notify

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"covey/internal/settings"
)

// The classes. They group what is sent together and what a person can switch
// off — which is the same question, because a mail somebody cannot switch off
// is one they stop reading.
const (
	// ClassDecision: something is waiting for a human decision. An approval
	// (spec/06) holds an agent mid-action; an open point (spec/21) does not.
	ClassDecision = "decision"
	// ClassTask: a task ended — done, failed, cancelled.
	ClassTask = "task"
	// ClassCost: money. A budget cap that paused an agent, the fleet switch.
	ClassCost = "cost"
	// ClassOps: the platform itself. A runner that is gone, a provider that
	// does not answer.
	ClassOps = "ops"
)

// Kinds within the classes. They decide how a line reads and whether the thing
// is still open when the mail is about to go out.
const (
	KindApproval    = "approval"
	KindImprovement = "improvement"
	KindTaskDone    = "task_done"
	KindTaskFailed  = "task_failed"
	KindBudget      = "budget"
	KindFleetKilled = "fleet_killed"
	KindRunnerGone  = "runner_gone"
	// KindCredential: a target system refused a stored credential, or will
	// soon — the one kind of ops event an agent cannot get past on its own.
	KindCredential = "credential"
)

// Defaults per class, applied while nobody has decided otherwise.
//
// `task` is off, and that is deliberate: it is the class most likely to become
// noise, and the one whose absence costs least. What holds an agent (decision)
// and what costs money (cost) are on, because their absence is what this
// package exists against.
var Defaults = map[string]bool{
	ClassDecision: true,
	ClassTask:     false,
	ClassCost:     true,
	ClassOps:      true,
}

// DefaultWindow is how long events are collected before a mail goes out
// while nobody has set notify.window (#180). Long enough for ten blocked
// tasks to become one mail, short enough that a waiting agent is not waiting
// for us as well.
const DefaultWindow = settings.DefaultNotifyWindow

// Event is what a caller reports. The recipients are not part of it: who is
// told follows from the class and the organisation, and a caller that had to
// know would get it wrong in the one place somebody forgot to update.
type Event struct {
	OrgID uuid.UUID
	// AgentID names the agent this is about, where there is one. It decides
	// the recipient for the classes that have an owner.
	AgentID uuid.UUID
	Class   string
	Kind    string
	// SubjectID is the approval, the task, the runner — what the event is
	// about. It is what makes the obsolescence check possible.
	SubjectID *uuid.UUID
	// Title is the line as it will stand in the mail, rendered now, while the
	// agent's name and the task's title are at hand.
	Title string
	// Link is where it can be dealt with, as a path (/inbox, /agents/…).
	Link string
}

type Store struct {
	pool *pgxpool.Pool
	// settings carries the instance's say: the window (notify.window) and
	// the master switch per class (notify.<class>). Nil means the defaults.
	settings *settings.Store
	// window, when set, overrides the setting. A knob for tests.
	window time.Duration
}

func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// WithSettings hands the store the instance settings it reads the window and
// the class switches from.
func (s *Store) WithSettings(st *settings.Store) *Store {
	s.settings = st
	return s
}

// WithWindow pins the damping window. A knob for tests, not a control — the
// control is the notify.window setting.
//
// Zero keeps the setting — an unset duration must not mean "send at once".
// A negative one means exactly that, and is how a test gets its event out
// without waiting five minutes for it.
func (s *Store) WithWindow(d time.Duration) *Store {
	if d != 0 {
		s.window = d
	}
	return s
}

// Window answers the damping window in force: the pinned one, else the
// setting, else the default.
func (s *Store) Window(ctx context.Context) time.Duration {
	if s.window != 0 {
		return s.window
	}
	if s.settings != nil {
		return s.settings.NotifyWindowValue(ctx)
	}
	return DefaultWindow
}

// classOn asks the instance's master switch.
func (s *Store) classOn(ctx context.Context, class string) bool {
	if s.settings == nil {
		return true
	}
	return s.settings.NotifyClassOn(ctx, class)
}

// Disabled lists the classes the installation has switched off for
// everybody — what the account page greys out.
func (s *Store) Disabled(ctx context.Context) []string {
	out := []string{}
	for _, class := range settings.NotifyClasses {
		if !s.classOn(ctx, class) {
			out = append(out, class)
		}
	}
	return out
}

// Emit writes one event for everybody it concerns.
//
// It never returns an error to its caller's path — a broken notification must
// not fail the run that produced the event. What it can report is a database
// problem, and the callers log it and carry on.
func (s *Store) Emit(ctx context.Context, ev Event) error {
	if s == nil || s.pool == nil {
		return nil
	}
	// The instance's switch comes before anybody's: a class switched off
	// here is not written down for anyone.
	if !s.classOn(ctx, ev.Class) {
		return nil
	}
	recipients, err := s.recipients(ctx, ev)
	if err != nil {
		return err
	}
	if len(recipients) == 0 {
		return nil
	}
	// The due time is the DATABASE's now plus the window, not this
	// process's: the sender asks the database whether a row is due, and two
	// clocks — the host's and a container's — disagree by enough to hold a
	// zero-window row back for a pass or two.
	window := s.Window(ctx).Seconds()
	batch := &pgx.Batch{}
	for _, account := range recipients {
		var org *uuid.UUID
		if ev.OrgID != uuid.Nil {
			o := ev.OrgID
			org = &o
		}
		batch.Queue(`INSERT INTO notifications
			(id, account_id, org_id, class, kind, subject_id, title, link, due_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8, now() + make_interval(secs => $9))`,
			uuid.New(), account, org, ev.Class, ev.Kind, ev.SubjectID, ev.Title, ev.Link, window)
	}
	return s.pool.SendBatch(ctx, batch).Close()
}

// recipients answers who is told, and it is the one place that knows.
//
// The rules follow the roles that may ACT on the thing: whoever cannot decide
// an approval gets no mail about one. The agent's owner is always included
// where there is one — they hired it, they answer for it.
func (s *Store) recipients(ctx context.Context, ev Event) ([]uuid.UUID, error) {
	var query string
	var args []any

	switch ev.Class {
	case ClassDecision:
		if ev.Kind == KindImprovement {
			// An open point from covey Doctor (spec/21) is a proposal about
			// how the organisation runs its agents. It blocks nothing, and
			// the organisation's administrator is who answers for its
			// configuration — so it goes to them alone, not to everybody
			// who could decide an approval (#181).
			query = `SELECT DISTINCT h.account_id FROM humans h
				WHERE h.org_id=$1 AND h.role='org_admin'`
			args = []any{ev.OrgID}
			break
		}
		// Whoever may decide (the roles behind POST /approvals/{id}/decide),
		// plus the agent's owner.
		query = `SELECT DISTINCT h.account_id FROM humans h
			WHERE h.org_id=$1 AND (h.role = ANY($2)
				OR h.id = (SELECT owner_id FROM agents WHERE id=$3))`
		args = []any{ev.OrgID, []string{"org_admin", "platform_admin", "agent_owner", "security"}, ev.AgentID}
	case ClassTask:
		// Only the owner. A finished task is news for whoever answers for the
		// agent, and noise for everybody else.
		query = `SELECT h.account_id FROM humans h
			WHERE h.org_id=$1 AND h.id = (SELECT owner_id FROM agents WHERE id=$2)`
		args = []any{ev.OrgID, ev.AgentID}
	case ClassCost:
		query = `SELECT DISTINCT h.account_id FROM humans h
			WHERE h.org_id=$1 AND h.role = ANY($2)`
		args = []any{ev.OrgID, []string{"org_admin", "platform_admin", "controlling"}}
	case ClassOps:
		if ev.OrgID == uuid.Nil {
			// The instance's own business: the people who administer it.
			query = `SELECT id FROM accounts WHERE platform_role='system_admin'`
			break
		}
		roles := []string{"org_admin", "platform_admin"}
		if ev.Kind == KindCredential {
			// Secrets are the security role's to replace (the routes behind
			// /secrets) — a warning about one that only the admins read
			// waits for them to forward it.
			roles = append(roles, "security")
		}
		query = `SELECT DISTINCT h.account_id FROM humans h
			WHERE h.org_id=$1 AND h.role = ANY($2)`
		args = []any{ev.OrgID, roles}
	default:
		return nil, errors.New("unknown notification class: " + ev.Class)
	}

	rows, err := s.pool.Query(ctx, query, args...)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return s.filterByPreference(ctx, out, ev.Class)
}

// filterByPreference drops whoever has switched this class off. It runs at
// EMIT time as well as before sending — here so that rows nobody wants are
// never written at all.
func (s *Store) filterByPreference(ctx context.Context, accounts []uuid.UUID, class string) ([]uuid.UUID, error) {
	if len(accounts) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT account_id, enabled FROM notification_prefs
		 WHERE class=$1 AND account_id = ANY($2)`, class, accounts)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	decided := map[uuid.UUID]bool{}
	for rows.Next() {
		var id uuid.UUID
		var enabled bool
		if err := rows.Scan(&id, &enabled); err != nil {
			return nil, err
		}
		decided[id] = enabled
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := accounts[:0]
	for _, a := range accounts {
		if enabled, ok := decided[a]; ok {
			if enabled {
				out = append(out, a)
			}
			continue
		}
		if Defaults[class] {
			out = append(out, a)
		}
	}
	return out, nil
}

// Preferences returns the effective settings of one account — the stored ones
// over the defaults.
func (s *Store) Preferences(ctx context.Context, accountID uuid.UUID) (map[string]bool, error) {
	out := map[string]bool{}
	for class, def := range Defaults {
		out[class] = def
	}
	if s == nil || s.pool == nil {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`SELECT class, enabled FROM notification_prefs WHERE account_id=$1`, accountID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var class string
		var enabled bool
		if err := rows.Scan(&class, &enabled); err != nil {
			return out, err
		}
		if _, known := Defaults[class]; known {
			out[class] = enabled
		}
	}
	return out, rows.Err()
}

// SetPreference records a decision. There is no "back to default": once
// somebody has answered the question, their answer stands — a switch that
// silently reverts to a default we change later is a promise broken quietly.
func (s *Store) SetPreference(ctx context.Context, accountID uuid.UUID, class string, enabled bool) error {
	if _, known := Defaults[class]; !known {
		return errors.New("unknown notification class: " + class)
	}
	_, err := s.pool.Exec(ctx,
		`INSERT INTO notification_prefs (account_id, class, enabled) VALUES ($1,$2,$3)
		 ON CONFLICT (account_id, class) DO UPDATE SET enabled=EXCLUDED.enabled`,
		accountID, class, enabled)
	return err
}
