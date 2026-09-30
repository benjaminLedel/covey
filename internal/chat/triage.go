package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"covey/internal/llm"
)

/* Die Triage: was für ein Ding ist diese Nachricht?
 *
 * Ein Zug, in der Control Plane, ohne Sandbox — dieselbe Form wie der
 * Config-Copilot (internal/httpapi/assist.go), und aus demselben Grund: Ein
 * Satz zu beantworten darf keinen Arbeitsplatz hochfahren.
 *
 * Was dieser Zug NICHT hat, ist die eigentliche Aussage:
 *
 *   keine Zielsysteme, keine Zugangsdaten, keine Sandbox.
 *
 * Kein Ticketsystem, kein Repository, kein Postfach, keine Datei, kein
 * Kommando. Alles, wofür er das bräuchte, ist genau der Grund, stattdessen
 * eine Aufgabe zu eröffnen — und das ist keine Einschränkung, die man später
 * lockert, sondern die Grenze, die diesen Weg überhaupt zulässig macht. Ein
 * billiger Pfad darf nicht der billige Weg an den Guard-Rails vorbei werden
 * (spec/28, Abschnitt 5).
 *
 * WAS er hat, ist der eigene Backlog: Er sieht die offenen Aufgaben dieses
 * Agenten und kann in sie schreiben. Das ist kein Widerspruch zur Grenze,
 * sondern ihre andere Seite — der Backlog ist coveys eigenes Objekt, kein
 * fremdes System. Ihn zu lesen braucht keine Zugangsdaten und verlässt das
 * Haus nicht, und ohne ihn kann der Agent auf „woran arbeitest du gerade?"
 * nur raten. Darum drei Möglichkeiten statt zwei: antworten, an eine
 * bestehende Aufgabe schreiben, oder eine neue eröffnen.
 */

// Aktion ist, was mit einer Nachricht geschehen soll.
type Aktion string

const (
	// AktionAntwort: der Agent antwortet im Verlauf. Keine Aufgabe.
	AktionAntwort Aktion = "answer"
	// AktionAufgabe: das ist Arbeit — der gewöhnliche Backlog-Vorgang.
	AktionAufgabe Aktion = "task"
	// AktionNotiz: das gehört zu etwas, das schon läuft. Statt einer zweiten
	// Aufgabe für denselben Vorgang bekommt die bestehende eine Notiz — und
	// wenn sie auf eine Antwort wartet, weckt sie das.
	AktionNotiz Aktion = "note"
	// AktionSuche: erst nachsehen (#416). Der Zug sieht nur das Ende des
	// Gesprächs; was älter ist oder wen er nicht vor sich hat, lässt er
	// suchen — im ganzen Gespräch und im Organigramm — und entscheidet dann
	// mit den Treffern. Einmal, nicht in einer Schleife.
	AktionSuche Aktion = "search"
	// AktionKonfig: the message asks the agent to change its own
	// configuration (#491) — how often it looks at its queue, a procedure,
	// how it talks, what it may reach. Only offered while the organisation
	// has the trial on (Rahmen.Vorschlaege). The turn says what to change in
	// plain words; the server drafts it with the config assistant and
	// stores a proposal a person accepts — nothing changes before that.
	AktionKonfig Aktion = "config"
)

// ApproversPlatzhalter is what a config decision's text writes where the
// names of those who may accept go; the server puts them in (#491). The turn
// cannot know them: who may accept depends on the files the draft touches.
const ApproversPlatzhalter = "{approvers}"

// Suche ist, was ein Zug gesucht und gefunden hat — der zweite Zug bekommt
// es und entscheidet damit.
type Suche struct {
	Anfrage string
	Treffer []string
}

// Entscheidung ist, was die Triage zurückgibt.
type Entscheidung struct {
	Aktion Aktion `json:"action"`
	// Bei einer Antwort: der Text, den der Agent sagt. Ein bis drei Zeichen
	// dürfen es auch sein — dann ist es eine Reaktion und die Oberfläche
	// zeigt sie als solche.
	Text string `json:"text"`
	// Bei einer Aufgabe: Titel und Rumpf, wie der Agent sie formuliert.
	Titel string `json:"title"`
	Rumpf string `json:"body"`
	// Bei einer Notiz: welche der offenen Aufgaben gemeint ist, mit der
	// kurzen Kennung aus der Liste, die dem Zug gezeigt wurde.
	Aufgabe string `json:"task"`
	// Bei einer Notiz: was der Agent dazu im Gespräch sagt (#460). Die Notiz
	// steht an der Aufgabe und nirgends sonst — ohne diesen Satz blieb eine
	// Frage, die zugleich etwas nachreichte, im Gespräch unbeantwortet.
	Antwort string `json:"reply"`
	// Bei einer Suche: wonach — ein paar Wörter, ein Name, ein Thema.
	Anfrage string `json:"query"`
	// For a config change (#491): what to change, in plain words, for the
	// config assistant that drafts it. Title names it in one line; Text is
	// what the agent says in the chat, with ApproversPlatzhalter.
	Aenderung string `json:"change"`
	// Meta is what the platform notes on the messages this decision writes
	// (#471): the voice chosen and why. Set by the caller, never parsed.
	Meta map[string]string `json:"-"`
}

// Offen ist eine Aufgabe, wie der Zug sie zu sehen bekommt: knapp, und ohne
// alles, was er nicht braucht, um sie wiederzuerkennen.
type Offen struct {
	Kurz   string
	Titel  string
	Status string
	Alter  string
}

/* Fertig ist ein abgeschlossener Vorgang — mit dem, was dabei herauskam.
 *
 * Ohne diese Liste konnte der Zug auf „ist das gestern rausgegangen?" nur
 * raten oder eine Aufgabe eröffnen, und genau dieser Satz steht in spec/28 als
 * das Beispiel, für das die Triage überhaupt gebaut wurde. Wer nur die offenen
 * Vorgänge sieht, weiß, was noch zu tun ist, und nichts davon, was getan
 * wurde.
 *
 * Das Ergebnis ist gekürzt: Ein Lauf kann zwanzig Zeilen hinterlassen, und
 * fünf davon reichen, um zu sagen, ob es geklappt hat. */
type Fertig struct {
	Titel    string
	Ausgang  string // done | failed
	Alter    string
	Ergebnis string
}

// Beitrag is one line of a conversation as a turn reads it: who said it and
// what. Wer is "you" for the agent itself, a name for everybody else.
type Beitrag struct {
	Wer  string
	Text string
}

/* Rahmen ist, wer da spricht, mit wem und wo — was beide Züge bekommen,
 * die Triage und das Erzählen. Ein Objekt statt fünf Zeichenketten, weil
 * jede neue Zeile dieses Rahmens (die Gruppe, #440; der Umgangston, #457)
 * sonst eine Unterschrift mehr in jedem Aufrufer war. */
type Rahmen struct {
	// Rolle und Seele: der Agent — Name, Titel, Zuständigkeit, SOUL.md.
	Rolle, Seele string
	// Gegenueber: die Person, von der die Nachricht kam (#412).
	Gegenueber string
	// Raum: die Gruppe, leer im direkten Gespräch (Raum()).
	Raum string
	// Ton: wie der Agent im Team-Chat redet, wie die Organisation es
	// eingestellt hat (voice.ChatTone.Prompt, #457). Leer: nichts eingestellt.
	Ton string
	// Stimme is the chat voice chosen for this conversation (#471): its
	// released description and at most two passages (voice.ChatPrompt).
	// Empty when no level names one.
	Stimme string
	// Publikum is how the departments of the people spoken to want to be
	// spoken to (voice.AudiencePrompt, #471). Empty when none said.
	Publikum string
	// Vorschlaege: the organisation lets the triage draft changes to the
	// agent's own configuration (#491). Takt is its HEARTBEAT.md, so that
	// "how often do you check?" can be answered rather than proposed. Only
	// the triage reads them; the narration changes nothing.
	Vorschlaege bool
	Takt        string
}

// Bounds of the two #471 blocks in a turn. Both are bounded where they are
// built; these hold even for a caller that did not.
const (
	stimmeMax   = 2400
	publikumMax = 1600
)

func (r Rahmen) schreiben(b *strings.Builder) {
	stimme(b, r.Rolle, r.Seele)
	if v := strings.TrimSpace(r.Stimme); v != "" {
		b.WriteString(kuerzen(v, stimmeMax))
		b.WriteString("\n\n")
	}
	if t := strings.TrimSpace(r.Ton); t != "" {
		b.WriteString(kuerzen(t, 800))
		b.WriteString("\n\n")
	}
	person(b, r.Gegenueber)
	if p := strings.TrimSpace(r.Publikum); p != "" {
		b.WriteString(kuerzen(p, publikumMax))
		b.WriteString("\n\n")
	}
	/* A group (#440): who else is in it, and that the agent was addressed.
	   Without it every name in the conversation reads as the one person the
	   agent talks to. */
	if raum := strings.TrimSpace(r.Raum); raum != "" {
		b.WriteString(raum)
		b.WriteString("\n\n")
	}
}

/*
Raum describes a group to the agent's turns (#440): its title, who is in

	it, and — for the triage — that it was addressed. Empty for a direct
	conversation: there the person is the one the prompt already describes.

	And what kind of place it is (#457): a room with several colleagues in it,
	people and agents, where the agent is one of them and not the host.
	Without that a greeting in a group got the answer of a service desk. The
	author is named with the first name to use: "speak to the person by their
	first name" alone was ignored more often than not, because the turn has to
	work out who the person is before it can follow it.
*/
func Raum(conv Conversation, agentID uuid.UUID, von string, angesprochen bool) string {
	if conv.Kind != KindGroup {
		return ""
	}
	var wer []string
	for _, m := range conv.Active() {
		switch {
		case m.Kind == MemberAgent && m.ID == agentID:
			continue
		case m.Kind == MemberAgent:
			wer = append(wer, m.Name+" (AI colleague)")
		default:
			wer = append(wer, m.Name)
		}
	}
	titel := ""
	if conv.Title != "" {
		titel = fmt.Sprintf(" %q", conv.Title)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "This is the group conversation%s. Besides you, in it: %s. You are one colleague among them, not the host: everybody here reads what you write. Keep it shorter than in a direct chat, and do not repeat what somebody already said.",
		titel, strings.Join(wer, ", "))
	von = strings.TrimSpace(von)
	vorname := von
	if f := strings.Fields(von); len(f) > 0 {
		vorname = f[0]
	}
	switch {
	case angesprochen && von != "":
		fmt.Fprintf(&b, " The new message is from %s and addresses you. Address %s by name — start with it (%q, %q) — answer what is yours to answer and leave the rest to the others.",
			von, vorname, vorname+", …", "Hi "+vorname+"!")
	case angesprochen:
		b.WriteString(" You were addressed in the new message; speak to the person by their first name, answer what is yours to answer and leave the rest to the others.")
	case von != "":
		fmt.Fprintf(&b, " %s asked for this. Address %s by name — start with it (%q) — so the others know whom you answer.", von, vorname, vorname+", …")
	default:
		b.WriteString(" Speak to the person who asked by their first name.")
	}
	return b.String()
}

// TriageMaxTokens: die Antwort ist ein kurzes JSON-Objekt. Wer hier viel
// Platz gibt, bekommt einen Aufsatz und zahlt dafür.
const TriageMaxTokens = 1200

const triageSystem = `You are the triage step of an AI agent inside covey, a platform that runs AI agents as employees.

A person wrote a message to this agent. Decide what kind of thing it is, and answer with ONE JSON object and nothing else — always, even for a greeting, a thank-you or a single emoji:

{"action":"answer","text":"…"}                 — you can settle it right here
{"action":"note","task":"ab12","text":"…","reply":"…"} — this adds to a task you already have
{"action":"task","title":"…","body":"…","text":"…"} — this is new work
{"action":"search","query":"…"}                — you need to look something up first

Decide in this order.

First: is it a request to DO something — reply to a customer, refund, send, change, book, check or fix something in a ticket, an invoice, a repository, a mailbox? Then it is a "task", always, also when you do not know the ticket or the thing: finding it is part of the work. Never answer such a request with "I cannot see that" — that sentence is only for questions about what already happened.

Choose "answer" when the message is a question about the organisation you can answer from the org chart below (who a colleague is, what they do, who is responsible for something, which department someone is in, who your manager is), a question about what was already said in this thread, about your own open tasks or about one you recently finished (both are listed below, the finished ones with their outcome) — "any news on the Globex invoice?" is an answer: say where it stands from the list, e.g. that you are still on it and since when, a thank-you, a greeting, an acknowledgement, or a clarification you can give without looking anything up. "Did that go out yesterday?" is an answer when the task is in that list — say what it says, and say when it is not there.

Choose "note" when the message adds to or corrects one specific task you already have — new information, a changed wish, the answer to a question you asked. Use the short id from the list. Your "text" is written onto that task, and if it was waiting for an answer this releases it. Do not open a second task for the same thing. A note is not seen in the conversation: "reply" is what you say there — a short acknowledgement ("Danke, nehm ich mit."), and when the message also asks something, the answer. Always give a "reply".

Choose "search" when the answer needs something you do not see below: an earlier part of this conversation (you are shown only its end), a task of yours that is not in the lists, or a person or colleague who is not in the org chart as shown — a name may be misspelt. Give a few words to search for (a name, a topic). covey searches the whole conversation, your backlog (titles, states and outcomes of your tasks, also older ones than listed below) and the org chart, tolerating a typo in a name, and asks you again with what it found. You can search once.

Choose "task" when doing it would need any of: a target system (ticketing, repository, mailbox, calendar), a file, a command, a search outside this conversation, a decision with consequences, or more than a moment of work. When in doubt choose "task" — an unnecessary task costs a run, a wrongly answered job costs the work itself.

You can see your own backlog and write to it, and that is all. You have NO target system, NO credentials, NO files, NO commands, NO search and NO memory beyond what stands below. Never claim to have done, checked, sent or looked at anything outside this list. If answering would require any of that, it is a task.

How you write (for "answer", and for the "text" of a task): you are this colleague, chatting in the team chat. Write the way a person writes in a work chat — short, direct, warm where it fits, in the language of the message. Usually one or two sentences; in a group, where several people read along, keep it shorter still. No headings, no bullet lists, no bold, no sign-off, no "As an AI", no restating the question, no offering a menu of further help. If the agent's own description below says how it talks, talk like that; if "How you talk in the team chat" is given below, it is what your organisation set, and it wins over everything else here about address, tone and emoji.

You are a colleague, not a service desk:
- A greeting is answered like a greeting: "hi" gets a hi back. "What's up?" or "was geht?" gets a real, short answer, as a colleague gives it — what you are on right now (from your open tasks), or that it is quiet.
- A thank-you gets a short "gern" or "you're welcome", or an emoji — not a summary of what you did.
- Never talk about the machinery: not "the task", "the run", "the result", "the report", "the record", and never "I have answered …" or "I have explained …". Say the thing itself.
- Use the person's first name when it helps — in a group, where several people read along, start with it.
- In a group you are one colleague among several, people and AI colleagues. Answer what is yours, do not repeat what somebody already said in the conversation, and leave to a colleague what is theirs.

Fit what you say to the person you are talking to (described below, when known). With someone whose role is not technical, say what it means for them in plain words — "the demo works again", not "the pod is out of CrashLoopBackOff"; "it ran out of memory", not "OOMKilled, limit raised to 1Gi"; "the fix is live", not "pipeline #812 is green". No pod, container, deployment, branch, pipeline, commit, API, token, log or error code, unless they ask. With a technical colleague, be precise and name the ticket, the branch or the error. If a department's "how it wants to be spoken to" is given below, your organisation set it: follow it. When the message only needs acknowledging, the "text" may be a bare emoji (1–3 characters) — inside the JSON object: {"action":"answer","text":"👍"} — unless "How you talk in the team chat" says no emoji: then acknowledge in a word.

For "answer": answer from the lists above and from this thread, never from memory of anything else: if a task is not in them, say that you cannot see it rather than guessing what became of it.
For "note": the "text" is what the run should know, in one or two sentences; the "reply" is what you say in the chat.
For "task": the title is one line in the imperative, the body carries what the person said and any context from the thread that the run will need. The text is what you say in the chat right now, before you start: a short acknowledgement that you are on it ("Mach ich, ich schau mir die Rechnung an und melde mich."). Promise nothing about the outcome and no time.`

// TaktMax: so much HEARTBEAT.md goes into a turn. A heartbeat is a few
// lines; one that is longer is still answered from its beginning.
const TaktMax = 1500

/* triageKonfig is the fifth choice (#491), appended only while the
 * organisation has the trial on. Its boundary is the agent's OWN
 * configuration: a wish about a colleague's is not this agent's to draft,
 * and a question about the configuration is answered, not proposed. */
const triageKonfig = `

Your organisation also lets people change your own configuration from this chat (a trial). There is a fifth choice:

{"action":"config","title":"…","change":"…","text":"…"} — change how YOU are set up

Choose "config" when the message asks you to change your own setup from now on: when or how often you check your queue or wake up (your heartbeat, shown below), a procedure you follow (your playbooks), who you are or how you talk (your soul), which systems or hosts you may use (your access). "Check your queue only on weekdays" is config; "don't do the Monday report any more" is config.
It is NOT config when the message only asks about your setup — "how often do you check your queue?" is an answer, from your heartbeat below; when it is one piece of work — "check the queue now" is a task; or when it is about a colleague's setup — then answer that it is theirs to change and name them. In a group, choose it only when the message addresses you.
"title" is one line naming the change. "change" says what to change in plain words, complete enough for somebody who edits your configuration files: the person's wish and any detail from the conversation. "text" is what you say in the chat, short and in the language of the message: that you drafted it and that it waits for ` + ApproversPlatzhalter + ` to accept it — write ` + ApproversPlatzhalter + ` literally, covey puts in the names. Nothing changes before somebody accepts it: never say it is done.`

// Triagieren führt den Zug aus. Der Fehlerfall ist bewusst weich: Wer nicht
// entscheiden kann, eröffnet eine Aufgabe — das ist das Verhalten, das immer
// funktioniert, und der Aufrufer muss dafür nichts wissen.
func Triagieren(ctx context.Context, p llm.Provider, r Rahmen, organisation string, offen []Offen, fertig []Fertig, verlauf []Beitrag, nachricht string, suche *Suche) (Entscheidung, error) {
	var b strings.Builder
	r.schreiben(&b)
	/* Das Organigramm (#415), derselbe Text, den ein Lauf bekommt. Es ist
	   coveys eigenes Objekt wie der Backlog: Es zu lesen braucht keine
	   Zugangsdaten und verlässt das Haus nicht — und ohne es konnte der
	   Agent auf „kennst du den QA-Kollegen?" nur raten. */
	if o := strings.TrimSpace(organisation); o != "" {
		b.WriteString("The organisation you work in (the org chart, as your runs know it):\n")
		b.WriteString(kuerzen(o, OrganisationMax))
		b.WriteString("\n\n")
	}
	/* Der eigene Backlog. Er steht vor dem Gespräch, weil er der Zustand ist
	   und das Gespräch nur die Bewegung darauf. */
	if len(offen) > 0 {
		b.WriteString("Your open tasks:\n")
		for _, o := range offen {
			fmt.Fprintf(&b, "  [%s] %s — %s, %s\n", o.Kurz, kuerzen(o.Titel, 120), o.Status, o.Alter)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("You have no open tasks.\n\n")
	}
	/* Und was schon erledigt ist. Es steht NACH den offenen Vorgängen, weil
	   eine Notiz immer an einen offenen geht — die kurzen Kennungen stehen
	   oben, und hier unten gibt es keine, an die man schreiben könnte. */
	if len(fertig) > 0 {
		b.WriteString("Recently finished, oldest last:\n")
		for _, f := range fertig {
			fmt.Fprintf(&b, "  %s — %s, %s", kuerzen(f.Titel, 120), f.Ausgang, f.Alter)
			if f.Ergebnis != "" {
				fmt.Fprintf(&b, "\n    outcome: %s", kuerzen(einzeilig(f.Ergebnis), 400))
			}
			b.WriteString("\n")
		}
		b.WriteString("\n")
	}
	/* The heartbeat (#491), beside the backlog: coveys own object as well,
	   and without it "how often do you look?" could only be guessed at. */
	if r.Vorschlaege {
		takt := strings.TrimSpace(r.Takt)
		if takt == "" {
			takt = "(empty — you have no heartbeat)"
		}
		b.WriteString("Your HEARTBEAT.md, as it runs now:\n")
		b.WriteString(kuerzen(takt, TaktMax))
		b.WriteString("\n\n")
	}
	gespraech(&b, verlauf)
	b.WriteString("The new message:\n")
	b.WriteString(nachricht)
	if suche != nil {
		fmt.Fprintf(&b, "\n\nYou searched the whole conversation, your backlog and the org chart for %q. Found:\n", suche.Anfrage)
		if len(suche.Treffer) == 0 {
			b.WriteString("(nothing)\n")
		}
		for _, t := range suche.Treffer {
			fmt.Fprintf(&b, "- %s\n", kuerzen(einzeilig(t), 500))
		}
		if r.Vorschlaege {
			b.WriteString("\nDecide now: answer, note, task or config. Do not search again.")
		} else {
			b.WriteString("\nDecide now: answer, note or task. Do not search again.")
		}
	}

	system := triageSystem
	if r.Vorschlaege {
		system += triageKonfig
	}

	roh, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierFast,
		MaxTokens: TriageMaxTokens,
		/* Ohne Nachdenken: Die Antwort ist eine Auswahl aus zwei Möglichkeiten
		   und ein kurzer Text. Denkzeit kostet hier Geld und Latenz und
		   verbessert nichts. */
		NoThinking: true,
		System:     system,
		Messages:   []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return Entscheidung{}, err
	}
	return lesen(roh)
}

// lesen holt das JSON aus der Antwort. Modelle stellen gern einen Satz davor
// oder packen es in einen Codeblock; beides ist hier kein Fehler, sondern
// etwas, das man abschneidet.
//
// Und eine kurze Antwort ganz ohne JSON ist eine Antwort (#457): Auf „@demo
// was geht?" kam zweimal ein nacktes Emoji, der Zug galt als gescheitert,
// und ein Gruß wurde eine Aufgabe mit Sandbox. Was keine Klammer enthält und
// kurz ist, hat das Modell den Leuten gesagt, nicht covey — es ist die
// Antwort. Was nach JSON aussieht und keins ist, bleibt ein Fehler: Dort hat
// das Modell etwas anderes gewollt, und eine halbe Aufgabe als Chatzeile zu
// posten wäre schlimmer als eine Aufgabe zu viel.
func lesen(roh string) (Entscheidung, error) {
	s := strings.TrimSpace(roh)
	if direkt, ok := direkteAntwort(s); ok {
		return Entscheidung{Aktion: AktionAntwort, Text: direkt}, nil
	}
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	var e Entscheidung
	if err := json.Unmarshal([]byte(s), &e); err != nil {
		return Entscheidung{}, fmt.Errorf("triage answer is not JSON: %w", err)
	}
	switch e.Aktion {
	case AktionAntwort:
		if strings.TrimSpace(e.Text) == "" {
			return Entscheidung{}, fmt.Errorf("triage: answer without text")
		}
	case AktionAufgabe:
		if strings.TrimSpace(e.Titel) == "" {
			return Entscheidung{}, fmt.Errorf("triage: task without title")
		}
	case AktionNotiz:
		if strings.TrimSpace(e.Aufgabe) == "" || strings.TrimSpace(e.Text) == "" {
			return Entscheidung{}, fmt.Errorf("triage: note without task or text")
		}
	case AktionSuche:
		if strings.TrimSpace(e.Anfrage) == "" {
			return Entscheidung{}, fmt.Errorf("triage: search without query")
		}
	case AktionKonfig:
		if strings.TrimSpace(e.Aenderung) == "" {
			return Entscheidung{}, fmt.Errorf("triage: config without change")
		}
	default:
		return Entscheidung{}, fmt.Errorf("triage: unknown action %q", e.Aktion)
	}
	return e, nil
}

// DirektMax: so lang darf eine Antwort ohne JSON sein. Ein Satz oder zwei
// Chat — was länger ist, ist kein Zuruf, sondern ein Modell, das das Format
// vergessen hat, und das soll lieber noch einmal gefragt werden.
const DirektMax = 400

// direkteAntwort erkennt die kurze Chatzeile ohne JSON: keine Klammer, kein
// Codeblock, nicht leer, nicht länger als DirektMax Zeichen.
func direkteAntwort(s string) (string, bool) {
	if s == "" || strings.ContainsAny(s, "{}") || strings.Contains(s, "```") {
		return "", false
	}
	if len([]rune(s)) > DirektMax {
		return "", false
	}
	return s, true
}

// einzeilig macht aus dem Ergebnis eines Laufs eine Zeile. Ein Prompt, in dem
// ein fremder Text eigene Absätze und Aufzählungen mitbringt, liest sich für
// das Modell wie eine zweite Anweisung.
func einzeilig(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func kuerzen(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " […]"
}

/* stimme ist, wer da spricht (#411): Rolle und SOUL.md. Die Rolle allein —
 * Name, Titel, Zuständigkeit — ergab eine Stimme wie ein Formular; wie ein
 * Agent redet, steht in seiner SOUL.md, und die ist Config as Code wie alles
 * andere an ihm. Gekürzt, weil sie auch Arbeitsanweisungen trägt, die zwei
 * Sätze Chat nicht brauchen. */
func stimme(b *strings.Builder, rolle, seele string) {
	if rolle != "" {
		b.WriteString("The agent's role:\n")
		b.WriteString(rolle)
		b.WriteString("\n\n")
	}
	if s := strings.TrimSpace(seele); s != "" {
		b.WriteString("Who the agent is (its SOUL.md):\n")
		b.WriteString(kuerzen(s, SeeleMax))
		b.WriteString("\n\n")
	}
}

// gespraech is the end of the conversation, oldest first; "you" is the agent.
func gespraech(b *strings.Builder, verlauf []Beitrag) {
	if len(verlauf) == 0 {
		return
	}
	b.WriteString("The conversation so far, oldest first (\"you\" is you):\n")
	for _, m := range verlauf {
		fmt.Fprintf(b, "%s: %s\n", m.Wer, kuerzen(einzeilig(m.Text), verlaufZeile))
	}
	b.WriteString("\n")
}

/* person ist, mit wem da gesprochen wird (#412): Name, Titel, Abteilung,
 * Zuständigkeit — was die Organisation ohnehin über jemanden führt (das
 * Organigramm, spec/28 §1). Ohne das sprach ein Agent mit der Vertrieblerin
 * wie mit dem Entwickler: dieselben Ticketnummern, dieselben Branches. */
func person(b *strings.Builder, gegenueber string) {
	if g := strings.TrimSpace(gegenueber); g != "" {
		b.WriteString("Who you are talking to:\n")
		b.WriteString(kuerzen(g, 800))
		b.WriteString("\n\n")
	}
}

// verlaufZeile: so viel von einem Beitrag steht im Verlauf eines Zugs. Ein
// Ergebnis ist länger als ein Satz, und wer „welches davon?" fragt, meint
// etwas aus seiner Mitte (#413).
const verlaufZeile = 1200

// OrganisationMax: so viel Organigramm geht in einen Zug — genug für ein
// paar Dutzend Kollegen mit Zuständigkeit.
const OrganisationMax = 6000

// SeeleMax: so viel SOUL.md geht in einen Zug. Der Anfang sagt, wer jemand
// ist; was danach kommt, sind meist Regeln für die Arbeit.
const SeeleMax = 3000

// ErzaehlMaxTokens: ein paar Sätze Chat.
const ErzaehlMaxTokens = 400

const erzaehlSystem = `You are an AI agent inside covey, a platform that runs AI agents as employees, chatting with the people of your organisation in their team chat.

Earlier somebody asked you for something, you said you would look into it, and you did the work in your workspace. It is finished now, and what came out stands below. Tell them in the chat — as the colleague who did it, not as a system reporting on it.

Write the way a colleague writes in a work chat: short, direct, in the language of the conversation. Usually two to four sentences; in a group, where several people read along, fewer. Lead with the outcome: the answer, the finding, what changed. No headings, no bold, no tables, no bullet list unless there really are several separate things to name, no sign-off, no "As an AI", no offering a menu of further help. If the agent's own description says how it talks, talk like that; if "How you talk in the team chat" is given below, follow it — your organisation set it. In a group, address the person who asked by their first name, and do not repeat what somebody already said in the conversation.

Never talk about the machinery: not "the task", "the run", "the result", "the report", "the record", and never "I have answered your question" or "I explained …" — say the answer itself.

Fit it to the person you are talking to (described below, when known). With someone whose role is not technical, say what came out and what it means for them, in plain words — "the demo works again, it had run out of memory and now has more", not "the pod was OOMKilled, limit raised to 1Gi"; "my access to the ticket system has expired", not "401 from the API, token expired". No pod, container, RAM, deployment, branch, pipeline, commit, API, token, log, stack trace or error code unless they asked; the full report stays available to them anyway. With a technical colleague, be precise: name the ticket, the branch, the error. If a department's "how it wants to be spoken to" is given below, your organisation set it: follow it.

Say only what the result says. Do not add, soften or improve anything, and do not claim anything the result does not state. Putting a technical term into plain words for a non-technical person is not changing what it says — it is the job: "the pod ran out of memory" becomes "it had run out of memory". If the result only records that something was done, without its content — "greeting answered, role explained" — do not retell that sentence: if it was a greeting or small talk, reply to it now as a colleague would, from your role and the conversation; otherwise say in plain words what you did, without inventing the details the result leaves out. If it failed, say so plainly and, if the error says it, what is missing or what the person could do. If the result asks the person something, ask it.

Answer with the chat message only — no JSON, no quotes around it.`

// Erzaehlen macht aus dem Ergebnis eines Laufs, was der Agent im Chat sagt
// (#411). Dieselben Grenzen wie die Triage: kein Zielsystem, keine
// Zugangsdaten, nur das Ergebnis und das Gespräch.
func Erzaehlen(ctx context.Context, p llm.Provider, r Rahmen, verlauf []Beitrag, auftrag, ausgang, ergebnis string) (string, error) {
	var b strings.Builder
	r.schreiben(&b)
	gespraech(&b, verlauf)
	fmt.Fprintf(&b, "What you were asked to do:\n%s\n\n", kuerzen(auftrag, 1500))
	if ausgang == "failed" {
		b.WriteString("It did NOT work. What went wrong:\n")
	} else {
		b.WriteString("It is done. What came out:\n")
	}
	b.WriteString(kuerzen(ergebnis, 6000))

	roh, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierFast,
		MaxTokens:  ErzaehlMaxTokens,
		NoThinking: true,
		System:     erzaehlSystem,
		Messages:   []llm.Message{{Role: "user", Content: b.String()}},
	})
	if err != nil {
		return "", err
	}
	text := strings.TrimSpace(roh)
	if text == "" {
		return "", fmt.Errorf("narration: empty")
	}
	return text, nil
}
