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
 *   keine Werkzeuge, keine Zielsysteme, keine Zugangsdaten.
 *
 * Er liest das Gespräch und erzeugt Text. Alles, wofür er mehr bräuchte, ist
 * genau der Grund, stattdessen eine Aufgabe zu eröffnen — und das ist keine
 * Einschränkung, die man später lockert, sondern die Grenze, die diesen Weg
 * überhaupt zulässig macht. Ein billiger Pfad darf nicht der billige Weg an
 * den Guard-Rails vorbei werden (spec/28, Abschnitt 5).
 */

// Aktion ist, was mit einer Nachricht geschehen soll.
type Aktion string

const (
	// AktionAntwort: der Agent antwortet im Verlauf. Keine Aufgabe.
	AktionAntwort Aktion = "answer"
	// AktionAufgabe: das ist Arbeit — der gewöhnliche Backlog-Vorgang.
	AktionAufgabe Aktion = "task"
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
}

// TriageMaxTokens: die Antwort ist ein kurzes JSON-Objekt. Wer hier viel
// Platz gibt, bekommt einen Aufsatz und zahlt dafür.
const TriageMaxTokens = 700

const triageSystem = `You are the triage step of an AI agent inside covey, a platform that runs AI agents as employees.

A person wrote a message to this agent. Decide what kind of thing it is, and answer with ONE JSON object and nothing else:

{"action":"answer","text":"…"}      — you can settle it right here, from the conversation alone
{"action":"task","title":"…","body":"…"}  — this is work

Choose "answer" when the message is a question about what was already said in this thread, a thank-you, a greeting, an acknowledgement, or a clarification you can give without looking anything up.

Choose "task" when doing it would need any of: a target system (ticketing, repository, mailbox, calendar), a file, a command, a search outside this conversation, a decision with consequences, or more than a moment of work. When in doubt choose "task" — an unnecessary task costs a run, a wrongly answered job costs the work itself.

You have NO tools, NO access to any system, and NO memory beyond the messages below. Never claim to have done, checked, sent or looked at anything. If answering would require any of that, it is a task.

For "answer": write in the agent's voice, in the language of the message, at most three sentences. A bare emoji (1–3 characters) is a valid answer when the message only needs acknowledging.
For "task": the title is one line in the imperative, the body carries what the person said and any context from the thread that the run will need.`

// Triagieren führt den Zug aus. Der Fehlerfall ist bewusst weich: Wer nicht
// entscheiden kann, eröffnet eine Aufgabe — das ist das Verhalten, das immer
// funktioniert, und der Aufrufer muss dafür nichts wissen.
func Triagieren(ctx context.Context, p llm.Provider, rolle string, verlauf []Message, nachricht string) (Entscheidung, error) {
	var b strings.Builder
	if rolle != "" {
		b.WriteString("The agent's role:\n")
		b.WriteString(rolle)
		b.WriteString("\n\n")
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
