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

// TriageMaxTokens: die Antwort ist ein kurzes JSON-Objekt. Wer hier viel
// Platz gibt, bekommt einen Aufsatz und zahlt dafür.
const TriageMaxTokens = 700

const triageSystem = `You are the triage step of an AI agent inside covey, a platform that runs AI agents as employees.

A person wrote a message to this agent. Decide what kind of thing it is, and answer with ONE JSON object and nothing else:

{"action":"answer","text":"…"}                 — you can settle it right here
{"action":"note","task":"ab12","text":"…"}     — this belongs to a task you already have
{"action":"task","title":"…","body":"…"}       — this is new work

Choose "answer" when the message is a question about what was already said in this thread or about your own open tasks (they are listed below), a thank-you, a greeting, an acknowledgement, or a clarification you can give without looking anything up.

Choose "note" when the message adds to, corrects or asks about one specific task you already have. Use the short id from the list. Your text is written onto that task, and if it was waiting for an answer this releases it. Do not open a second task for the same thing.

Choose "task" when doing it would need any of: a target system (ticketing, repository, mailbox, calendar), a file, a command, a search outside this conversation, a decision with consequences, or more than a moment of work. When in doubt choose "task" — an unnecessary task costs a run, a wrongly answered job costs the work itself.

You can see your own backlog and write to it, and that is all. You have NO target system, NO credentials, NO files, NO commands, NO search and NO memory beyond what stands below. Never claim to have done, checked, sent or looked at anything outside this list. If answering would require any of that, it is a task.

For "answer": write in the agent's voice, in the language of the message, at most three sentences. A bare emoji (1–3 characters) is a valid answer when the message only needs acknowledging.
For "note": the text is what the run should know, in one or two sentences.
For "task": the title is one line in the imperative, the body carries what the person said and any context from the thread that the run will need.`

// Triagieren führt den Zug aus. Der Fehlerfall ist bewusst weich: Wer nicht
// entscheiden kann, eröffnet eine Aufgabe — das ist das Verhalten, das immer
// funktioniert, und der Aufrufer muss dafür nichts wissen.
func Triagieren(ctx context.Context, p llm.Provider, rolle string, offen []Offen, verlauf []Message, nachricht string) (Entscheidung, error) {
	var b strings.Builder
	if rolle != "" {
		b.WriteString("The agent's role:\n")
		b.WriteString(rolle)
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
	if len(verlauf) > 0 {
		b.WriteString("The conversation so far, oldest first:\n")
		for _, m := range verlauf {
			wer := "agent"
			if !strings.HasPrefix(m.Author, "agent") {
				wer = "person"
			}
			fmt.Fprintf(&b, "%s: %s\n", wer, kuerzen(m.Text, 600))
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

func kuerzen(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + " […]"
}
