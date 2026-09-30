package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"covey/internal/llm"
)

/* The spoken form of an answer (#502).
 *
 * A message said in a call (meta via=call, #494) is answered twice: in
 * writing, for the conversation, where "DLES-273, MR !475" is exactly what a
 * reader wants; and for the ear, where the same line read out is noise. The
 * written form stays as it is. The spoken one stands beside it in the meta of
 * the agent's message (MetaSpoken), and the call speaks it; when it left
 * something out, MetaDetailsInChat says so and the call adds that the details
 * are in the chat.
 *
 * Three places write it: the triage (its answer, a note's reply, a task's
 * acknowledgement, the line beside a config proposal), the run of a chat
 * answer nobody triaged (a trailing <spoken> tag in its result, SplitSpoken),
 * and — for a task's result that has none — one short turn when the result is
 * posted (Sprechfassung). A written message gets none of this: nothing
 * changes for it, not the prompt and not the meta. */

// The meta keys of an agent's message that carry its spoken form.
const (
	MetaSpoken        = "spoken"
	MetaDetailsInChat = "details_in_chat"
)

// SaidInCall says whether a person's message was said aloud in a call.
func SaidInCall(m Message) bool { return m.Meta[ViaMeta] == ViaCall }

// SpokenMax bounds a spoken form: three short sentences are well under it.
// What is longer is cut at its last sentence end within the bound — a model
// that wrote a paragraph for the ear still must not keep a caller listening.
const SpokenMax = 360

// Sprechbar is a spoken form as it is stored: on one line, without the
// markdown a model may still have put in, and bounded by SpokenMax.
func Sprechbar(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	r := []rune(s)
	if len(r) <= SpokenMax {
		return s
	}
	cut := string(r[:SpokenMax])
	if i := strings.LastIndexAny(cut, ".!?…"); i > SpokenMax/3 {
		_, n := utf8.DecodeRuneInString(cut[i:])
		return strings.TrimSpace(cut[:i+n])
	}
	if i := strings.LastIndex(cut, " "); i > 0 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
}

// WithSpoken is meta with the spoken form beside what it carries: a copy,
// so that one decision's meta is not changed under the other messages it
// writes. Without a spoken form it is meta unchanged.
func WithSpoken(meta map[string]string, spoken string, details bool) map[string]string {
	spoken = Sprechbar(spoken)
	if spoken == "" {
		return meta
	}
	out := make(map[string]string, len(meta)+2)
	for k, v := range meta {
		out[k] = v
	}
	out[MetaSpoken] = spoken
	if details {
		out[MetaDetailsInChat] = "true"
	}
	return out
}

// SpokenRules is how a spoken form is written — the same rules for the
// triage, the run of a chat answer and the turn after a result.
const SpokenRules = `- one to three short sentences, as you would say them on the phone;
- no ids, ticket or merge request numbers, links, addresses, file paths, lists, emojis, markdown or parentheses, unless the person asked for exactly that: say what a thing is — "the merge request for the login bug", not "MR !475";
- numbers as they are said — "about two hundred euros", "half past three" — and no symbols;
- the same language, voice and tone as the written form, and nothing the written form does not say.`

// triageAnruf is appended to the triage's system prompt when the message
// was said in a call, and only then.
const triageAnruf = `

The new message was said aloud in a call, not typed: the person is listening, and what you say is read out to them by a voice. Everything above stays as it is — the written form is what the conversation shows. Add "spoken": the same thing said for the ear:
` + SpokenRules + `
Add "details_in_chat": true when the spoken form leaves out something the written one carries (an id, a link, a list, a figure), false when it says all of it.
"spoken" is what the person hears: for "answer" it says "text", for "note" it says "reply", for "task" the acknowledgement in "text", for "config" the "text" (write ` + ApproversPlatzhalter + ` there too). A "search" needs none.
For example, written: "Done — PROJ-12, MR !88, pipeline green, waiting for Ada's review." Spoken: "Yes, it's in. The merge request is ready, the tests are green, and it's waiting for Ada's review." — {"action":"answer","text":"Done — PROJ-12, MR !88, pipeline green, waiting for Ada's review.","spoken":"Yes, it's in. The merge request is ready, the tests are green, and it's waiting for Ada's review.","details_in_chat":true}`

// fuerAnruf keeps the spoken form of a decision only where there is a call:
// a written message gets none, whatever the model returned, and a search
// says nothing.
func fuerAnruf(e Entscheidung, anruf bool) Entscheidung {
	if !anruf || e.Aktion == AktionSuche {
		e.Gesprochen, e.DetailsImChat = "", false
		return e
	}
	e.Gesprochen = Sprechbar(e.Gesprochen)
	if e.Gesprochen == "" {
		e.DetailsImChat = false
	}
	return e
}

// AnswerMeta is the meta of the message a decision says in the
// conversation: the voice (Meta) and, in a call, the spoken form.
func (e Entscheidung) AnswerMeta() map[string]string {
	return WithSpoken(e.Meta, e.Gesprochen, e.DetailsImChat)
}

// spokenTag is the trailing section a chat answer's run writes in a call
// (agents.ChatAnswerCallDoc).
var spokenTag = regexp.MustCompile(`(?is)\s*<spoken(\s+details_in_chat\s*=\s*["']?(true|false)["']?)?\s*>(.*?)</spoken>\s*`)

// SplitSpoken takes the spoken form out of a run's result: the written reply
// without the tag, the spoken form, and whether it left details for the chat.
// A result without the tag is returned as it stands.
func SplitSpoken(result string) (written, spoken string, details bool) {
	m := spokenTag.FindStringSubmatchIndex(result)
	if m == nil {
		return result, "", false
	}
	spoken = Sprechbar(result[m[6]:m[7]])
	if m[4] >= 0 {
		details = strings.EqualFold(result[m[4]:m[5]], "true")
	}
	written = strings.TrimSpace(result[:m[0]] + "\n\n" + result[m[1]:])
	if spoken == "" {
		details = false
	}
	return written, spoken, details
}

// SprechMaxTokens: a JSON object with three short sentences in it.
const SprechMaxTokens = 300

const sprechSystem = `You are part of covey, a platform that runs AI agents as employees. An AI colleague is in a call with a person, and a message of theirs has just been posted into the conversation. A voice reads it out. Write what the voice says — the same message for the ear:
` + SpokenRules + `

Answer with ONE JSON object and nothing else: {"spoken":"…","details_in_chat":true} — "details_in_chat" is true when the spoken form leaves out something the message carries (an id, a link, a list, a figure), false when it says all of it.`

// Sprechfassung is the spoken form of a message that was written without
// one — a task's result posted while the call it came from may still be on.
// One turn on the fast tier; an error leaves the message to be spoken as it
// is written.
func Sprechfassung(ctx context.Context, p llm.Provider, text string) (string, bool, error) {
	roh, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierFast,
		MaxTokens:  SprechMaxTokens,
		NoThinking: true,
		System:     sprechSystem,
		Messages:   []llm.Message{{Role: "user", Content: "The message:\n" + kuerzen(text, 6000)}},
	})
	if err != nil {
		return "", false, err
	}
	s := strings.TrimSpace(roh)
	if i := strings.Index(s, "{"); i > 0 {
		s = s[i:]
	}
	if j := strings.LastIndex(s, "}"); j >= 0 {
		s = s[:j+1]
	}
	var out struct {
		Spoken  string `json:"spoken"`
		Details bool   `json:"details_in_chat"`
	}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return "", false, fmt.Errorf("spoken form is not JSON: %w", err)
	}
	spoken := Sprechbar(out.Spoken)
	if spoken == "" {
		return "", false, fmt.Errorf("spoken form: empty")
	}
	return spoken, out.Details, nil
}
