package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
)

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

// TriageMaxTokens: die Antwort ist ein kurzes JSON-Objekt. Wer hier viel
// Platz gibt, bekommt einen Aufsatz und zahlt dafür.
const TriageMaxTokens = 700

const triageSystem = `You are the triage step of an AI agent inside covey, a platform that runs AI agents as employees.

A person wrote a message to this agent. Decide what kind of thing it is, and answer with ONE JSON object and nothing else:

{"action":"answer","text":"…"}                 — you can settle it right here
{"action":"note","task":"ab12","text":"…"}     — this belongs to a task you already have
{"action":"task","title":"…","body":"…","text":"…"} — this is new work

Choose "answer" when the message is a question about what was already said in this thread, about your own open tasks or about one you recently finished (both are listed below, the finished ones with their outcome), a thank-you, a greeting, an acknowledgement, or a clarification you can give without looking anything up. "Did that go out yesterday?" is an answer when the task is in that list — say what it says, and say when it is not there.

Choose "note" when the message adds to, corrects or asks about one specific task you already have. Use the short id from the list. Your text is written onto that task, and if it was waiting for an answer this releases it. Do not open a second task for the same thing.

Choose "task" when doing it would need any of: a target system (ticketing, repository, mailbox, calendar), a file, a command, a search outside this conversation, a decision with consequences, or more than a moment of work. When in doubt choose "task" — an unnecessary task costs a run, a wrongly answered job costs the work itself.

You can see your own backlog and write to it, and that is all. You have NO target system, NO credentials, NO files, NO commands, NO search and NO memory beyond what stands below. Never claim to have done, checked, sent or looked at anything outside this list. If answering would require any of that, it is a task.

How you write (for "answer", and for the "text" of a task): you are this colleague, chatting. Write the way a person writes in a work chat — short, direct, warm where it fits, in the language of the message. Usually one or two sentences. No headings, no bullet lists, no bold, no sign-off, no "As an AI", no restating the question, no offering a menu of further help. If the agent's own description below says how it talks, talk like that. Fit what you say to the person you are talking to (described below, when known): with someone whose role is not technical, say what it means for them in plain words and leave out file names, commands, branch names and jargon unless they ask; with a technical colleague, be precise and name the ticket, the branch or the error. A bare emoji (1–3 characters) is a valid answer when the message only needs acknowledging.

For "answer": answer from the lists above and from this thread, never from memory of anything else: if a task is not in them, say that you cannot see it rather than guessing what became of it.
For "note": the text is what the run should know, in one or two sentences.
For "task": the title is one line in the imperative, the body carries what the person said and any context from the thread that the run will need. The text is what you say in the chat right now, before you start: a short acknowledgement that you are on it ("Mach ich, ich schau mir die Rechnung an und melde mich."). Promise nothing about the outcome and no time.`

// Triagieren führt den Zug aus. Der Fehlerfall ist bewusst weich: Wer nicht
// entscheiden kann, eröffnet eine Aufgabe — das ist das Verhalten, das immer
// funktioniert, und der Aufrufer muss dafür nichts wissen.
func Triagieren(ctx context.Context, p llm.Provider, rolle, seele, gegenueber string, offen []Offen, fertig []Fertig, verlauf []Message, nachricht string) (Entscheidung, error) {
	var b strings.Builder
	stimme(&b, rolle, seele)
	person(&b, gegenueber)
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
	if len(verlauf) > 0 {
		b.WriteString("The conversation so far, oldest first:\n")
		for _, m := range verlauf {
			wer := "agent"
			if !strings.HasPrefix(m.Author, "agent") {
				wer = "person"
			}
			fmt.Fprintf(&b, "%s: %s\n", wer, kuerzen(einzeilig(m.Text), verlaufZeile))
		}
		b.WriteString("\n")
	}
	b.WriteString("The new message:\n")
	b.WriteString(nachricht)

	roh, err := p.Complete(ctx, llm.Request{
		Tier:      llm.TierFast,
		MaxTokens: TriageMaxTokens,
		/* Ohne Nachdenken: Die Antwort ist eine Auswahl aus zwei Möglichkeiten
		   und ein kurzer Text. Denkzeit kostet hier Geld und Latenz und
		   verbessert nichts. */
		NoThinking: true,
		System:     triageSystem,
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
func lesen(roh string) (Entscheidung, error) {
	s := strings.TrimSpace(roh)
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
	default:
		return Entscheidung{}, fmt.Errorf("triage: unknown action %q", e.Aktion)
	}
	return e, nil
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

// SeeleMax: so viel SOUL.md geht in einen Zug. Der Anfang sagt, wer jemand
// ist; was danach kommt, sind meist Regeln für die Arbeit.
const SeeleMax = 3000

// ErzaehlMaxTokens: ein paar Sätze Chat.
const ErzaehlMaxTokens = 400

const erzaehlSystem = `You are an AI agent inside covey, a platform that runs AI agents as employees, chatting with a person in a work chat.

Earlier the person asked you for something, you said you would look into it, and you did the work in your workspace. The run has ended, and its result — a report written for the record — stands below. Now tell the person in the chat what came out.

Write the way a colleague writes in a work chat: short, direct, in the language of the conversation. Usually two to four sentences. Lead with the outcome. No headings, no bold, no tables, no bullet list unless there really are several separate things to name, no sign-off, no "As an AI", no offering a menu of further help. If the agent's own description says how it talks, talk like that.

Fit it to the person you are talking to (described below, when known). With someone whose role is not technical, say what came out and what it means for them, in plain words — no file names, commands, branch names, stack traces or jargon unless they asked for them; the full report stays available to them anyway. With a technical colleague, be precise: name the ticket, the branch, the error.

Say only what the result says. Do not add, soften or improve anything, and do not claim anything the result does not state. If the run failed, say so plainly and, if the result or error says it, what is missing or what the person could do. If the result asks the person something, ask it.

Answer with the chat message only — no JSON, no quotes around it.`

// Erzaehlen macht aus dem Ergebnis eines Laufs, was der Agent im Chat sagt
// (#411). Dieselben Grenzen wie die Triage: kein Zielsystem, keine
// Zugangsdaten, nur das Ergebnis und das Gespräch.
func Erzaehlen(ctx context.Context, p llm.Provider, rolle, seele, gegenueber string, verlauf []Message, auftrag, ausgang, ergebnis string) (string, error) {
	var b strings.Builder
	stimme(&b, rolle, seele)
	person(&b, gegenueber)
	if len(verlauf) > 0 {
		b.WriteString("The conversation so far, oldest first:\n")
		for _, m := range verlauf {
			wer := "agent"
			if !strings.HasPrefix(m.Author, "agent") {
				wer = "person"
			}
			fmt.Fprintf(&b, "%s: %s\n", wer, kuerzen(einzeilig(m.Text), verlaufZeile))
		}
		b.WriteString("\n")
	}
	fmt.Fprintf(&b, "What you were asked to do:\n%s\n\n", kuerzen(auftrag, 1500))
	if ausgang == "failed" {
		b.WriteString("The run FAILED. Its error:\n")
	} else {
		b.WriteString("The run finished. Its result:\n")
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
