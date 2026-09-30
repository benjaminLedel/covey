package httpapi

// The chat with an agent (#298), since #440 a conversation between members
// (conversations.go): what the person and the agent say, beside the backlog
// and not a view on it. This file holds the per-agent thread endpoints, which
// stay as aliases onto the person's direct conversation with the agent, and
// the triage that decides what a message to an agent is.
//
// It is not a second orchestration path and not a conversational runtime. A
// message becomes a task or an answer, a reply becomes the resume input of a
// parked task, and the agent works its backlog exactly as before. What a task
// opened in a conversation reports back is written there as a message; the
// rest of the backlog stays in the backlog.
//
// What is deliberately NOT here: a role that sees only this surface. The five
// seat roles all describe somebody who administers something, and handing out
// agent_owner so that a colleague may type a sentence hands out the config of
// every agent with it. Until that role exists, the chat is open to the same
// people who may create a task by hand — which is the honest state, not the
// target state.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"covey/internal/agents"
	"covey/internal/backlog"
	"covey/internal/chat"
	"covey/internal/identity"
	"covey/internal/llm"
	"covey/internal/orchestrator"
	"covey/internal/push"
	"covey/internal/voice"
)

// chatEntry is one line of the thread. The kinds:
//
//	message  — what the person wrote
//	answer   — what the agent said
//	question — what a task opened here wants to know (it is parked)
//	result   — what came out of it
//	error    — what went wrong
//
// `note` was the agent's notes on its tasks; since #440 the thread mirrors
// nothing of the backlog, and the kind no longer occurs. Author is
// "chat:someone@example.org" for the person and "agent" for the agent; the
// surface only has to decide left or right from it.
type chatEntry struct {
	Kind string `json:"kind"`
	/* Die eigene Kennung des Eintrags — die Aufgabe, wenn es eine gibt, sonst
	   die Nachricht. Die Oberfläche gruppiert danach. */
	ID uuid.UUID `json:"id"`
	/* Die Aufgabe, falls daraus Arbeit wurde. Fehlt bei einer Nachricht, die
	   der Agent einfach beantwortet hat — und das ist der ganze Punkt von
	   #302: Nicht jede Zeile hat einen Vorgang. */
	TaskID    *uuid.UUID `json:"task_id,omitempty"`
	TaskTitle string     `json:"task_title"`
	TaskState string     `json:"task_state"`
	Author    string     `json:"author"`
	Text      string     `json:"text"`
	At        time.Time  `json:"at"`
	/* The drafts a hiring task produced (#327). Only on a result of the
	   People department, read off the recording where the platform wrote them
	   (hiring.go, rule 3) — never off what the agent reported. The way to the
	   draft is the agent page, where hiring already is; there is no hire
	   action, and the thread does not pretend otherwise. */
	Drafts []entwurfKurz `json:"drafts,omitempty"`
	/* On a result or an error: what the agent said about it in the chat
	   (#411) — a few sentences told from the report, which stays in Text.
	   The surfaces show this and keep the report one tap away. Empty: not
	   told (no triage, not yet, or nothing to tell with). */
	Said string `json:"said,omitempty"`
}

// entwurfKurz is a drafted colleague as the thread shows it: enough to
// recognise it and to get to its page.
type entwurfKurz struct {
	ID          uuid.UUID  `json:"id"`
	Slug        string     `json:"slug"`
	DisplayName string     `json:"display_name"`
	JobTitle    string     `json:"job_title"`
	HiredAt     *time.Time `json:"hired_at,omitempty"`
}

/*
offenerVorgang ist ein Hintergrundvorgang, wie die Leiste ihn zeigt: was er

	ist, wo er steht, woher er kam und seit wann er sich nicht mehr gerührt
	hat. Der Schritt kommt nicht von hier — den hat die Schale schon aus
	`/org/running`, und ihn zweimal zu holen hieße, ihn zweimal zu bezahlen.
*/
type offenerVorgang struct {
	ID      uuid.UUID `json:"id"`
	Title   string    `json:"title"`
	State   string    `json:"state"`
	Origin  string    `json:"origin"`
	Created time.Time `json:"created_at"`
	Updated time.Time `json:"updated_at"`
}

// chatMark is one emoji on one task, already grouped: wie oft, und ob ich
// selbst dabei bin. Die Oberfläche braucht beides und sonst nichts.
type chatMark struct {
	Emoji string `json:"emoji"`
	Count int    `json:"count"`
	Mine  bool   `json:"mine"`
	/* Wer — gekürzt auf die ersten drei, für den Tooltip. Eine Liste von
	   vierzig Namen im Verlauf liest niemand. */
	Who []string `json:"who"`
}

type chatThread struct {
	Entries []chatEntry `json:"entries"`
	/* Denkt gerade nach: Es liegt eine angenommene Nachricht vor, für die die
	   Triage noch keine Entscheidung hat. Das ist das eine, was NICHT im
	   Verlauf steht — es ist kein Eintrag, sondern ein Zustand zwischen zwei
	   Einträgen. Der Ereignisstrom meldet es sofort; hier steht es, damit es
	   ein Neuladen der Seite übersteht. */
	Pending bool `json:"pending"`
	/* Was im Hintergrund läuft: die Vorgänge dieses Agenten, die noch nicht
	   fertig sind. Sie stehen NICHT in den Einträgen — dort steht, was gesagt
	   wurde, und ein Vorgang, der seit einer Stunde läuft, hat seit einer
	   Stunde nichts gesagt. Genau deshalb braucht er eine eigene Anzeige
	   (#308). */
	Tasks []offenerVorgang `json:"tasks"`
	/* Reaktionen je Aufgabe. Sie hängen an der Aufgabe und nicht am Eintrag
	   (internal/backlog/reactions.go) — die Oberfläche zeigt sie deshalb am
	   ersten Eintrag eines Vorgangs. */
	Marks map[string][]chatMark `json:"marks"`
	/* The direct conversation behind the thread (#440), once there is one,
	   and whether the person muted it. */
	ConversationID *uuid.UUID `json:"conversation_id,omitempty"`
	Muted          bool       `json:"muted"`
	// CanWrite: the organisation's reach lets the reader write to the agent.
	CanWrite bool `json:"can_write"`
}

// threadTasks is how far back a thread reaches. Whoever wants more than the
// last twenty exchanges with one agent is looking for a task, and the backlog
// has the search for that.
const threadTasks = 20

// threadMessages: wie viele Nachrichten dazukommen. Doppelt so viele wie
// Aufgaben, weil eine beantwortete Nachricht keine Aufgabe erzeugt — ein
// Verlauf kann also aus deutlich mehr Zeilen als Vorgängen bestehen.
const threadMessages = 40

/* sucheMessages: das Fenster, wenn gesucht wird.
 *
 * Zehnmal so weit wie das des Verlaufs. Weiter nicht: Wer darüber hinaus
 * sucht, sucht im Backlog, und der Verweis daneben führt dorthin — eine
 * Volltextsuche über die ganze Geschichte eines Agenten ist eine andere Sache
 * als das Durchsehen eines Gesprächs. */
const sucheMessages = 400

// handleThread is the direct conversation of the signed-in person with one
// agent, in the shape the thread had before conversations (#440) — the
// current app reads it, and so does the web's agent thread.
//
// It reads the conversation and nothing else: what the person and the agent
// said, and what a task opened here reported back. The agent's other tasks
// are not in it; what is still running stands beside it (Tasks), because the
// backlog is shared. A person who never wrote to the agent gets an empty
// thread, and no conversation is opened by looking.
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	out := chatThread{Entries: []chatEntry{}, Tasks: []offenerVorgang{}, Marks: map[string][]chatMark{}}
	convID, gibt, err := s.Chat.FindDirect(r.Context(), p.OrgID, chat.Human(p.ID), chat.Agent(id))
	if err != nil {
		mapErr(w, err)
		return
	}

	/* ?q= sucht im Gespräch statt es zu zeigen. Dafür reicht das Fenster
	   weiter zurück: Wer sucht, sucht das, was er nicht mehr sieht. */
	begriff := strings.TrimSpace(r.URL.Query().Get("q"))
	if gibt {
		var msgs []chat.Message
		if begriff != "" {
			msgs, err = s.Chat.Search(r.Context(), convID, []string{begriff}, sucheMessages)
			// Newest first from the search; a thread reads oldest first.
			for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
				msgs[i], msgs[j] = msgs[j], msgs[i]
			}
		} else {
			msgs, err = s.Chat.Page(r.Context(), convID, nil, threadMessages)
		}
		if err != nil {
			mapErr(w, err)
			return
		}
		for _, m := range msgs {
			out.Entries = append(out.Entries, alsEintrag(m, p.Email))
		}
	}

	/* Bei einer Suche bleibt es bei den Treffern. Reaktionen, der Denkzustand
	   und die Hintergrundvorgänge gehören zum Gespräch, nicht zu einer Liste
	   von Fundstellen — und drei Abfragen, die niemand ansieht, sind drei
	   Abfragen zu viel auf einem Weg, der bei jedem Tastendruck läuft. */
	if begriff != "" {
		writeJSON(w, http.StatusOK, out)
		return
	}

	if err := s.entwuerfeAnhaengen(r.Context(), id, out.Entries); err != nil {
		mapErr(w, err)
		return
	}
	out.Marks, err = s.marksOf(r, out.Entries)
	if err != nil {
		mapErr(w, err)
		return
	}
	out.Tasks, err = s.offeneVorgaenge(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if out.CanWrite, err = s.Chat.Reaches(r.Context(), p.OrgID, p.ID, id, p.Role == identity.RoleOrgAdmin); err != nil {
		mapErr(w, err)
		return
	}
	if gibt {
		if out.Pending, err = s.Chat.Pending(r.Context(), convID); err != nil {
			mapErr(w, err)
			return
		}
		out.ConversationID = &convID
		if c, err := s.Chat.Get(r.Context(), convID); err == nil {
			for _, m := range c.Active() {
				if m.Kind == chat.MemberHuman && m.ID == p.ID {
					out.Muted = m.Muted
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

/*
alsEintrag is a message of the conversation as a line of the old thread.

	What a person wrote is a `message` with the author the thread always
	had ("chat:<mail>"); what the agent said is an `answer`; a task's report
	is a `result` or an `error` whose Text is the report and whose Said is
	what the message retells of it, and a parked task's question is a
	`question`. The entry's id is the task's where there is one — the
	surfaces group by it.
*/
func alsEintrag(m chat.Message, email string) chatEntry {
	e := chatEntry{
		ID: m.ID, TaskID: m.TaskID, TaskTitle: m.TaskTitle, TaskState: m.TaskState,
		Text: m.Text, At: m.CreatedAt, Author: "agent", Kind: "answer",
	}
	if m.TaskID != nil {
		e.ID = *m.TaskID
	}
	switch {
	case m.AuthorKind == chat.MemberHuman:
		e.Kind, e.Author = "message", "chat:"+email
	case m.Kind == chat.MessageResult || m.Kind == chat.MessageError:
		e.Kind = m.Kind
		if m.Report != "" && m.Report != m.Text {
			e.Text, e.Said = m.Report, m.Text
		}
	case m.Kind == chat.MessageQuestion:
		e.Kind = "question"
	}
	return e
}

/*
entwuerfeAnhaengen puts the drafts a hiring task produced onto its result

	(#327). Only for the People department — for everyone else the query would
	be a query for nothing — and read off the recording, where the platform
	wrote the provenance (hiring.go, rule 3). One query for the whole thread,
	not one per task.
*/
func (s *Server) entwuerfeAnhaengen(ctx context.Context, agentID uuid.UUID, entries []chatEntry) error {
	a, err := s.Registry.Get(ctx, agentID)
	if err != nil || a.Slug != peopleSlug {
		return nil
	}
	var tasks []uuid.UUID
	for _, e := range entries {
		if e.Kind == "result" && e.TaskID != nil {
			tasks = append(tasks, *e.TaskID)
		}
	}
	if len(tasks) == 0 {
		return nil
	}
	rows, err := s.Pool.Query(ctx, `SELECT DISTINCT task_id, payload->>'drafted_agent'
		FROM recording_events WHERE task_id = ANY($1) AND kind='lifecycle'
		  AND payload->>'status'='agent_drafted'`, tasks)
	if err != nil {
		return err
	}
	defer rows.Close()
	je := map[uuid.UUID][]entwurfKurz{}
	for rows.Next() {
		var taskID uuid.UUID
		var raw string
		if rows.Scan(&taskID, &raw) != nil {
			continue
		}
		id, perr := uuid.Parse(raw)
		if perr != nil {
			continue
		}
		/* A draft that was rejected is deleted (spec/20) — then it is gone
		   from the thread too, and nothing here mourns it. */
		d, gerr := s.Registry.Get(ctx, id)
		if gerr != nil {
			continue
		}
		je[taskID] = append(je[taskID], entwurfKurz{ID: d.ID, Slug: d.Slug, DisplayName: d.DisplayName, JobTitle: d.JobTitle, HiredAt: d.HiredAt})
	}
	if rows.Err() != nil {
		return rows.Err()
	}
	for i := range entries {
		if entries[i].Kind == "result" && entries[i].TaskID != nil {
			entries[i].Drafts = je[*entries[i].TaskID]
		}
	}
	return nil
}

/*
offeneVorgaenge sind die Vorgänge, die noch laufen — das, was nach einer

	Nachricht im Hintergrund weitergeht.

*
* Alle, nicht nur die aus diesem Gespräch: Was ein Kollege sonst noch auf dem
* Tisch hat, ist der Grund, warum er auf das hier noch nicht gekommen ist. Die
* Herkunft steht daneben, damit man das eigene wiedererkennt.
*/
func (s *Server) offeneVorgaenge(ctx context.Context, agentID uuid.UUID) ([]offenerVorgang, error) {
	rows, err := s.Pool.Query(ctx,
		`SELECT id, title, state, origin, created_at, updated_at
		   FROM backlog_tasks
		  WHERE agent_id=$1 AND archived_at IS NULL
		    AND state IN ('open','in_progress','blocked')
		  ORDER BY CASE state WHEN 'in_progress' THEN 0 WHEN 'blocked' THEN 1 ELSE 2 END,
		           created_at
		  LIMIT $2`, agentID, threadTasks)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []offenerVorgang{}
	for rows.Next() {
		var v offenerVorgang
		if err := rows.Scan(&v.ID, &v.Title, &v.State, &v.Origin, &v.Created, &v.Updated); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// marksOf collects the reactions of every task in the thread and groups them
// the way the surface reads them.
func (s *Server) marksOf(r *http.Request, entries []chatEntry) (map[string][]chatMark, error) {
	gesehen := map[uuid.UUID]bool{}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if e.TaskID == nil || gesehen[*e.TaskID] {
			continue
		}
		gesehen[*e.TaskID] = true
		ids = append(ids, *e.TaskID)
	}
	roh, err := s.Backlog.ReactionsByTasks(r.Context(), ids)
	if err != nil {
		return nil, err
	}
	ich := "human:" + principalFrom(r).Email
	out := map[string][]chatMark{}
	for id, liste := range roh {
		/* Die Reihenfolge ist die des ersten Auftretens, nicht die der
		   Häufigkeit: Eine Zeile, die beim Zählen umspringt, liest sich wie
		   ein Fehler. */
		pos := map[string]int{}
		for _, r := range liste {
			i, da := pos[r.Emoji]
			if !da {
				pos[r.Emoji] = len(out[id.String()])
				out[id.String()] = append(out[id.String()], chatMark{Emoji: r.Emoji})
				i = pos[r.Emoji]
			}
			m := &out[id.String()][i]
			m.Count++
			if r.Author == ich {
				m.Mine = true
			}
			if len(m.Who) < 3 {
				m.Who = append(m.Who, r.Author)
			}
		}
	}
	return out, nil
}

// handleReact toggles one reaction on one task.
func (s *Server) handleReact(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	var in struct {
		Emoji string `json:"emoji"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: expected {\"emoji\": \"…\"}")
		return
	}
	/* Ein Zeichen, nicht ein Satz. Die Grenze steht hier und nicht in der
	   Oberfläche: Ein zweiter Client wüsste nichts von ihr, und „Reaktion"
	   wäre dann ein Feld für Fließtext mit anderem Namen. */
	emoji := strings.TrimSpace(in.Emoji)
	if emoji == "" || len([]rune(emoji)) > 3 {
		writeErr(w, http.StatusBadRequest, "emoji is required and must be at most 3 characters")
		return
	}
	gesetzt, err := s.Backlog.React(r.Context(), id, emoji, "human:"+principalFrom(r).Email)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"set": gesetzt})
}

// chatTitle is the line the backlog shows for a message somebody typed.
//
// A task has a title, a chat message has none — so the first line becomes it
// and the whole text stays in the body. Cutting at a word boundary and not in
// the middle of a word keeps a board from filling up with halved words.
//
// The cut counts CHARACTERS, not bytes. Cut by bytes, a message in Japanese or
// Chinese — no spaces, three bytes per character — is severed inside a
// character, and the half character that is left is not valid UTF-8: Postgres
// refuses the insert (22021), the API answers 500, and the message is gone.
// This interface ships in ten languages, two of which hit that on an ordinary
// sentence.
func chatTitle(text string) string {
	first := strings.TrimSpace(strings.SplitN(text, "\n", 2)[0])
	zeichen := []rune(first)
	if len(zeichen) <= 80 {
		return first
	}
	cut := string(zeichen[:80])
	// The word boundary is a bonus, not the rule: a language that does not
	// separate words with spaces simply does not get one, and the cut stays
	// valid because it was made on characters.
	if i := strings.LastIndex(cut, " "); i > len(cut)/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// chatText reads the one field both writes take, and keeps two failures apart
// that used to answer the same sentence: a body that is not valid JSON (or
// carries a field nobody reads) is a different mistake from an empty message,
// and a client sending {"message": …} deserves to be told which one it made.
func chatText(w http.ResponseWriter, r *http.Request) (string, bool) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: expected {\"text\": \"…\"}")
		return "", false
	}
	text := strings.TrimSpace(in.Text)
	if text == "" {
		writeErr(w, http.StatusBadRequest, "text is required")
		return "", false
	}
	return text, true
}

// handleChatMessage writes a message into the person's direct conversation
// with the agent (#440) — the alias the per-agent thread keeps, so that the
// current app goes on working. It answers in the shape it always had: the
// message with the author "chat:<mail>", and without triage the task.
func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	if !s.teamSurfaceAn(w, r) || !s.erreicht(w, r, id) || s.gestoppt(w, r, id) {
		return
	}
	text, ok := chatText(w, r)
	if !ok {
		return
	}
	convID, err := s.Chat.Direct(r.Context(), p.OrgID, chat.Human(p.ID), chat.Agent(id), &p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	conv, err := s.Chat.Get(r.Context(), convID)
	if err != nil {
		mapErr(w, err)
		return
	}
	a, err := s.annehmen(r.Context(), conv, sprecherVon(p), text, nil, langFrom(r))
	if err != nil {
		mapErr(w, err)
		return
	}
	alt := map[string]any{
		"id": a.msg.ID, "org_id": a.msg.OrgID, "agent_id": id, "conversation_id": convID,
		"author": "chat:" + p.Email, "text": a.msg.Text, "created_at": a.msg.CreatedAt,
		"triage_state": a.msg.TriageState,
	}
	if a.pending {
		writeJSON(w, http.StatusAccepted, map[string]any{"message": alt, "pending": true})
		return
	}
	var task any
	if len(a.tasks) > 0 {
		alt["task_id"] = a.tasks[0].ID
		task = a.tasks[0]
	}
	writeJSON(w, http.StatusCreated, map[string]any{"message": alt, "task": task, "answered": false})
}

// teamSurfaceAn refuses a message while the organisation has not turned the
// team surface on (#328). Checked here and not only in the interface, since
// the interface is not the only client of these endpoints.
func (s *Server) teamSurfaceAn(w http.ResponseWriter, r *http.Request) bool {
	on, err := s.Chat.TeamSurface(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return false
	}
	if !on {
		writeErr(w, http.StatusForbidden, "the team surface is not enabled for this organisation")
		return false
	}
	return true
}

// sprecher is who wrote a message, with what the triage needs of them.
type sprecher struct {
	id    uuid.UUID
	email string
	role  string
}

func sprecherVon(p identity.Principal) sprecher {
	return sprecher{id: p.ID, email: p.Email, role: p.Role}
}

// angenommen is what became of a message: the message, the tasks opened
// without triage, and whether a triage still owes it a decision.
type angenommen struct {
	msg     chat.Message
	tasks   []backlog.Task
	pending bool
}

/*
annehmen writes a person's message and hands it to the agents it

	addresses (chat.Addressed): in a direct conversation the agent, in a group
	whoever is mentioned or replied to, in a conversation of people nobody.

	The message is written down first, always — before any model has seen it
	and whatever the triage decides afterwards. What somebody said is a fact;
	what is made of it is a judgement, and a judgement that fails must not
	swallow the fact.

	With the triage on, the request does not wait for the decision (202): the
	turn runs beside it, and what comes of it arrives over the event stream
	(`/api/v1/events`) the surfaces listen to anyway. Without triage there is
	nothing to wait for — a task costs one insert — and the caller gets it in
	the same answer.
*/
func (s *Server) annehmen(ctx context.Context, conv chat.Conversation, wer sprecher, text string, replyTo *chat.Message, lang string) (angenommen, error) {
	var out angenommen
	var agenten []uuid.UUID
	for _, m := range chat.Addressed(conv, text, replyTo) {
		/* A stopped agent takes no work (#414): in a direct conversation the
		   request is refused before this; in a group it is simply not
		   addressed, and the others still are. */
		var stop bool
		if err := s.Pool.QueryRow(ctx, `SELECT a.killed OR o.fleet_killed
			  FROM agents a JOIN organizations o ON o.id = a.org_id WHERE a.id = $1`, m.ID).Scan(&stop); err != nil || stop {
			continue
		}
		agenten = append(agenten, m.ID)
	}
	triage := false
	if len(agenten) > 0 {
		mode, err := s.Chat.Mode(ctx, conv.OrgID)
		if err != nil {
			return out, err
		}
		triage = mode == chat.TriageOn
	}
	msg := chat.Message{
		ConversationID: conv.ID, AuthorKind: chat.MemberHuman, AuthorID: &wer.id,
		Text: text, Kind: chat.MessageText,
	}
	if replyTo != nil {
		msg.ReplyTo = &replyTo.ID
	}
	if triage {
		msg.TriageState = chat.StatePending
	}
	msg, _, err := s.Chat.Post(ctx, msg)
	if err != nil {
		return out, err
	}
	out.msg = msg
	if len(agenten) == 0 {
		s.chatEreignis(conv.OrgID, uuid.Nil, conv.ID, "message", nil)
		return out, nil
	}
	if !triage {
		for _, agentID := range agenten {
			t, err := s.aufgabeAusNachricht(ctx, conv, agentID, msg, chat.Entscheidung{}, text, wer.email, lang)
			if err != nil {
				return out, err
			}
			out.tasks = append(out.tasks, t)
		}
		for _, agentID := range agenten {
			s.chatEreignis(conv.OrgID, agentID, conv.ID, "task", nil)
		}
		return out, nil
	}

	/* Angenommen. Der Zug läuft daneben, mit einem Kontext, der nicht am
	   Request hängt: Dessen Kontext wird abgebrochen, sobald die Antwort
	   geschrieben ist, und ein Zug, den das Abschicken der Antwort abbricht,
	   liefe nie zu Ende. */
	go s.triageLauf(conv, agenten, msg, wer.email, lang)
	for _, agentID := range agenten {
		s.chatEreignis(conv.OrgID, agentID, conv.ID, "thinking", nil)
	}
	out.pending = true
	return out, nil
}

// triageLaufFrist: so lange darf ein Zug dauern. Ein Modell, das länger
// braucht, hat nicht geantwortet — und die Nachricht wird zur Aufgabe, was
// ohnehin die Antwort auf jeden Fehler ist.
const triageLaufFrist = 90 * time.Second

/* triageLauf ist der Zug außerhalb des Requests — einer je angesprochenem
 * Agenten, nacheinander.
 *
 * Er endet IMMER mit einem Zustand an der Nachricht und einem Ereignis —
 * auch beim Absturz eines Modells, auch bei einem Fehler in der Datenbank.
 * Was er nicht darf, ist still enden: Eine angenommene Nachricht, zu der nie
 * etwas kommt, ist schlimmer als eine überflüssige Aufgabe.
 */
func (s *Server) triageLauf(conv chat.Conversation, agenten []uuid.UUID, msg chat.Message, email, lang string) {
	ctx, abbrechen := context.WithTimeout(context.Background(), triageLaufFrist*time.Duration(len(agenten)))
	defer abbrechen()

	/* The events go out after the state is written: a surface that reloads
	   on the event must not still read "thinking". */
	type ereignis struct {
		agentID uuid.UUID
		zustand string
		daten   map[string]string
	}
	var ereignisse []ereignis
	zustand := chat.StateDone
	for _, agentID := range agenten {
		entscheidung, offen, grund := s.triagieren(ctx, conv, agentID, msg, email)
		if a, err := s.Registry.Get(ctx, agentID); err == nil {
			entscheidung = entscheidungFuer(a.Slug, entscheidung)
		}
		ergebnis, daten, err := s.entscheidungAnwenden(ctx, conv, agentID, msg, entscheidung, offen, email, lang)
		/* Why there was no answer (#416): a triage that failed became a task in
		   silence, and the reason stood only in the server log. It goes onto the
		   task as a triage note — visible at the task, kept out of the thread. */
		if err == nil && grund != "" && daten["task_id"] != "" {
			if id, perr := uuid.Parse(daten["task_id"]); perr == nil {
				if _, nerr := s.Backlog.AddNote(ctx, id, "triage:covey", "The triage could not decide, so the message became a task: "+grund); nerr != nil {
					s.Log.Warn("triage: the reason was not noted", "task", id, "err", nerr)
				}
			}
		}
		if err != nil {
			s.Log.Error("chat triage could not be applied", "agent", agentID, "message", msg.ID, "err", err)
			zustand = chat.StateFailed
			ereignisse = append(ereignisse, ereignis{agentID, "failed", nil})
			continue
		}
		ereignisse = append(ereignisse, ereignis{agentID, ergebnis, daten})
	}
	if err := s.Chat.SetTriage(ctx, msg.ID, zustand); err != nil {
		s.Log.Warn("chat message stays pending", "message", msg.ID, "err", err)
	}
	for _, e := range ereignisse {
		s.chatEreignis(conv.OrgID, e.agentID, conv.ID, e.zustand, e.daten)
	}
}

/*
entscheidungAnwenden führt aus, was die Triage beschlossen hat, und gibt

	zurück, was daraus geworden ist. Ein Fehler hier ist ein echter Fehler —
	die weiche Behandlung („im Zweifel eine Aufgabe") sitzt eine Ebene tiefer
	in triagieren(), wo sie hingehört.
*/
func (s *Server) entscheidungAnwenden(
	ctx context.Context,
	conv chat.Conversation,
	agentID uuid.UUID,
	msg chat.Message,
	entscheidung chat.Entscheidung,
	offen map[string]uuid.UUID,
	email, lang string,
) (string, map[string]string, error) {
	/* Die Notiz an eine laufende Aufgabe: Statt eines zweiten Vorgangs für
	   dieselbe Sache bekommt der bestehende, was dazugekommen ist — und wenn
	   er auf eine Antwort wartete, weckt ihn das über denselben Weg wie eine
	   Antwort von Hand. */
	if entscheidung.Aktion == chat.AktionNotiz {
		ziel, bekannt := offen[entscheidung.Aufgabe]
		if !bekannt {
			/* Eine Kennung, die es nicht gibt: Das Modell hat sich eine
			   ausgedacht, und dann ist es eine neue Aufgabe — nie eine
			   stillschweigend verworfene Nachricht. */
			entscheidung = chat.Entscheidung{Aktion: chat.AktionAufgabe}
		} else {
			/* `triage:` und nicht `human:`: Die Notiz ist das, was der Zug aus
			   der Nachricht gemacht hat, damit der Lauf sie versteht — nicht
			   das, was jemand gesagt hat. Das Gesagte steht schon als
			   Nachricht im Gespräch. */
			if _, err := s.Backlog.AddNote(ctx, ziel, "triage:"+email, entscheidung.Text); err != nil {
				return "", nil, err
			}
			geweckt := "false"
			if _, err := s.Backlog.Answer(ctx, ziel, email, entscheidung.Text); err == nil {
				geweckt = "true"
			}
			if err := s.Chat.LinkTask(ctx, msg.ID, ziel); err != nil {
				return "", nil, err
			}
			daten := map[string]string{"task_id": ziel.String(), "woken": geweckt}
			/* What the person hears (#460): the note stands at the task and
			   nowhere else, and a message that added something and asked
			   something got no word back. */
			if antwort := strings.TrimSpace(entscheidung.Antwort); antwort != "" {
				if _, _, err := s.Chat.Post(ctx, chat.Message{
					ConversationID: conv.ID, AuthorKind: chat.MemberAgent, AuthorID: &agentID,
					Text: antwort, TaskID: &ziel, ReplyTo: &msg.ID, Meta: entscheidung.Meta,
				}); err != nil {
					s.Log.Warn("chat: the reply to a note was not written", "agent", agentID, "err", err)
				} else {
					daten["answered"] = "true"
				}
			}
			return "noted", daten, nil
		}
	}

	if entscheidung.Aktion == chat.AktionAntwort {
		if _, _, err := s.Chat.Post(ctx, chat.Message{
			ConversationID: conv.ID, AuthorKind: chat.MemberAgent, AuthorID: &agentID,
			Text: entscheidung.Text, ReplyTo: &msg.ID, Meta: entscheidung.Meta,
		}); err != nil {
			return "", nil, err
		}
		return "answered", nil, nil
	}

	t, err := s.aufgabeAusNachricht(ctx, conv, agentID, msg, entscheidung, msg.Text, email, lang)
	if err != nil {
		return "", nil, err
	}
	/* What a colleague says before going off to do it (#411): a task that
	   started in silence left the person looking at a background item. The
	   triage wrote the sentence; a task opened without it (the triage failed,
	   or the People department's brief) stays quiet as before. */
	if ack := strings.TrimSpace(entscheidung.Text); ack != "" {
		if _, _, err := s.Chat.Post(ctx, chat.Message{
			ConversationID: conv.ID, AuthorKind: chat.MemberAgent, AuthorID: &agentID,
			Text: ack, TaskID: &t.ID, ReplyTo: &msg.ID, Meta: entscheidung.Meta,
		}); err != nil {
			s.Log.Warn("chat: the acknowledgement was not written", "agent", agentID, "err", err)
		}
	}
	return "task", map[string]string{"task_id": t.ID.String()}, nil
}

/*
aufgabeAusNachricht legt den Vorgang an und hängt die Nachricht daran.

	`origin` sagt, woher die Arbeit kam, und der Chat ist eine Herkunft neben
	manual, schedule und webhook:… — keine neue Art Aufgabe. Wohin sie sich
	zurückmeldet, steht daneben (conversation_id, #440): in das Gespräch, aus
	dem sie kam, und nirgends sonst.
*/
func (s *Server) aufgabeAusNachricht(
	ctx context.Context,
	conv chat.Conversation,
	agentID uuid.UUID,
	msg chat.Message,
	entscheidung chat.Entscheidung,
	text, email, lang string,
) (backlog.Task, error) {
	titel, rumpf := entscheidung.Titel, entscheidung.Rumpf
	if titel == "" {
		titel, rumpf = chatTitle(text), text
	}
	/* A message to the People department is a brief (#327). Her playbook
	   counts on the frame the console's brief builds — the company, the
	   connected target systems, who asked — and a bare sentence made her
	   guess at all three. The same body, from the same builder, so that the
	   two doors lead into the same room. */
	if a, err := s.Registry.Get(ctx, agentID); err == nil && a.Slug == peopleSlug {
		/* The title too: the triage's reading of a brief is the wrong one
		   ("clarify which tickets were handled" for "somebody who handles the
		   tickets"), and the brief's title is the person's own first line. */
		titel = briefTitle(lang, text)
		rumpf = s.briefBody(ctx, identity.Principal{OrgID: conv.OrgID, Email: email}, lang, text, "", "", "")
	}
	/* What the message refers to (#413). A run sees its task and not the
	   conversation; "do the same for Initech" or "and the other one?" needs
	   what came before. The last part of the conversation goes into the
	   body, the message itself excepted — it is the task. */
	rumpf += s.gespraechFuerLauf(ctx, conv.ID, agentID, msg.ID)
	t, err := s.Backlog.CreateIn(ctx, conv.OrgID, agentID, titel, rumpf, "chat:"+email, 0, &conv.ID)
	if err != nil {
		return backlog.Task{}, err
	}
	if err := s.Chat.LinkTask(ctx, msg.ID, t.ID); err != nil {
		return backlog.Task{}, err
	}
	return t, nil
}

/* chatEreignis meldet, was mit einem Gespräch geschehen ist.
 *
 * Es ist dieselbe Leitung, über die Aufgaben, Läufe und Freigaben kommen, und
 * sie ist bereits je Organisation abgegrenzt (broadcast.go). Das Ereignis
 * sagt nur, welches Gespräch sich bewegt hat, nicht was darin steht — die
 * Oberfläche holt es daraufhin selbst, und dort gilt, wer Mitglied ist.
 * `state` ist dabei, weil eine Sache NICHT im Gespräch steht: dass gerade
 * nachgedacht wird. */
func (s *Server) chatEreignis(orgID, agentID, convID uuid.UUID, zustand string, daten map[string]string) {
	if s.Orch == nil {
		return
	}
	d := map[string]string{"state": zustand, "conversation_id": convID.String()}
	for k, v := range daten {
		d[k] = v
	}
	ev := orchestrator.Event{Type: "chat", OrgID: orgID, Data: d}
	if agentID != uuid.Nil {
		ev.AgentID = agentID.String()
	}
	s.Orch.Events().Publish(ev)
}

/* NachholenOffeneTriage holt nach, was ein Neustart unterbrochen hat.
 *
 * Eine Nachricht wird angenommen und dann entschieden; dazwischen liegt ein
 * Zug, der Sekunden dauert. Stirbt die Control Plane in diesem Fenster, stünde
 * die Nachricht für immer auf `pending` — angenommen, aber ohne Antwort und
 * ohne Aufgabe. Das ist genau der Verlust, den die Warteschlange verhindern
 * soll, also wird beim Hochfahren aufgeräumt.
 *
 * Es wird nicht in einer Schleife gepollt: Der gewöhnliche Weg ist die
 * Goroutine aus dem Request, und dies hier ist das Netz darunter. */
func (s *Server) NachholenOffeneTriage(ctx context.Context) {
	if s.Chat == nil {
		return
	}
	offen, err := s.Chat.Liegengeblieben(ctx, 50)
	if err != nil {
		s.Log.Warn("could not look for unfinished chat triage", "err", err)
		return
	}
	for _, m := range offen {
		conv, err := s.Chat.Get(ctx, m.ConversationID)
		if err != nil || m.AuthorID == nil {
			continue
		}
		var email string
		if err := s.Pool.QueryRow(ctx, `SELECT email FROM humans WHERE id = $1`, *m.AuthorID).Scan(&email); err != nil {
			continue
		}
		var bezug *chat.Message
		if m.ReplyTo != nil {
			if b, err := s.Chat.Message(ctx, *m.ReplyTo); err == nil {
				bezug = &b
			}
		}
		var agenten []uuid.UUID
		for _, a := range chat.Addressed(conv, m.Text, bezug) {
			agenten = append(agenten, a.ID)
		}
		if len(agenten) == 0 {
			continue
		}
		s.Log.Info("finishing chat triage left over from a restart", "message", m.ID, "conversation", conv.ID)
		/* The language of the request is gone with the restart; the brief
		   frame then reads in English, which every playbook reads. */
		go s.triageLauf(conv, agenten, m, email, "")
	}
}

// triagieren runs the turn — or says "task" without asking anybody.
//
// Every failure lands on the same answer: open the task. An organisation
// without the switch, without a credential, without a reachable provider, or
// with a model that answered nonsense gets the behaviour it had before #302,
// and nobody has to be told about it. The one thing that must never happen is
// that a message disappears because a model was not available.
//
// What the turn sees (#440): the end of THIS conversation, and — beyond it —
// the agent's own backlog, open and recently finished, which it reads and
// writes; a search reaches further into both. That is prompt context and not
// tool calls: the turn is one JSON decision, and a backlog read costs no
// credential and leaves nothing.
func (s *Server) triagieren(ctx context.Context, conv chat.Conversation, agentID uuid.UUID, msg chat.Message, email string) (chat.Entscheidung, map[string]uuid.UUID, string) {
	aufgabe := chat.Entscheidung{Aktion: chat.AktionAufgabe}

	mode, err := s.Chat.Mode(ctx, conv.OrgID)
	if err != nil || mode != chat.TriageOn {
		return aufgabe, nil, ""
	}
	provider, err := s.resolveOrgLLM(ctx, conv.OrgID)
	if err != nil {
		return aufgabe, nil, "no model is configured for the control plane"
	}
	verlauf, err := s.verlaufFuer(ctx, conv.ID, agentID, msg.ID, triageKontext)
	if err != nil {
		s.Log.Warn("triage: the conversation could not be read — the message becomes a task", "agent", agentID, "err", err)
		return aufgabe, nil, "the conversation could not be read: " + err.Error()
	}
	liste, nach := s.offeneAufgaben(ctx, agentID)
	fertig := s.fertigeAufgaben(ctx, agentID)
	rahmen := chat.Rahmen{
		Rolle: s.rolleVon(ctx, agentID), Seele: s.seeleVon(ctx, agentID),
		Gegenueber: s.gegenueberVon(ctx, conv.OrgID, email),
		Raum:       chat.Raum(conv, agentID, s.nameVon(ctx, conv.OrgID, email), true),
	}
	meta := s.chatStimme(ctx, &rahmen, conv.OrgID, agentID, conv.ID, email)
	organisation := s.organisationVon(ctx, agentID)

	/* Ein Zug, einmal wiederholt (#416): Ein Modell, das einmal nicht
	   antwortet oder unlesbar antwortet, tut es beim zweiten Mal meist doch —
	   und jeder Fehlschlag wird eine Aufgabe, die eine Sandbox hochfährt, um
	   „wie geht's?" zu beantworten. */
	zug := func(suche *chat.Suche) (chat.Entscheidung, error) {
		e, err := chat.Triagieren(ctx, provider, rahmen, organisation, liste, fertig, verlauf, msg.Text, suche)
		if err != nil && ctx.Err() == nil {
			s.Log.Warn("triage turn failed, trying once more", "agent", agentID, "err", err)
			e, err = chat.Triagieren(ctx, provider, rahmen, organisation, liste, fertig, verlauf, msg.Text, suche)
		}
		return e, err
	}
	e, err := zug(nil)
	if err == nil && e.Aktion == chat.AktionSuche {
		treffer := s.suchen(ctx, agentID, conv.ID, organisation, e.Anfrage)
		e, err = zug(&chat.Suche{Anfrage: e.Anfrage, Treffer: treffer})
		if err == nil && e.Aktion == chat.AktionSuche {
			err = errors.New("the model asked to search a second time")
		}
	}
	if err != nil {
		s.Log.Warn("triage failed — the message becomes a task", "agent", agentID, "err", err)
		return aufgabe, nil, err.Error()
	}
	e.Meta = meta
	return e, nach, ""
}

// verlaufFuer is the end of the conversation as a turn of this agent reads
// it: its own lines as "you", everybody else by name. The message being
// decided is left out — the turn gets it as "the new message".
func (s *Server) verlaufFuer(ctx context.Context, convID, agentID, ohne uuid.UUID, n int) ([]chat.Beitrag, error) {
	msgs, err := s.Chat.Page(ctx, convID, nil, n+1)
	if err != nil {
		return nil, err
	}
	var out []chat.Beitrag
	for _, m := range msgs {
		if m.ID == ohne {
			continue
		}
		out = append(out, beitrag(m, agentID))
	}
	if len(out) > n {
		out = out[len(out)-n:]
	}
	return out, nil
}

func beitrag(m chat.Message, agentID uuid.UUID) chat.Beitrag {
	wer := m.AuthorName
	switch {
	case m.By(chat.Agent(agentID)):
		wer = "you"
	case wer == "" && m.AuthorKind == chat.MemberAgent:
		wer = "an AI colleague"
	case wer == "":
		wer = "a person"
	}
	return chat.Beitrag{Wer: wer, Text: m.Text}
}

// laufKontext: how much of the conversation a task from the chat carries.
const laufKontext = 10

// gespraechFuerLauf is the conversation before a message, for the body of the
// task made from it (#413). Empty when there is none.
func (s *Server) gespraechFuerLauf(ctx context.Context, convID, agentID, ohne uuid.UUID) string {
	verlauf, err := s.verlaufFuer(ctx, convID, agentID, ohne, laufKontext)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, m := range verlauf {
		text := strings.Join(strings.Fields(m.Text), " ")
		if r := []rune(text); len(r) > 800 {
			text = string(r[:800]) + " […]"
		}
		fmt.Fprintf(&b, "- %s: %s\n", m.Wer, text)
	}
	if b.Len() == 0 {
		return ""
	}
	return "\n\n---\nEarlier in this conversation, oldest first (for context — the task is the message above; \"you\" is you):\n" + b.String()
}

/*
entscheidungFuer is the one exception to "the agent decides" (#302): the

	People department does not answer briefs, she works them (#327).

	The triage is a cheap turn in the control plane — no target systems, no
	org chart, no configs of the colleagues — and a description of a job read
	by it looks like a question about the job: "somebody who handles the
	tickets on Zendesk" came back as "I cannot see which tickets were handled".
	Her playbook exists precisely so that this sentence becomes a draft after
	looking at what is connected here; an answer from a turn that cannot look
	is the guess the brief is there to avoid. A note onto a brief that is
	already running stays a note — that is how her question gets its reply.
*/
func entscheidungFuer(slug string, e chat.Entscheidung) chat.Entscheidung {
	if slug == peopleSlug && e.Aktion == chat.AktionAntwort {
		return chat.Entscheidung{Aktion: chat.AktionAufgabe}
	}
	return e
}

// offeneAufgaben ist der Blick des Agenten auf seinen eigenen Backlog: was
// noch läuft, knapp genug, dass es in einen Prompt passt.
//
// Die kurze Kennung sind die ersten vier Zeichen der UUID. Sie ist keine
// Adresse, sondern ein Griff für diesen einen Zug — deshalb kommt die
// Zuordnung zurück und wird nicht aus dem Text wieder aufgelöst: Was das
// Modell sagt, wird nie zu einer Kennung, die irgendwo hinzeigt, die es sich
// nicht selbst hat zeigen lassen.
func (s *Server) offeneAufgaben(ctx context.Context, agentID uuid.UUID) ([]chat.Offen, map[string]uuid.UUID) {
	tasks, err := s.Backlog.ListByAgent(ctx, agentID, false)
	if err != nil {
		return nil, nil
	}
	liste := make([]chat.Offen, 0, len(tasks))
	nach := map[string]uuid.UUID{}
	for _, t := range tasks {
		switch t.State {
		case backlog.StateOpen, backlog.StateInProgress, backlog.StateBlocked:
		default:
			continue
		}
		kurz := t.ID.String()[:4]
		if _, doppelt := nach[kurz]; doppelt {
			continue
		}
		nach[kurz] = t.ID
		liste = append(liste, chat.Offen{
			Kurz:   kurz,
			Titel:  t.Title,
			Status: t.State,
			Alter:  alter(t.CreatedAt),
		})
		if len(liste) >= triageAufgaben {
			break
		}
	}
	return liste, nach
}

/*
fertigeAufgaben ist der zweite Teil des eigenen Blicks: was zuletzt fertig

	wurde, und was dabei herauskam.

*
* Nur so lässt sich „ist das gestern rausgegangen?" beantworten, statt dafür
* eine Aufgabe zu eröffnen — der Satz, an dem spec/28 die ganze Triage
* aufhängt. Die Ergebnisse sind das Teuerste an diesem Prompt, deshalb sind es
* wenige und gekürzte.
*
* Das Archiv bleibt draußen: Was jemand weggelegt hat, ist aus dem Gespräch
* heraus — dieselbe Grenze wie im Verlauf.
*/
func (s *Server) fertigeAufgaben(ctx context.Context, agentID uuid.UUID) []chat.Fertig {
	rows, err := s.Pool.Query(ctx,
		`SELECT title, state, coalesce(result,''), coalesce(error,''), updated_at
		   FROM backlog_tasks
		  WHERE agent_id=$1 AND state IN ('done','failed') AND archived_at IS NULL
		  ORDER BY updated_at DESC LIMIT $2`, agentID, triageFertig)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []chat.Fertig
	for rows.Next() {
		var f chat.Fertig
		var ergebnis, fehler string
		var wann time.Time
		if err := rows.Scan(&f.Titel, &f.Ausgang, &ergebnis, &fehler, &wann); err != nil {
			return out
		}
		f.Alter = alter(wann)
		f.Ergebnis = ergebnis
		if f.Ausgang == backlog.StateFailed && fehler != "" {
			f.Ergebnis = fehler
		}
		out = append(out, f)
	}
	/* Umgedreht: Der Prompt liest sich von alt nach neu, wie das Gespräch
	   darüber. Die Abfrage musste andersherum sortieren, um die jüngsten zu
	   bekommen. */
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// triageFertig: wie viele abgeschlossene Vorgänge mitkommen. Fünf sind der
// letzte Arbeitstag eines Agenten; mehr wäre ein Bericht, und ein Bericht
// gehört in den Backlog, nicht in einen Prompt, der bei jeder Nachricht läuft.
const triageFertig = 5

// triageAufgaben: wie viele offene Vorgänge der Zug zu sehen bekommt. Ein
// Agent mit zweihundert offenen Aufgaben hat ein anderes Problem als die
// Frage, ob diese Nachricht dazugehört.
const triageAufgaben = 25

func alter(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%d min old", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h old", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days old", int(d.Hours()/24))
	}
}

// triageKontext: wie viele frühere Nachrichten der Zug zu sehen bekommt. Mehr
// kostet Tokens bei jedem „danke"; weniger, und eine Rückfrage steht ohne
// das, worauf sie sich bezieht.
const triageKontext = 10

// rolleVon holt die kurze Selbstbeschreibung des Agenten — Anzeigename und
// Stellenbezeichnung, mehr nicht. Die vollständige Konfiguration gehört in
// den Lauf und nicht in einen Zug, der nur einordnen soll.
func (s *Server) rolleVon(ctx context.Context, agentID uuid.UUID) string {
	a, err := s.Registry.Get(ctx, agentID)
	if err != nil {
		return ""
	}
	return beschreibung(a)
}

// organisationVon is the org chart as the agent's runs get it (#415), for the
// triage. Empty without an orchestrator (a server that does not dispatch).
func (s *Server) organisationVon(ctx context.Context, agentID uuid.UUID) string {
	if s.Orch == nil {
		return ""
	}
	a, err := s.Registry.Get(ctx, agentID)
	if err != nil {
		return ""
	}
	return s.Orch.OrgSectionsForChat(ctx, a)
}

// gegenueberVon describes the person a message came from (#412): name, job
// title, department and responsibilities, as the organisation keeps them —
// so the agent can tell the engineer the branch and the salesperson what it
// means for them. Empty when the person is not known in this organisation.
func (s *Server) gegenueberVon(ctx context.Context, orgID uuid.UUID, email string) string {
	if email == "" || s.Pool == nil {
		return ""
	}
	var name, titel, zustaendig, abteilung string
	err := s.Pool.QueryRow(ctx, `SELECT h.display_name, h.job_title, h.responsibilities, coalesce(d.name, '')
		  FROM humans h LEFT JOIN departments d ON d.id = h.department_id
		 WHERE h.org_id = $1 AND lower(h.email) = lower($2)`, orgID, email).
		Scan(&name, &titel, &zustaendig, &abteilung)
	if err != nil {
		return ""
	}
	teile := []string{name}
	if titel != "" {
		teile = append(teile, "job title: "+titel)
	}
	if abteilung != "" {
		teile = append(teile, "department: "+abteilung)
	}
	if zustaendig != "" {
		teile = append(teile, "responsible for: "+zustaendig)
	}
	return strings.Join(teile, " — ")
}

/* chatStimme puts the chat voice into a turn's frame (#471): the voice the
 * rule chooses for this conversation — the department of the person who
 * wrote, then the agent's chat slot, then the organisation's — its tone, and
 * the "how to speak with us" lines of the departments involved. What it
 * returns is the meta the answer carries, so that the thread says which voice
 * spoke and why. Without voices on this instance the frame stays as it was. */
func (s *Server) chatStimme(ctx context.Context, r *chat.Rahmen, orgID, agentID, convID uuid.UUID, email string) map[string]string {
	if s.Voices == nil {
		return nil
	}
	aud := s.Voices.ConversationAudience(ctx, orgID, convID, email)
	c := s.Voices.Resolve(ctx, orgID, agentID, voice.OccasionChat, aud)
	meta := map[string]string{"voice_reason": c.Reason()}
	if c.Found() {
		if v, err := s.Voices.Get(ctx, orgID, c.VoiceID); err == nil {
			r.Stimme = voice.ChatPrompt(v)
			meta["voice_id"], meta["voice"] = v.ID.String(), v.Name
		}
	}
	r.Ton = s.Voices.ChatToneFor(ctx, orgID, agentID, c).Prompt()
	r.Publikum = voice.AudiencePrompt(c.Notes)
	if len(c.Notes) > 0 {
		var names []string
		for _, n := range c.Notes {
			names = append(names, n.Department)
		}
		meta["audience"] = strings.Join(names, ", ")
	}
	return meta
}

// nameVon is the display name of the person behind an address, for the
// group text (#457). Empty when unknown.
func (s *Server) nameVon(ctx context.Context, orgID uuid.UUID, email string) string {
	if email == "" || s.Pool == nil {
		return ""
	}
	var name string
	_ = s.Pool.QueryRow(ctx, `SELECT display_name FROM humans WHERE org_id = $1 AND lower(email) = lower($2)`,
		orgID, email).Scan(&name)
	return name
}

// seeleVon is the agent's SOUL.md from its current config (#411): how it
// talks is written there, not in its title. Empty when it has none.
func (s *Server) seeleVon(ctx context.Context, agentID uuid.UUID) string {
	cv, err := s.Registry.CurrentConfig(ctx, agentID)
	if err != nil {
		return ""
	}
	return cv.Files["SOUL.md"]
}

func beschreibung(a agents.Agent) string {
	teile := []string{a.DisplayName}
	if a.JobTitle != "" {
		teile = append(teile, a.JobTitle)
	}
	if a.Responsibilities != "" {
		teile = append(teile, a.Responsibilities)
	}
	return strings.Join(teile, " — ")
}

// handleGetTriage says whether this organisation lets its agents decide.
func (s *Server) handleGetTriage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	mode, err := s.Chat.Mode(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	/* `available` sagt, ob es überhaupt ginge: Ohne Zugangsdaten in der
	   Control Plane bleibt der Schalter ein Schalter ohne Wirkung, und die
	   Oberfläche soll das sagen dürfen, statt ihn anzubieten. */
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":      string(mode),
		"available": llm.Available(r.Context(), s.Secrets, s.Runtimes, p.OrgID),
	})
}

func (s *Server) handleSetTriage(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	var in struct {
		Mode string `json:"mode"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid body: expected {\"mode\": \"on|off\"}")
		return
	}
	if err := s.Chat.SetMode(r.Context(), p.OrgID, chat.TriageMode(in.Mode)); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": in.Mode})
}

// handleGetTeamSurface says whether this organisation has the team surface on.
func (s *Server) handleGetTeamSurface(w http.ResponseWriter, r *http.Request) {
	on, err := s.Chat.TeamSurface(r.Context(), principalFrom(r).OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": on})
}

func (s *Server) handleSetTeamSurface(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := readJSON(r, &in); err != nil || in.Enabled == nil {
		writeErr(w, http.StatusBadRequest, "invalid body: expected {\"enabled\": true|false}")
		return
	}
	if err := s.Chat.SetTeamSurface(r.Context(), principalFrom(r).OrgID, *in.Enabled); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"enabled": *in.Enabled})
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
/* gestoppt refuses a message or a reply to a stopped agent (#414) and says
 * so. It would be written down and never run: the dispatcher takes no task
 * of a stopped agent, nor of any agent while the organisation's kill switch
 * is on. The surfaces hide the composer; this is the guard that holds for
 * every other client too. */
func (s *Server) gestoppt(w http.ResponseWriter, r *http.Request, agentID uuid.UUID) bool {
	var stop bool
	if err := s.Pool.QueryRow(r.Context(), `SELECT a.killed OR o.fleet_killed
		  FROM agents a JOIN organizations o ON o.id = a.org_id WHERE a.id = $1`, agentID).Scan(&stop); err != nil {
		mapErr(w, err)
		return true
	}
	if stop {
		writeErr(w, http.StatusConflict, "the agent is stopped (kill switch) and takes no messages until it is released")
	}
	return stop
}

/* darfAntworten: who answers a task (#440). The roles that may create one by
 * hand, as before — and the responsible person whatever their role: a member
 * of the conversation the task reports to (its question stands there), or
 * the agent's supervisor, to whom a question from elsewhere goes. */
func (s *Server) darfAntworten(ctx context.Context, p identity.Principal, t backlog.Task) bool {
	if darfArbeitGeben(p.Role) {
		return true
	}
	if t.ConversationID != nil {
		if c, err := s.Chat.Get(ctx, *t.ConversationID); err == nil && c.Has(chat.Human(p.ID)) {
			return true
		}
	}
	var sup bool
	_ = s.Pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM agents WHERE id = $1 AND supervisor_id = $2)`, t.AgentID, p.ID).Scan(&sup)
	return sup
}

func (s *Server) handleTaskReply(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	t, err := s.Backlog.Get(r.Context(), id)
	if err != nil {
		mapErr(w, err)
		return
	}
	if !s.darfAntworten(r.Context(), p, t) {
		writeErr(w, http.StatusForbidden, "only who may create a task by hand, a member of the conversation the task reports to, or the agent's supervisor answers it")
		return
	}
	if s.gestoppt(w, r, t.AgentID) {
		return
	}
	text, ok := chatText(w, r)
	if !ok {
		return
	}

	note, err := s.Backlog.AddNote(r.Context(), id, "human:"+p.Email, text)
	if err != nil {
		mapErr(w, err)
		return
	}

	out := chatReply{Note: note}
	geweckt, err := s.Backlog.Answer(r.Context(), id, p.Email, text)
	switch {
	case err == nil:
		out.Task, out.Woken = &geweckt, true
	case errors.Is(err, backlog.ErrInvalidTransition):
		// nobody was waiting — the note stands
	default:
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMyThreads answers, per agent, how much of the person's direct
// conversation with it they have not read, and its newest line (#378) — the
// alias the per-agent list keeps; GET /conversations is the whole list.
func (s *Server) handleMyThreads(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := s.Chat.List(r.Context(), p.ID, 0)
	if err != nil {
		mapErr(w, err)
		return
	}
	out := []threadState{}
	for _, c := range list {
		if c.Kind != chat.KindDirect || c.Last == nil {
			continue
		}
		for _, m := range c.Active() {
			if m.Kind != chat.MemberAgent {
				continue
			}
			kind := "answer"
			switch {
			case c.Last.AuthorKind == chat.MemberHuman:
				kind = "message"
			case c.Last.Kind != chat.MessageText:
				kind = c.Last.Kind
			}
			out = append(out, threadState{AgentID: m.ID, Unread: c.Unread, LastAt: c.Last.CreatedAt,
				LastText: push.FirstLine(c.Last.Text, 140), LastKind: kind})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": out})
}

// threadState is one agent's thread as the per-agent list shows it.
type threadState struct {
	AgentID  uuid.UUID `json:"agent_id"`
	Unread   int       `json:"unread"`
	LastAt   time.Time `json:"last_at"`
	LastText string    `json:"last_text"`
	LastKind string    `json:"last_kind"`
}

// handleThreadRead moves the person's point in their direct conversation
// with the agent forward to the newest entry the reader has shown.
func (s *Server) handleThreadRead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		At time.Time `json:"at"`
	}
	if err := readJSON(r, &in); err != nil || in.At.IsZero() {
		writeErr(w, http.StatusBadRequest, "expected {\"at\": <RFC 3339 time of the newest entry shown>}")
		return
	}
	p := principalFrom(r)
	convID, gibt, err := s.Chat.FindDirect(r.Context(), p.OrgID, chat.Human(p.ID), chat.Agent(agentFrom(r).ID))
	if err != nil {
		mapErr(w, err)
		return
	}
	if gibt {
		if err := s.Chat.MarkRead(r.Context(), convID, p.ID, in.At); err != nil {
			mapErr(w, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}
