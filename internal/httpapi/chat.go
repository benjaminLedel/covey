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
	"covey/internal/identity"
	"covey/internal/llm"
	"covey/internal/orchestrator"
	"covey/internal/push"
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
	/* The drafts a hiring task produced (#327). Only on a result of the
	   People department, read off the recording where the platform wrote them
	   (hiring.go, rule 3) — never off what the agent reported. The way to the
	   draft is the agent page, where hiring already is; there is no hire
	   action, and the thread does not pretend otherwise. */
	Drafts []entwurfKurz `json:"drafts,omitempty"`
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
}

// threadTasks is how far back a thread reaches. Whoever wants more than the
// last twenty exchanges with one agent is looking for a task, and the backlog
// has the search for that.
const threadTasks = 20

// threadMessages: wie viele Nachrichten dazukommen. Doppelt so viele wie
// Aufgaben, weil eine beantwortete Nachricht keine Aufgabe erzeugt — ein
// Verlauf kann also aus deutlich mehr Zeilen als Vorgängen bestehen.
const threadMessages = 40

/* sucheTasks und sucheMessages: das Fenster, wenn gesucht wird.
 *
 * Zehnmal so weit wie das des Verlaufs. Weiter nicht: Wer über hundert
 * Vorgänge hinaus sucht, sucht im Backlog, und der Verweis daneben führt
 * dorthin — eine Volltextsuche über die ganze Geschichte eines Agenten ist
 * eine andere Sache als das Durchsehen eines Gesprächs. */
const (
	sucheTasks    = 200
	sucheMessages = 400
)

/*
suchbegriff macht aus der Eingabe ein Muster, das nur das findet, wonach

	gefragt wurde. Ein Prozentzeichen ist in ILIKE „alles", und wer nach „50 %"
	sucht, bekäme sonst den halben Verlauf.
*/
func suchbegriff(roh string) string {
	q := strings.TrimSpace(roh)
	if q == "" {
		return ""
	}
	r := strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)
	return r.Replace(q)
}

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
	/* ?q= sucht im Gespräch statt es zu zeigen.

	   Gefiltert wird AUSSEN, über dem fertigen Verlauf: Ein Treffer ist eine
	   Zeile, die jemand gesagt oder der Lauf hinterlassen hat, und welche der
	   sechs Quellen sie hergab, ist dem Suchenden gleich. Drinnen zu filtern
	   hieße, dieselbe Bedingung sechsmal zu schreiben und beim siebten Zweig
	   zu vergessen.

	   Dafür reicht das Fenster weiter zurück: Wer sucht, sucht das, was er
	   nicht mehr sieht. */
	/* What the platform starts on its own — heartbeat runs, housekeeping,
	   their continuations — is not part of the conversation (#401, #410):
	   nobody said anything, and at `alle: 15m` those blocks pushed what a
	   person had written out of the window. chat.MachineryCTE holds the rule
	   the unread count uses too.

	   Only their questions stay — a question is addressed to the person and
	   answered here. So such a task is left out of the window unless it once
	   parked with a question, and inside the window only its question line
	   is drawn. */
	q := `WITH RECURSIVE ` + chat.MachineryCTE("agent_id=$1") + `, t AS (
		SELECT bt.id, bt.title, bt.body, bt.state, bt.origin, bt.result, bt.error, bt.created_at, bt.updated_at,
		       EXISTS (SELECT 1 FROM maschinerie mm WHERE mm.id = bt.id) AS takt
		FROM backlog_tasks bt
		WHERE bt.agent_id=$1 AND bt.archived_at IS NULL
		  AND (NOT EXISTS (SELECT 1 FROM maschinerie mm WHERE mm.id = bt.id)
		       OR EXISTS (SELECT 1 FROM task_transitions tr WHERE tr.task_id = bt.id AND tr.to_state = 'blocked'))
		ORDER BY bt.created_at DESC LIMIT $2
	), m AS (
		SELECT id, author, text, task_id, created_at
		FROM chat_messages WHERE agent_id=$1
		ORDER BY created_at DESC LIMIT $3
	), alles AS (
	SELECT CASE WHEN m.author = 'agent' THEN 'answer' ELSE 'message' END AS kind,
	       m.id AS eid, m.task_id AS tid, coalesce(bt.title, '') AS titel, coalesce(bt.state, '') AS zustand,
	       m.author AS wer, m.text AS text, m.created_at AS wann
	       FROM m LEFT JOIN backlog_tasks bt ON bt.id = m.task_id
	UNION ALL
	SELECT 'message', t.id, t.id, t.title, t.state, t.origin,
	       -- Der Rumpf, nicht Titel plus Rumpf: Die erste Zeile IST der Titel.
	       CASE WHEN coalesce(t.body,'')='' THEN t.title ELSE t.body END,
	       t.created_at FROM t
	       WHERE NOT t.takt AND NOT EXISTS (SELECT 1 FROM m WHERE m.task_id = t.id)
	UNION ALL
	SELECT 'note', n.task_id, n.task_id, t.title, t.state, n.author, n.content, n.created_at
	       FROM task_notes n JOIN t ON t.id = n.task_id
	       -- Die Notiz der Triage ist Maschinerie, nicht Gespräch: Sie trägt
	       -- die Nachricht an den laufenden Vorgang weiter, und die Nachricht
	       -- selbst steht zwei Zeilen darüber.
	       WHERE NOT t.takt AND n.author NOT LIKE 'triage:%'
	UNION ALL
	SELECT 'question', tr.task_id, tr.task_id, t.title, t.state, 'agent', tr.note, tr.created_at
	       FROM task_transitions tr JOIN t ON t.id = tr.task_id
	       WHERE tr.to_state='blocked'
	UNION ALL
	SELECT 'result', t.id, t.id, t.title, t.state, 'agent', t.result, t.updated_at
	       FROM t WHERE NOT t.takt AND t.state='done' AND coalesce(t.result,'') <> ''
	UNION ALL
	SELECT 'error', t.id, t.id, t.title, t.state, 'agent', t.error, t.updated_at
	       FROM t WHERE NOT t.takt AND t.state='failed' AND coalesce(t.error,'') <> ''
	)
	SELECT kind, eid, tid, titel, zustand, wer, text, wann FROM alles
	 WHERE $4 = '' OR text ILIKE '%' || $4 || '%' ESCAPE '\'
	                OR titel ILIKE '%' || $4 || '%' ESCAPE '\'
	 ORDER BY wann`

	begriff := suchbegriff(r.URL.Query().Get("q"))
	aufgaben, nachrichten := threadTasks, threadMessages
	if begriff != "" {
		aufgaben, nachrichten = sucheTasks, sucheMessages
	}
	rows, err := s.Pool.Query(r.Context(), q, id, aufgaben, nachrichten, begriff)
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

	/* Bei einer Suche bleibt es bei den Treffern. Reaktionen, der Denkzustand
	   und die Hintergrundvorgänge gehören zum Gespräch, nicht zu einer Liste
	   von Fundstellen — und drei Abfragen, die niemand ansieht, sind drei
	   Abfragen zu viel auf einem Weg, der bei jedem Tastendruck läuft. */
	if begriff != "" {
		out.Tasks = []offenerVorgang{}
		out.Marks = map[string][]chatMark{}
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
	/* Eine Zeile über einem Teilindex, der im Normalbetrieb leer ist. */
	if err := s.Pool.QueryRow(r.Context(),
		`SELECT EXISTS(SELECT 1 FROM chat_messages WHERE agent_id=$1 AND triage_state='pending')`,
		id).Scan(&out.Pending); err != nil {
		mapErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
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

// handleChatMessage takes a message and lets the agent decide what it is.
//
// The message is written down first, always — before any model has seen it
// and whatever the triage decides afterwards. What somebody said is a fact;
// what is made of it is a judgement, and a judgement that fails must not
// swallow the fact.
/* handleChatMessage nimmt eine Nachricht an — und wartet nicht auf die
  Entscheidung.
*
* Vorher lief der Modell-Zug im Request. Das war die bequeme Form und die
* falsche: Wer abschickte, sah Sekunden lang ein stehendes Eingabefeld und
* nicht einmal die eigene Nachricht, und die Verbindung musste so lange
* offen bleiben, wie das Modell brauchte. Ein Chat, in dem das Absenden
* hängt, ist kein Chat, sondern ein Formular.
*
* Jetzt: Die Nachricht wird geschrieben, der Request ist fertig (202), und
* die Entscheidung fällt daneben. Was dabei herauskommt, kommt über den
* Ereignisstrom zurück, über den die Oberfläche ohnehin schon hört
* (`/api/v1/events`, sse.go) — dieselbe Leitung, die Aufgaben und Läufe
* meldet, meldet nun auch, dass der Kollege nachdenkt.
*
* Ohne Triage bleibt alles, wie es war: Da ist nichts zu warten — eine
* Aufgabe anzulegen kostet eine Einfügung —, und der Aufrufer bekommt sie
* wie bisher in derselben Antwort. */
func (s *Server) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid id")
		return
	}
	p := principalFrom(r)
	// The team surface is where a message comes from; an organisation that
	// has not turned it on gets no messages by the side door either (#328).
	// Checked here and not only in the interface, since the interface is not
	// the only client of this endpoint.
	if on, err := s.Chat.TeamSurface(r.Context(), p.OrgID); err != nil {
		mapErr(w, err)
		return
	} else if !on {
		writeErr(w, http.StatusForbidden, "the team surface is not enabled for this organisation")
		return
	}
	text, ok := chatText(w, r)
	if !ok {
		return
	}

	/* Ob es überhaupt etwas zu entscheiden gibt, steht an der Organisation.
	   Die Frage kostet eine Zeile und entscheidet, ob diese Nachricht mit
	   einer Schuld geschrieben wird. */
	mode, err := s.Chat.Mode(r.Context(), p.OrgID)
	if err != nil {
		mapErr(w, err)
		return
	}
	triage := mode == chat.TriageOn

	msg, err := s.Chat.Add(r.Context(), p.OrgID, id, "chat:"+p.Email, text, triage)
	if err != nil {
		mapErr(w, err)
		return
	}

	if !triage {
		t, err := s.aufgabeAusNachricht(r.Context(), p.OrgID, id, msg, chat.Entscheidung{}, text, p.Email, langFrom(r))
		if err != nil {
			mapErr(w, err)
			return
		}
		msg.TaskID = &t.ID
		writeJSON(w, http.StatusCreated, map[string]any{"message": msg, "task": t, "answered": false})
		return
	}

	/* Angenommen. Der Zug läuft daneben, mit einem Kontext, der nicht am
	   Request hängt: Dessen Kontext wird abgebrochen, sobald die Antwort
	   geschrieben ist, und ein Zug, den das Abschicken der Antwort abbricht,
	   liefe nie zu Ende. */
	go s.triageLauf(p.OrgID, id, msg, text, p.Email, langFrom(r))

	s.chatEreignis(p.OrgID, id, "thinking", nil)
	writeJSON(w, http.StatusAccepted, map[string]any{"message": msg, "pending": true})
}

// triageLaufFrist: so lange darf ein Zug dauern. Ein Modell, das länger
// braucht, hat nicht geantwortet — und die Nachricht wird zur Aufgabe, was
// ohnehin die Antwort auf jeden Fehler ist.
const triageLaufFrist = 90 * time.Second

/* triageLauf ist der Zug außerhalb des Requests.
 *
 * Er endet IMMER mit einem Zustand an der Nachricht und einem Ereignis —
 * auch beim Absturz eines Modells, auch bei einem Fehler in der Datenbank.
 * Was er nicht darf, ist still enden: Eine angenommene Nachricht, zu der nie
 * etwas kommt, ist schlimmer als eine überflüssige Aufgabe.
 */
func (s *Server) triageLauf(orgID, agentID uuid.UUID, msg chat.Message, text, email, lang string) {
	ctx, abbrechen := context.WithTimeout(context.Background(), triageLaufFrist)
	defer abbrechen()

	entscheidung, offen := s.triagieren(ctx, orgID, agentID, text)
	if a, err := s.Registry.Get(ctx, agentID); err == nil {
		entscheidung = entscheidungFuer(a.Slug, entscheidung)
	}
	zustand, daten, err := s.entscheidungAnwenden(ctx, orgID, agentID, msg, entscheidung, offen, text, email, lang)
	if err != nil {
		s.Log.Error("chat triage could not be applied", "agent", agentID, "message", msg.ID, "err", err)
		if err := s.Chat.Erledigt(ctx, msg.ID, chat.StateFailed); err != nil {
			s.Log.Warn("chat message stays pending", "message", msg.ID, "err", err)
		}
		s.chatEreignis(orgID, agentID, "failed", nil)
		return
	}
	if err := s.Chat.Erledigt(ctx, msg.ID, chat.StateDone); err != nil {
		s.Log.Warn("chat message stays pending", "message", msg.ID, "err", err)
	}
	s.chatEreignis(orgID, agentID, zustand, daten)
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

/*
entscheidungAnwenden führt aus, was die Triage beschlossen hat, und gibt

	zurück, was daraus geworden ist. Ein Fehler hier ist ein echter Fehler —
	die weiche Behandlung („im Zweifel eine Aufgabe") sitzt eine Ebene tiefer
	in triagieren(), wo sie hingehört.
*/
func (s *Server) entscheidungAnwenden(
	ctx context.Context,
	orgID, agentID uuid.UUID,
	msg chat.Message,
	entscheidung chat.Entscheidung,
	offen map[string]uuid.UUID,
	text, email, lang string,
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
			   Nachricht im Verlauf, und beides nebeneinander läse sich wie
			   dieselbe Bitte zweimal, einmal davon in fremden Worten. Am
			   Vorgang bleibt die Notiz, wo sie hingehört; aus dem Gespräch
			   hält sie sich heraus (handleThread). */
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
			return "noted", map[string]string{"task_id": ziel.String(), "woken": geweckt}, nil
		}
	}

	if entscheidung.Aktion == chat.AktionAntwort {
		if _, err := s.Chat.Add(ctx, orgID, agentID, "agent", entscheidung.Text, false); err != nil {
			return "", nil, err
		}
		return "answered", nil, nil
	}

	t, err := s.aufgabeAusNachricht(ctx, orgID, agentID, msg, entscheidung, text, email, lang)
	if err != nil {
		return "", nil, err
	}
	return "task", map[string]string{"task_id": t.ID.String()}, nil
}

/*
aufgabeAusNachricht legt den Vorgang an und hängt die Nachricht daran.

	`origin` sagt, woher die Arbeit kam, und der Chat ist eine Herkunft neben
	manual, schedule und webhook:… — keine neue Art Aufgabe.
*/
func (s *Server) aufgabeAusNachricht(
	ctx context.Context,
	orgID, agentID uuid.UUID,
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
		rumpf = s.briefBody(ctx, identity.Principal{OrgID: orgID, Email: email}, lang, text, "", "", "")
	}
	t, err := s.Backlog.Create(ctx, orgID, agentID, titel, rumpf, "chat:"+email, 0)
	if err != nil {
		return backlog.Task{}, err
	}
	if err := s.Chat.LinkTask(ctx, msg.ID, t.ID); err != nil {
		return backlog.Task{}, err
	}
	return t, nil
}

/* chatEreignis meldet, was mit dem Verlauf geschehen ist.
 *
 * Es ist dieselbe Leitung, über die Aufgaben, Läufe und Freigaben kommen, und
 * sie ist bereits je Organisation abgegrenzt (broadcast.go). Die Oberfläche
 * braucht daraufhin nur den Verlauf neu zu holen — was genau geschehen ist,
 * steht in ihm, nicht im Ereignis. `state` ist trotzdem dabei, weil eine
 * Sache NICHT im Verlauf steht: dass gerade nachgedacht wird. */
func (s *Server) chatEreignis(orgID, agentID uuid.UUID, zustand string, daten map[string]string) {
	if s.Orch == nil {
		return
	}
	d := map[string]string{"state": zustand}
	for k, v := range daten {
		d[k] = v
	}
	s.Orch.Events().Publish(orchestrator.Event{
		Type:    "chat",
		AgentID: agentID.String(),
		OrgID:   orgID,
		Data:    d,
	})
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
		s.Log.Info("finishing chat triage left over from a restart", "message", m.ID, "agent", m.AgentID)
		email := strings.TrimPrefix(m.Author, "chat:")
		/* The language of the request is gone with the restart; the brief
		   frame then reads in English, which every playbook reads. */
		go s.triageLauf(m.OrgID, m.AgentID, m, m.Text, email, "")
	}
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
	fertig := s.fertigeAufgaben(ctx, agentID)

	e, err := chat.Triagieren(ctx, provider, s.rolleVon(ctx, agentID), liste, fertig, verlauf, text)
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

// handleMyThreads answers, per agent, how much of its thread the signed-in
// person has not read, and the newest entry from the agent's side (#378).
func (s *Server) handleMyThreads(w http.ResponseWriter, r *http.Request) {
	p := principalFrom(r)
	list, err := chat.New(s.Pool).Threads(r.Context(), p.OrgID, p.ID)
	if err != nil {
		mapErr(w, err)
		return
	}
	for i := range list {
		list[i].LastText = push.FirstLine(list[i].LastText, 140)
	}
	writeJSON(w, http.StatusOK, map[string]any{"threads": list})
}

// handleThreadRead moves the person's point in the agent's thread forward to
// the newest entry the reader has shown.
func (s *Server) handleThreadRead(w http.ResponseWriter, r *http.Request) {
	var in struct {
		At time.Time `json:"at"`
	}
	if err := readJSON(r, &in); err != nil || in.At.IsZero() {
		writeErr(w, http.StatusBadRequest, "expected {\"at\": <RFC 3339 time of the newest entry shown>}")
		return
	}
	if err := chat.New(s.Pool).MarkRead(r.Context(), principalFrom(r).ID, agentFrom(r).ID, in.At); err != nil {
		mapErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
