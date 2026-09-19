package httpapi

// The chat: a door into the backlog for somebody who only wants to hand over
// work (#298).
//
// It is not a second orchestration path and not a conversational runtime. A
// message becomes a task, a reply becomes the resume input of a parked task,
// and everything the agent produces on the way — notes, its question, the
// result — is read back out of the objects that already carry it. The agent
// works its backlog exactly as before and does not know it is being chatted
// with.
//
// The thread is a VIEW, like the inbox next door: one query over three tables
// (backlog_tasks, task_notes, task_transitions), assembled here rather than in
// the browser. Composed client-side it would be two requests per task — twenty
// for a thread somebody scrolls once.
//
// What is deliberately NOT here: a role that sees only this surface. The five
// seat roles all describe somebody who administers something, and handing out
// agent_owner so that a colleague may type a sentence hands out the config of
// every agent with it. Until that role exists, the chat is open to the same
// people who may create a task by hand — which is the honest state, not the
// target state.

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/backlog"
)

// chatEntry is one line of the thread. The kinds:
//
//	message  — what a person handed over (the task itself)
//	note     — what the agent wrote down along the way
//	question — what it wants to know before it goes on (it is parked)
//	result   — what came out
//	error    — what went wrong
//
// Author carries the origin of a message ("chat:someone@example.org") or the
// author of a note ("agent", "human:someone@example.org"); the surface only
// has to decide left or right from it.
type chatEntry struct {
	Kind      string    `json:"kind"`
	TaskID    uuid.UUID `json:"task_id"`
	TaskTitle string    `json:"task_title"`
	TaskState string    `json:"task_state"`
	Author    string    `json:"author"`
	Text      string    `json:"text"`
	At        time.Time `json:"at"`
}

type chatThread struct {
	Entries []chatEntry `json:"entries"`
}

// threadTasks is how far back a thread reaches. Whoever wants more than the
// last twenty exchanges with one agent is looking for a task, and the backlog
// has the search for that.
const threadTasks = 20

// handleThread assembles the conversation with one agent.
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}

	// The archive stays out: what somebody filed away is not part of the
	// conversation any more.
	const q = `WITH t AS (
		SELECT id, title, body, state, origin, result, error, created_at, updated_at
		FROM backlog_tasks
		WHERE agent_id=$1 AND archived_at IS NULL
		ORDER BY created_at DESC LIMIT $2
	)
	SELECT 'message', t.id, t.title, t.state, t.origin,
	       -- The body, not title plus body: a chat message IS its body, and its
	       -- first line was made the title. Glued together, every message
	       -- typed here would stand twice.
	       CASE WHEN coalesce(t.body,'')='' THEN t.title ELSE t.body END,
	       t.created_at FROM t
	UNION ALL
	SELECT 'note', n.task_id, t.title, t.state, n.author, n.content, n.created_at
	       FROM task_notes n JOIN t ON t.id = n.task_id
	UNION ALL
	SELECT 'question', tr.task_id, t.title, t.state, 'agent', tr.note, tr.created_at
	       FROM task_transitions tr JOIN t ON t.id = tr.task_id
	       WHERE tr.to_state='blocked'
	UNION ALL
	SELECT 'result', t.id, t.title, t.state, 'agent', t.result, t.updated_at
	       FROM t WHERE t.state='done' AND coalesce(t.result,'') <> ''
	UNION ALL
	SELECT 'error', t.id, t.title, t.state, 'agent', t.error, t.updated_at
	       FROM t WHERE t.state='failed' AND coalesce(t.error,'') <> ''
	ORDER BY 7`

	rows, err := s.Pool.Query(r.Context(), q, id, threadTasks)
	if err != nil {
		mapErr(w, err)
		return
	}
	defer rows.Close()

	out := chatThread{Entries: []chatEntry{}}
	for rows.Next() {
		var e chatEntry
		if err := rows.Scan(&e.Kind, &e.TaskID, &e.TaskTitle, &e.TaskState,
			&e.Author, &e.Text, &e.At); err != nil {
			mapErr(w, err)
			return
		}
		// The question stands in the transition note, where it was written
		// with its state in front of it ("blocked: may I send this?"). In a
		// thread the prefix is the machine talking about itself.
		if e.Kind == "question" {
			e.Text = strings.TrimSpace(strings.TrimPrefix(e.Text, "blocked:"))
			if e.Text == "" {
				continue
			}
		}
		out.Entries = append(out.Entries, e)
	}
	if rows.Err() != nil {
		mapErr(w, rows.Err())
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// chatTitle is the line the backlog shows for a message somebody typed.
//
// A task has a title, a chat message has none — so the first line becomes it
// and the whole text stays in the body. Cutting at a word boundary and not at
// the character keeps a board from filling up with halved words.
func chatTitle(text string) string {
	first := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	if len(first) <= 80 {
		return first
	}
	cut := first[:80]
	if i := strings.LastIndex(cut, " "); i > 40 {
		cut = cut[:i]
	}
	return cut + "…"
}

// handleChatMessage turns a message into a task.
func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Text string `json:"text"`
	}
	text := ""
	if err := readJSON(r, &in); err == nil {
		text = strings.TrimSpace(in.Text)
	}
	if text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}
	// origin says where the work came from, and the chat is one more origin
	// beside manual, schedule and webhook:… — not a new kind of task.
	t, err := s.Backlog.Create(r.Context(), p.OrgID, id, chatTitle(text), text, "chat:"+p.Email, 0)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

// chatReply is what comes back from a reply: the note always, the task only
// when the reply actually woke somebody.
type chatReply struct {
	Note  backlog.Note  `json:"note"`
	Task  *backlog.Task `json:"task,omitempty"`
	Woken bool          `json:"woken"`
}

// handleTaskReply answers one task.
//
// Two things happen, and in this order: the reply is written to the task as a
// note — it belongs to the record whatever else follows — and if the agent was
// waiting for it, the text becomes the resume input and wakes it.
//
// A task nobody is waiting on keeps the note and says so with woken=false.
// That is not an error: somebody wrote something down on a piece of work, and
// the agent will read it on its next run.
func (s *Server) handleTaskReply(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	var in struct {
		Text string `json:"text"`
	}
	text := ""
	if err := readJSON(r, &in); err == nil {
		text = strings.TrimSpace(in.Text)
	}
	if text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return
	}

	note, err := s.Backlog.AddNote(r.Context(), id, "human:"+p.Email, text)
	if err != nil {
		mapErr(w, err)
		return
	}

	out := chatReply{Note: note}
	t, err := s.Backlog.Answer(r.Context(), id, p.Email, text)
	switch {
	case err == nil:
		out.Task, out.Woken = &t, true
	case errors.Is(err, backlog.ErrInvalidTransition):
		// nobody was waiting — the note stands
	default:
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
