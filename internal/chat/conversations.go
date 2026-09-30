package chat

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Conversations (#440): people and agents talking, the way one writes to a
// colleague. A conversation has members, and only members read it. It is
// direct — exactly two members, one conversation per pair — or a group with
// a title.

// The two kinds of member.
const (
	MemberHuman = "human"
	MemberAgent = "agent"
)

// The two kinds of conversation.
const (
	KindDirect = "direct"
	KindGroup  = "group"
)

// The kinds of message. Text is what somebody said; the other three are what
// a task reports back, written by the platform in the agent's name.
const (
	MessageText     = "text"
	MessageResult   = "result"
	MessageError    = "error"
	MessageQuestion = "question"
	// MessageConfigProposal is a drafted change to the agent's configuration
	// (#491): Meta["proposal_id"] names the stored proposal, Text is what a
	// surface without the card shows.
	MessageConfigProposal = "config_proposal"
)

// ErrNotFound: no such conversation, or none of the reader's.
var ErrNotFound = errors.New("conversation not found")

// Ref names a member: a human seat or an agent.
type Ref struct {
	Kind string    `json:"kind"`
	ID   uuid.UUID `json:"id"`
}

func Human(id uuid.UUID) Ref { return Ref{Kind: MemberHuman, ID: id} }
func Agent(id uuid.UUID) Ref { return Ref{Kind: MemberAgent, ID: id} }

func (r Ref) String() string { return r.Kind + ":" + r.ID.String() }

// DirectKey is what makes a direct conversation one per pair: both members,
// sorted, so that A→B and B→A are the same key.
func DirectKey(a, b Ref) string {
	k := []string{a.String(), b.String()}
	sort.Strings(k)
	return k[0] + "|" + k[1]
}

// Member is one member as a surface shows it.
type Member struct {
	Kind       string     `json:"kind"`
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Slug       string     `json:"slug,omitempty"`
	Email      string     `json:"email,omitempty"`
	Role       string     `json:"role"`
	JoinedAt   time.Time  `json:"joined_at"`
	LeftAt     *time.Time `json:"left_at,omitempty"`
	LastReadAt *time.Time `json:"last_read_at,omitempty"`
	Muted      bool       `json:"muted"`
}

func (m Member) Ref() Ref { return Ref{Kind: m.Kind, ID: m.ID} }

// Conversation is the object with its members.
type Conversation struct {
	ID            uuid.UUID `json:"id"`
	OrgID         uuid.UUID `json:"org_id"`
	Kind          string    `json:"kind"`
	Title         string    `json:"title,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	LastMessageAt time.Time `json:"last_message_at"`
	Members       []Member  `json:"members"`
}

// Active are the members who have not left.
func (c Conversation) Active() []Member {
	var out []Member
	for _, m := range c.Members {
		if m.LeftAt == nil {
			out = append(out, m)
		}
	}
	return out
}

// Has says whether ref is an active member.
func (c Conversation) Has(ref Ref) bool {
	for _, m := range c.Active() {
		if m.Kind == ref.Kind && m.ID == ref.ID {
			return true
		}
	}
	return false
}

// Message is one line of a conversation.
type Message struct {
	ID             uuid.UUID  `json:"id"`
	ConversationID uuid.UUID  `json:"conversation_id"`
	OrgID          uuid.UUID  `json:"org_id"`
	AuthorKind     string     `json:"author_kind"`
	AuthorID       *uuid.UUID `json:"author_id,omitempty"`
	AuthorName     string     `json:"author_name,omitempty"`
	Text           string     `json:"text"`
	Kind           string     `json:"kind"`
	TaskID         *uuid.UUID `json:"task_id,omitempty"`
	ReplyTo        *uuid.UUID `json:"reply_to,omitempty"`
	TriageState    string     `json:"triage_state,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	// Meta is what the platform notes about a message it wrote — which
	// blocked edge a question stands on.
	Meta map[string]string `json:"meta,omitempty"`
	/* Read with the message, never written: the task it points at, as far
	   as a line needs it — its title and state, and for a result or an
	   error the report, which the message retells and keeps one tap away. */
	TaskTitle string `json:"task_title,omitempty"`
	TaskState string `json:"task_state,omitempty"`
	Report    string `json:"report,omitempty"`
	/* Read with a config_proposal message, never written (#491): the
	   proposal as the reader may see and decide it. Filled by the API. */
	Proposal *ProposalCard `json:"proposal,omitempty"`
}

// ProposalCard is a configuration proposal as a conversation shows it
// (#491): what changes, why, whether it widens access, who may decide, and —
// once decided — who and when. The reader's own right to decide stands in
// CanDecide; the server says it, so that no client keeps a table of roles.
type ProposalCard struct {
	ID        uuid.UUID      `json:"id"`
	Status    string         `json:"status"`
	Title     string         `json:"title"`
	Rationale string         `json:"rationale"`
	Diff      []ProposalFile `json:"diff"`
	// Widens: ACCESS.md or EGRESS.md change — only org_admin or security
	// may accept then.
	Widens    []string `json:"widens,omitempty"`
	Conflicts []string `json:"conflicts,omitempty"`
	CanDecide bool     `json:"can_decide"`
	// CanAccept: CanDecide, and nothing blocks the acceptance (a conflict,
	// or access that only security may widen).
	CanAccept      bool       `json:"can_accept"`
	Approvers      []string   `json:"approvers"`
	RequestedBy    string     `json:"requested_by,omitempty"`
	DecidedBy      string     `json:"decided_by,omitempty"`
	DecidedAt      *time.Time `json:"decided_at,omitempty"`
	AppliedVersion int        `json:"applied_version,omitempty"`
}

// ProposalFile is one changed file: the running state and the proposed one.
type ProposalFile struct {
	File   string `json:"file"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// By says whether ref wrote the message.
func (m Message) By(ref Ref) bool {
	return m.AuthorKind == ref.Kind && m.AuthorID != nil && *m.AuthorID == ref.ID
}

// Direct returns the direct conversation between a and b, opening it the
// first time. ON CONFLICT on direct_key is what keeps two requests that open
// it at the same moment from making two.
func (s *Store) Direct(ctx context.Context, orgID uuid.UUID, a, b Ref, createdBy *uuid.UUID) (uuid.UUID, error) {
	key := DirectKey(a, b)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO conversations (id, org_id, kind, direct_key, created_by_human)
		VALUES ($1, $2, 'direct', $3, $4) ON CONFLICT (direct_key) DO NOTHING`, id, orgID, key, createdBy); err != nil {
		return uuid.Nil, err
	}
	if err := tx.QueryRow(ctx, `SELECT id FROM conversations WHERE direct_key = $1 AND org_id = $2`, key, orgID).Scan(&id); err != nil {
		return uuid.Nil, err
	}
	for _, m := range []Ref{a, b} {
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_members (conversation_id, member_kind, member_id)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, m.Kind, m.ID); err != nil {
			return uuid.Nil, err
		}
	}
	return id, tx.Commit(ctx)
}

// FindDirect looks the direct conversation up without opening it: reading
// an empty conversation must not leave a row behind.
func (s *Store) FindDirect(ctx context.Context, orgID uuid.UUID, a, b Ref) (uuid.UUID, bool, error) {
	var id uuid.UUID
	err := s.pool.QueryRow(ctx, `SELECT id FROM conversations WHERE direct_key = $1 AND org_id = $2`,
		DirectKey(a, b), orgID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	}
	return id, err == nil, err
}

// CreateGroup opens a group; the person who opens it is its owner.
func (s *Store) CreateGroup(ctx context.Context, orgID uuid.UUID, title string, owner uuid.UUID, members []Ref) (uuid.UUID, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, err
	}
	defer tx.Rollback(ctx)
	id := uuid.New()
	if _, err := tx.Exec(ctx, `INSERT INTO conversations (id, org_id, kind, title, created_by_human)
		VALUES ($1, $2, 'group', $3, $4)`, id, orgID, title, owner); err != nil {
		return uuid.Nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO conversation_members (conversation_id, member_kind, member_id, role)
		VALUES ($1, 'human', $2, 'owner')`, id, owner); err != nil {
		return uuid.Nil, err
	}
	for _, m := range members {
		if m.Kind == MemberHuman && m.ID == owner {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO conversation_members (conversation_id, member_kind, member_id)
			VALUES ($1, $2, $3) ON CONFLICT DO NOTHING`, id, m.Kind, m.ID); err != nil {
			return uuid.Nil, err
		}
	}
	return id, tx.Commit(ctx)
}

// memberNames resolves a member to how it is called. One LEFT JOIN each, so
// that a member whose seat or agent is gone still reads as a row.
const memberNames = `
	LEFT JOIN humans h ON cm.member_kind = 'human' AND h.id = cm.member_id
	LEFT JOIN agents a ON cm.member_kind = 'agent' AND a.id = cm.member_id`

// Get reads a conversation with its members, left ones included.
func (s *Store) Get(ctx context.Context, id uuid.UUID) (Conversation, error) {
	var c Conversation
	err := s.pool.QueryRow(ctx, `SELECT id, org_id, kind, coalesce(title, ''), created_at, last_message_at
		FROM conversations WHERE id = $1 AND archived_at IS NULL`, id).
		Scan(&c.ID, &c.OrgID, &c.Kind, &c.Title, &c.CreatedAt, &c.LastMessageAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return c, ErrNotFound
	}
	if err != nil {
		return c, err
	}
	rows, err := s.pool.Query(ctx, `SELECT cm.member_kind, cm.member_id,
		       coalesce(h.display_name, a.display_name, ''), coalesce(a.slug, ''), coalesce(h.email, ''),
		       cm.role, cm.joined_at, cm.left_at, cm.last_read_at, cm.muted
		  FROM conversation_members cm`+memberNames+`
		 WHERE cm.conversation_id = $1
		 ORDER BY cm.joined_at, cm.member_kind, cm.member_id`, id)
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var m Member
		if err := rows.Scan(&m.Kind, &m.ID, &m.Name, &m.Slug, &m.Email, &m.Role, &m.JoinedAt, &m.LeftAt, &m.LastReadAt, &m.Muted); err != nil {
			return c, err
		}
		c.Members = append(c.Members, m)
	}
	return c, rows.Err()
}

// AddMember takes somebody into a group — back in, if they had left.
func (s *Store) AddMember(ctx context.Context, id uuid.UUID, ref Ref) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO conversation_members (conversation_id, member_kind, member_id)
		VALUES ($1, $2, $3)
		ON CONFLICT (conversation_id, member_kind, member_id)
		DO UPDATE SET left_at = NULL, changed_at = now(), joined_at = CASE WHEN conversation_members.left_at IS NULL
		                                          THEN conversation_members.joined_at ELSE now() END`,
		id, ref.Kind, ref.ID)
	return err
}

// RemoveMember lets somebody leave a group. The row stays: the audit still
// says who was in it, and when they left.
func (s *Store) RemoveMember(ctx context.Context, id uuid.UUID, ref Ref) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversation_members SET left_at = now(), changed_at = now()
		WHERE conversation_id = $1 AND member_kind = $2 AND member_id = $3 AND left_at IS NULL`, id, ref.Kind, ref.ID)
	return err
}

// Post writes a message and moves the conversation's last_message_at, in
// one transaction. A message with an id that exists already is not written
// again: the reports of a task carry an id derived from the task, so that
// two control planes, or one after a restart, report it once.
func (s *Store) Post(ctx context.Context, m Message) (Message, bool, error) {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	if m.Kind == "" {
		m.Kind = MessageText
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return m, false, err
	}
	defer tx.Rollback(ctx)
	err = tx.QueryRow(ctx, `INSERT INTO conversation_messages
		(id, conversation_id, org_id, author_kind, author_id, text, kind, task_id, reply_to, triage_state, meta)
		SELECT $1, c.id, c.org_id, $3, $4, $5, $6, $7, $8, $9, $10 FROM conversations c WHERE c.id = $2
		ON CONFLICT (id) DO NOTHING
		RETURNING org_id, created_at`,
		m.ID, m.ConversationID, m.AuthorKind, m.AuthorID, m.Text, m.Kind, m.TaskID, m.ReplyTo, m.TriageState, m.Meta).
		Scan(&m.OrgID, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return m, false, nil
	}
	if err != nil {
		return m, false, err
	}
	if _, err := tx.Exec(ctx, `UPDATE conversations SET last_message_at = greatest(last_message_at, $2) WHERE id = $1`,
		m.ConversationID, m.CreatedAt); err != nil {
		return m, false, err
	}
	return m, true, tx.Commit(ctx)
}

// messageCols reads a message with its author's name and its task.
const messageCols = `m.id, m.conversation_id, m.org_id, m.author_kind, m.author_id,
	coalesce(h.display_name, a.display_name, ''), m.text, m.kind, m.task_id, m.reply_to, m.triage_state, m.created_at, m.meta,
	coalesce(t.title, ''), coalesce(t.state, ''),
	CASE WHEN m.kind = 'result' THEN coalesce(t.result, '') WHEN m.kind = 'error' THEN coalesce(t.error, '') ELSE '' END`

const messageJoins = `
	LEFT JOIN humans h ON m.author_kind = 'human' AND h.id = m.author_id
	LEFT JOIN agents a ON m.author_kind = 'agent' AND a.id = m.author_id
	LEFT JOIN backlog_tasks t ON t.id = m.task_id`

func scanMessages(rows pgx.Rows) ([]Message, error) {
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		if err := rows.Scan(&m.ID, &m.ConversationID, &m.OrgID, &m.AuthorKind, &m.AuthorID, &m.AuthorName,
			&m.Text, &m.Kind, &m.TaskID, &m.ReplyTo, &m.TriageState, &m.CreatedAt, &m.Meta,
			&m.TaskTitle, &m.TaskState, &m.Report); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// PageQuery and ListQuery are the two hot queries (#440): a page of
// messages, and a person's conversations. Exported so that a test can hold
// them to the indexes (internal/integration, EXPLAIN).
var (
	PageQuery = `SELECT * FROM (
		SELECT ` + messageCols + ` FROM conversation_messages m` + messageJoins + `
		 WHERE m.conversation_id = $1
		   AND ($2::timestamptz IS NULL OR (m.created_at, m.id) < ($2, $3::uuid))
		 ORDER BY m.created_at DESC, m.id DESC LIMIT $4
	) p ORDER BY created_at, id`
	ListQuery = `SELECT c.id, cm.muted, cm.last_read_at, ` + unreadCount + `
		  FROM conversation_members cm JOIN conversations c ON c.id = cm.conversation_id
		 WHERE cm.member_kind = 'human' AND cm.member_id = $1 AND cm.left_at IS NULL AND c.archived_at IS NULL
		   AND ($3::timestamptz IS NULL OR c.last_message_at > $3 OR cm.changed_at > $3)
		 ORDER BY c.last_message_at DESC LIMIT $2`
)

// Cursor is where a page ends: the oldest message of the page before.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// Page reads the messages before the cursor (the newest without one), at
// most limit, oldest first — the order a conversation is read in. The
// cursor compares (created_at, id) as a pair, which is the index's order.
func (s *Store) Page(ctx context.Context, id uuid.UUID, before *Cursor, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	var at *time.Time
	var mid *uuid.UUID
	if before != nil {
		at, mid = &before.At, &before.ID
	}
	rows, err := s.pool.Query(ctx, PageQuery, id, at, mid, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// After reads the messages after the cursor, oldest first — what a reader
// that has the conversation up to that message does not have yet (#447).
func (s *Store) After(ctx context.Context, id uuid.UUID, after Cursor, limit int) ([]Message, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+messageCols+` FROM conversation_messages m`+messageJoins+`
		 WHERE m.conversation_id = $1 AND (m.created_at, m.id) > ($2, $3::uuid)
		 ORDER BY m.created_at, m.id LIMIT $4`, id, after.At, after.ID, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Left are the conversations the person left after since — so that a list
// refreshed by ListSince can drop them.
func (s *Store) Left(ctx context.Context, humanID uuid.UUID, since time.Time) ([]uuid.UUID, error) {
	rows, err := s.pool.Query(ctx, `SELECT conversation_id FROM conversation_members
		 WHERE member_kind = 'human' AND member_id = $1 AND left_at > $2`, humanID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []uuid.UUID{}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// Now is the database's clock — the cursor a delta read hands out, so that
// it compares with the timestamps the rows carry, not with this process's.
func (s *Store) Now(ctx context.Context) (time.Time, error) {
	var t time.Time
	err := s.pool.QueryRow(ctx, `SELECT now()`).Scan(&t)
	return t, err
}

// Search finds the messages that contain any of the words (#416), newest
// first. Case does not matter.
func (s *Store) Search(ctx context.Context, id uuid.UUID, woerter []string, limit int) ([]Message, error) {
	muster := Muster(woerter)
	if len(muster) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `SELECT `+messageCols+` FROM conversation_messages m`+messageJoins+`
		 WHERE m.conversation_id = $1 AND (m.text ILIKE ANY($3::text[])
		       OR (m.kind IN ('result', 'error') AND
		           CASE WHEN m.kind = 'result' THEN coalesce(t.result, '') ELSE coalesce(t.error, '') END ILIKE ANY($3::text[])))
		 ORDER BY m.created_at DESC LIMIT $2`, id, limit, muster)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// Muster makes ILIKE patterns of words, with the wildcards in them escaped:
// "50 %" is looked for, not "everything".
func Muster(woerter []string) []string {
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	var out []string
	for _, w := range woerter {
		if w = strings.TrimSpace(w); w != "" {
			out = append(out, "%"+r.Replace(w)+"%")
		}
	}
	return out
}

// Pending says whether a message of the conversation still waits for a
// triage's decision — the "thinking" between two lines.
func (s *Store) Pending(ctx context.Context, id uuid.UUID) (bool, error) {
	var p bool
	err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM conversation_messages
		WHERE conversation_id = $1 AND triage_state = 'pending')`, id).Scan(&p)
	return p, err
}

// SetTriage writes the triage's state onto the message.
func (s *Store) SetTriage(ctx context.Context, id uuid.UUID, zustand string) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversation_messages SET triage_state = $2 WHERE id = $1`, id, zustand)
	return err
}

// LinkTask records that this message became work, or went onto it.
func (s *Store) LinkTask(ctx context.Context, id, taskID uuid.UUID) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversation_messages SET task_id = $2 WHERE id = $1`, id, taskID)
	return err
}

/*
Liegengeblieben holt die Nachrichten, die beim letzten Mal keine

	Entscheidung mehr bekommen haben.

	Sie werden in derselben Bewegung auf 'done' gesetzt, unter FOR UPDATE SKIP
	LOCKED — dasselbe Muster wie im Backlog. Zwei Control Planes an derselben
	Datenbank holen sich damit nicht dieselbe Nachricht, und eine, die dabei
	stirbt, lässt keine Zeile ewig im Zugriff stehen. Dass der Zustand VOR der
	Arbeit gesetzt wird, ist Absicht: Eine Nachricht, an der die Triage zweimal
	scheitert, soll nicht bei jedem Neustart wieder einen Zug kosten.
*/
func (s *Store) Liegengeblieben(ctx context.Context, limit int) ([]Message, error) {
	rows, err := s.pool.Query(ctx, `WITH genommen AS (
		UPDATE conversation_messages SET triage_state = $2
		 WHERE id IN (
		     SELECT id FROM conversation_messages
		      WHERE triage_state = $1
		      ORDER BY created_at
		      FOR UPDATE SKIP LOCKED
		      LIMIT $3)
		 RETURNING *
	)
	SELECT `+messageCols+` FROM genommen m`+messageJoins,
		StatePending, StateDone, limit)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MarkRead moves the person's point in the conversation to at — forward
// only, and never past now. The reader sends the time of the newest message
// it has shown, so one that arrives in between stays unread.
func (s *Store) MarkRead(ctx context.Context, id, humanID uuid.UUID, at time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversation_members
		SET last_read_at = greatest(coalesce(last_read_at, '-infinity'), least($3::timestamptz, now())), changed_at = now()
		WHERE conversation_id = $1 AND member_kind = 'human' AND member_id = $2`, id, humanID, at)
	return err
}

// SetMuted: a muted conversation is not pushed. It stays in the list and
// counts as unread there; muting is about the phone, not about reading.
func (s *Store) SetMuted(ctx context.Context, id, humanID uuid.UUID, muted bool) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversation_members SET muted = $3, changed_at = now()
		WHERE conversation_id = $1 AND member_kind = 'human' AND member_id = $2`, id, humanID, muted)
	return err
}

// UnreadCap: counting stops here. "99+" is the answer past it, and counting
// a thousand rows to say so would be the list's most expensive part.
const UnreadCap = 100

// Summary is one conversation in a person's list: the members, how much the
// person has not read, and the newest message.
type Summary struct {
	Conversation
	Unread   int        `json:"unread"`
	Muted    bool       `json:"muted"`
	LastRead *time.Time `json:"last_read_at,omitempty"`
	Last     *Message   `json:"last,omitempty"`
}

/* unreadCount counts the messages after the member's point, not the member's
 * own, and stops at UnreadCap. It reads the page index backwards from the
 * newest message, so it costs at most UnreadCap index entries. */
const unreadCount = `(SELECT count(*) FROM (
		SELECT 1 FROM conversation_messages um
		 WHERE um.conversation_id = cm.conversation_id
		   AND um.created_at > coalesce(cm.last_read_at, cm.joined_at)
		   AND NOT (um.author_kind = cm.member_kind AND um.author_id = cm.member_id)
		 ORDER BY um.created_at DESC LIMIT 100) u)` // LIMIT = UnreadCap

// List reads the person's conversations, newest first.
func (s *Store) List(ctx context.Context, humanID uuid.UUID, limit int) ([]Summary, error) {
	return s.ListSince(ctx, humanID, nil, limit)
}

/* ListSince reads only the conversations that changed after since (#447): a
 * new message, the person's own read position, mute or membership. Unread
 * rises only with a new message and falls only with a read, so both are
 * covered. nil since is the whole list. */
func (s *Store) ListSince(ctx context.Context, humanID uuid.UUID, since *time.Time, limit int) ([]Summary, error) {
	if limit <= 0 || limit > 500 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, ListQuery, humanID, limit, since)
	if err != nil {
		return nil, err
	}
	type kopf struct {
		id       uuid.UUID
		muted    bool
		lastRead *time.Time
		unread   int
	}
	var koepfe []kopf
	for rows.Next() {
		var k kopf
		if err := rows.Scan(&k.id, &k.muted, &k.lastRead, &k.unread); err != nil {
			rows.Close()
			return nil, err
		}
		koepfe = append(koepfe, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(koepfe) == 0 {
		return []Summary{}, nil
	}
	ids := make([]uuid.UUID, len(koepfe))
	for i, k := range koepfe {
		ids[i] = k.id
	}
	/* Members and the newest message for the whole page at once — two
	   queries for the list, not two per conversation. */
	convs, err := s.many(ctx, ids)
	if err != nil {
		return nil, err
	}
	lrows, err := s.pool.Query(ctx, `SELECT l.* FROM unnest($1::uuid[]) AS k(id)
		CROSS JOIN LATERAL (
			SELECT `+messageCols+` FROM conversation_messages m`+messageJoins+`
			 WHERE m.conversation_id = k.id ORDER BY m.created_at DESC, m.id DESC LIMIT 1) l`, ids)
	if err != nil {
		return nil, err
	}
	letzte, err := scanMessages(lrows)
	if err != nil {
		return nil, err
	}
	zuletzt := map[uuid.UUID]Message{}
	for _, m := range letzte {
		zuletzt[m.ConversationID] = m
	}
	out := make([]Summary, 0, len(koepfe))
	for _, k := range koepfe {
		c, ok := convs[k.id]
		if !ok {
			continue
		}
		sm := Summary{Conversation: c, Unread: k.unread, Muted: k.muted, LastRead: k.lastRead}
		if m, ok := zuletzt[k.id]; ok {
			sm.Last = &m
		}
		out = append(out, sm)
	}
	return out, nil
}

// many reads several conversations with their members.
func (s *Store) many(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]Conversation, error) {
	out := map[uuid.UUID]Conversation{}
	rows, err := s.pool.Query(ctx, `SELECT id, org_id, kind, coalesce(title, ''), created_at, last_message_at
		FROM conversations WHERE id = ANY($1)`, ids)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.OrgID, &c.Kind, &c.Title, &c.CreatedAt, &c.LastMessageAt); err != nil {
			rows.Close()
			return nil, err
		}
		out[c.ID] = c
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	mrows, err := s.pool.Query(ctx, `SELECT cm.conversation_id, cm.member_kind, cm.member_id,
		       coalesce(h.display_name, a.display_name, ''), coalesce(a.slug, ''), coalesce(h.email, ''),
		       cm.role, cm.joined_at, cm.left_at, cm.last_read_at, cm.muted
		  FROM conversation_members cm`+memberNames+`
		 WHERE cm.conversation_id = ANY($1)
		 ORDER BY cm.joined_at, cm.member_kind, cm.member_id`, ids)
	if err != nil {
		return nil, err
	}
	defer mrows.Close()
	for mrows.Next() {
		var cid uuid.UUID
		var m Member
		if err := mrows.Scan(&cid, &m.Kind, &m.ID, &m.Name, &m.Slug, &m.Email, &m.Role, &m.JoinedAt, &m.LeftAt, &m.LastReadAt, &m.Muted); err != nil {
			return nil, err
		}
		c := out[cid]
		c.Members = append(c.Members, m)
		out[cid] = c
	}
	return out, mrows.Err()
}

// Unread is the sum over the person's conversations, for a badge.
func (s *Store) Unread(ctx context.Context, humanID uuid.UUID) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `SELECT coalesce(sum(`+unreadCount+`), 0)
		  FROM conversation_members cm JOIN conversations c ON c.id = cm.conversation_id
		 WHERE cm.member_kind = 'human' AND cm.member_id = $1 AND cm.left_at IS NULL AND c.archived_at IS NULL`,
		humanID).Scan(&n)
	return n, err
}

// Addressed are the agents a message speaks to — the ones whose triage
// decides about it. In a direct conversation that is the other member when
// it is an agent: it answers every message. In a group an agent answers only
// when addressed: mentioned by @slug or @name, or replied to. Several may be
// addressed at once; a group where nobody is addressed is people talking.
func Addressed(c Conversation, text string, replyTo *Message) []Member {
	var agenten []Member
	for _, m := range c.Active() {
		if m.Kind == MemberAgent {
			agenten = append(agenten, m)
		}
	}
	if c.Kind == KindDirect {
		return agenten
	}
	klein := strings.ToLower(text)
	var out []Member
	for _, a := range agenten {
		if replyTo != nil && replyTo.By(a.Ref()) || erwaehnt(klein, a.Slug) || erwaehnt(klein, a.Name) {
			out = append(out, a)
		}
	}
	return out
}

// erwaehnt: "@name" stands in the text and does not run on into a longer
// word — "@ada" is not "@adam".
func erwaehnt(klein, name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	nadel := "@" + name
	for i := 0; ; {
		j := strings.Index(klein[i:], nadel)
		if j < 0 {
			return false
		}
		ende := i + j + len(nadel)
		if ende == len(klein) || !wortzeichen(klein[ende]) {
			return true
		}
		i = ende
	}
}

func wortzeichen(b byte) bool {
	return b == '-' || b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 0x80
}

// Message reads one message.
func (s *Store) Message(ctx context.Context, id uuid.UUID) (Message, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+messageCols+` FROM conversation_messages m`+messageJoins+` WHERE m.id = $1`, id)
	if err != nil {
		return Message{}, err
	}
	out, err := scanMessages(rows)
	if err != nil {
		return Message{}, err
	}
	if len(out) == 0 {
		return Message{}, ErrNotFound
	}
	return out[0], nil
}

// SetTitle renames a group.
func (s *Store) SetTitle(ctx context.Context, id uuid.UUID, title string) error {
	_, err := s.pool.Exec(ctx, `UPDATE conversations SET title = $2 WHERE id = $1 AND kind = 'group'`, id, title)
	return err
}
