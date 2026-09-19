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
	"covey/internal/llm"
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
	/* Reaktionen je Aufgabe. Sie hängen an der Aufgabe und nicht am Eintrag
	   (internal/backlog/reactions.go) — die Oberfläche zeigt sie deshalb am
	   ersten Eintrag eines Vorgangs. */
	Marks map[string][]chatMark `json:"marks"`
}

// threadTasks is how far back a thread reaches. Whoever wants more than the
// last twenty exchanges with one agent is looking for a task, and the backlog
// has the search for that.
const threadTasks = 20

// threadMessages: wie viele Nachrichten dazukommen. Doppelt so viele wie
// Aufgaben, weil eine beantwortete Nachricht keine Aufgabe erzeugt — ein
// Verlauf kann also aus deutlich mehr Zeilen als Vorgängen bestehen.
const threadMessages = 40

// handleThread assembles the conversation with one agent.
func (s *Server) handleThread(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}

	/* Der Verlauf hat zwei Quellen, seit eine Nachricht nicht mehr
	   zwangsläufig eine Aufgabe ist (#302):

	     die NACHRICHTEN  — was gesagt wurde, samt der Antworten des Agenten
	     die AUFGABEN     — was daraus wurde, mit Notizen, Fragen, Ergebnis

	   Eine Aufgabe, zu der eine Nachricht gehört, steuert ihre Zeile nicht
	   selbst bei; sonst stünde dieselbe Bitte zweimal da — einmal als das,
	   was jemand schrieb, und einmal als das, was daraus wurde.

	   Das Archiv bleibt draußen: Was jemand weggelegt hat, ist nicht mehr
	   Teil des Gesprächs. */
	const q = `WITH t AS (
		SELECT id, title, body, state, origin, result, error, created_at, updated_at
		FROM backlog_tasks
		WHERE agent_id=$1 AND archived_at IS NULL
		ORDER BY created_at DESC LIMIT $2
	), m AS (
		SELECT id, author, text, task_id, created_at
		FROM chat_messages WHERE agent_id=$1
		ORDER BY created_at DESC LIMIT $3
	)
	SELECT CASE WHEN m.author = 'agent' THEN 'answer' ELSE 'message' END,
	       m.id, m.task_id, coalesce(bt.title, ''), coalesce(bt.state, ''),
	       m.author, m.text, m.created_at
	       FROM m LEFT JOIN backlog_tasks bt ON bt.id = m.task_id
	UNION ALL
	SELECT 'message', t.id, t.id, t.title, t.state, t.origin,
	       -- Der Rumpf, nicht Titel plus Rumpf: Die erste Zeile IST der Titel.
	       CASE WHEN coalesce(t.body,'')='' THEN t.title ELSE t.body END,
	       t.created_at FROM t
	       WHERE NOT EXISTS (SELECT 1 FROM m WHERE m.task_id = t.id)
	UNION ALL
	SELECT 'note', n.task_id, n.task_id, t.title, t.state, n.author, n.content, n.created_at
	       FROM task_notes n JOIN t ON t.id = n.task_id
	UNION ALL
	SELECT 'question', tr.task_id, tr.task_id, t.title, t.state, 'agent', tr.note, tr.created_at
	       FROM task_transitions tr JOIN t ON t.id = tr.task_id
	       WHERE tr.to_state='blocked'
	UNION ALL
	SELECT 'result', t.id, t.id, t.title, t.state, 'agent', t.result, t.updated_at
	       FROM t WHERE t.state='done' AND coalesce(t.result,'') <> ''
	UNION ALL
	SELECT 'error', t.id, t.id, t.title, t.state, 'agent', t.error, t.updated_at
	       FROM t WHERE t.state='failed' AND coalesce(t.error,'') <> ''
	ORDER BY 8`

	rows, err := s.Pool.Query(r.Context(), q, id, threadTasks, threadMessages)
	if err != nil {
		mapErr(w, err)
		return
	}
	defer rows.Close()

	out := chatThread{Entries: []chatEntry{}}
	for rows.Next() {
		var e chatEntry
		if err := rows.Scan(&e.Kind, &e.ID, &e.TaskID, &e.TaskTitle, &e.TaskState,
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

	out.Marks, err = s.marksOf(r, out.Entries)
	if err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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

// handleChatMessage takes a message and lets the agent decide what it is.
//
// The message is written down first, always — before any model has seen it
// and whatever the triage decides afterwards. What somebody said is a fact;
// what is made of it is a judgement, and a judgement that fails must not
// swallow the fact.
func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	text, ok := chatText(w, r)
	if !ok {
		return
	}

	msg, err := s.Chat.Add(r.Context(), p.OrgID, id, "chat:"+p.Email, text)
	if err != nil {
		mapErr(w, err)
		return
	}

	entscheidung, offen := s.triagieren(r.Context(), p.OrgID, id, text)

	/* Die Notiz an eine laufende Aufgabe: Statt eines zweiten Vorgangs für
	   dieselbe Sache bekommt der bestehende, was dazugekommen ist — und wenn
	   er auf eine Antwort wartete, weckt ihn das über denselben Weg wie eine
	   Antwort von Hand. */
	if entscheidung.Aktion == chat.AktionNotiz {
		if ziel, ok := offen[entscheidung.Aufgabe]; ok {
			if _, err := s.Backlog.AddNote(r.Context(), ziel, "human:"+p.Email, entscheidung.Text); err != nil {
				mapErr(w, err)
				return
			}
			geweckt := false
			if _, err := s.Backlog.Answer(r.Context(), ziel, p.Email, entscheidung.Text); err == nil {
				geweckt = true
			}
			if err := s.Chat.LinkTask(r.Context(), msg.ID, ziel); err != nil {
				mapErr(w, err)
				return
			}
			msg.TaskID = &ziel
			writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "noted": true, "woken": geweckt})
			return
		}
		/* Eine Kennung, die es nicht gibt: Das Modell hat sich eine ausgedacht,
		   und dann ist es eine neue Aufgabe — nie eine stillschweigend
		   verworfene Nachricht. */
		entscheidung = chat.Entscheidung{Aktion: chat.AktionAufgabe}
	}

	if entscheidung.Aktion == chat.AktionAntwort {
		if _, err := s.Chat.Add(r.Context(), p.OrgID, id, "agent", entscheidung.Text); err != nil {
			mapErr(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "answered": true})
		return
	}

	/* origin sagt, woher die Arbeit kam, und der Chat ist eine Herkunft neben
	   manual, schedule und webhook:… — keine neue Art Aufgabe. */
	titel, rumpf := entscheidung.Titel, entscheidung.Rumpf
	if titel == "" {
		titel, rumpf = chatTitle(text), text
	}
	t, err := s.Backlog.Create(r.Context(), p.OrgID, id, titel, rumpf, "chat:"+p.Email, 0)
	if err != nil {
		mapErr(w, err)
		return
	}
	if err := s.Chat.LinkTask(r.Context(), msg.ID, t.ID); err != nil {
		mapErr(w, err)
		return
	}
	/* Die zurückgegebene Nachricht trägt die Verknüpfung mit: Sie wurde
	   geschrieben, bevor die Aufgabe existierte, und eine Antwort, die den
	   Stand von vor zwei Zeilen zeigt, ist eine Antwort, der man nicht
	   glauben kann. */
	msg.TaskID = &t.ID
	writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "task": t, "answered": false})
}

// triagieren runs the turn — or says "task" without asking anybody.
//
// Every failure lands on the same answer: open the task. An organisation
// without the switch, without a credential, without a reachable provider, or
// with a model that answered nonsense gets the behaviour it had before #302,
// and nobody has to be told about it. The one thing that must never happen is
// that a message disappears because a model was not available.
func (s *Server) triagieren(ctx context.Context, orgID, agentID uuid.UUID, text string) (chat.Entscheidung, map[string]uuid.UUID) {
	aufgabe := chat.Entscheidung{Aktion: chat.AktionAufgabe}

	mode, err := s.Chat.Mode(ctx, orgID)
	if err != nil || mode != chat.TriageOn {
		return aufgabe, nil
	}
	provider, err := llm.Resolve(ctx, s.Secrets, orgID)
	if err != nil {
		return aufgabe, nil
	}
	verlauf, err := s.Chat.Recent(ctx, agentID, triageKontext)
	if err != nil {
		return aufgabe, nil
	}
	liste, nach := s.offeneAufgaben(ctx, agentID)

	e, err := chat.Triagieren(ctx, provider, s.rolleVon(ctx, agentID), liste, verlauf, text)
	if err != nil {
		s.Log.Warn("triage failed — the message becomes a task", "agent", agentID, "err", err)
		return aufgabe, nil
	}
	return e, nach
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
const triageKontext = 12

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
		"available": llm.Available(r.Context(), s.Secrets, p.OrgID),
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
