package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"covey/internal/llm"
	"covey/internal/speech/normalise"
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

/* The language of the spoken form (#511). In a German call the spoken forms
 * came out in English: "Hi! Bin gerade bei den Reviews – schau mir die MRs
 * an und warte auf die Pipelines." was spoken as "Hi! I'm looking at the
 * merge requests right now and waiting for the pipelines to finish." The
 * instruction and its example were English and named no language, and a
 * model followed the language of the prompt. So the prompt now says the
 * language, and a spoken form in another language than its written answer
 * is dropped (SpokenFits): the call then speaks the written answer, which is
 * in the right language. */

// languageNames are the languages normalise.Language tells, by name.
var languageNames = map[string]string{
	"de": "German", "en": "English", "fr": "French", "es": "Spanish", "it": "Italian",
	"nl": "Dutch", "pt": "Portuguese", "pl": "Polish", "ja": "Japanese", "zh": "Chinese",
}

// baseLanguage is the base of a BCP 47 tag in lower case: "de-AT" is "de".
func baseLanguage(tag string) string {
	b, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	b, _, _ = strings.Cut(b, "_")
	return b
}

// spokenLanguageRule is the line that names the spoken form's language:
// the written answer's, and — when it is known which that will be — by name.
func spokenLanguageRule(lang, written string) string {
	rule := "Write the spoken form in the same language as the " + written + " — never translate it"
	if name := languageNames[baseLanguage(lang)]; name != "" {
		rule += "; here that is " + name + ", the language of the conversation"
	}
	return rule + "."
}

// SpokenFits says whether a spoken form may stand beside its written
// answer: false only when it is known to be in another language than the
// written one — or, when the written answer is too short to tell ("👍",
// "Okay."), than hint, the conversation's language.
func SpokenFits(written, spoken, hint string) bool {
	ls := normalise.Language(spoken)
	if ls == "" {
		return true
	}
	lw := normalise.Language(written)
	if lw == "" {
		lw = baseLanguage(hint)
	}
	return lw == "" || lw == ls
}

// conversationLanguage is the language a triage turn names for the spoken
// form: the message's, and when the message is too short to tell, the
// conversation's. "" when neither tells.
func conversationLanguage(nachricht string, verlauf []Beitrag) string {
	if l := normalise.Language(nachricht); l != "" {
		return l
	}
	var b strings.Builder
	for i := len(verlauf) - 1; i >= 0 && i >= len(verlauf)-6; i-- {
		b.WriteString(verlauf[i].Text)
		b.WriteString("\n")
	}
	return normalise.Language(b.String())
}

// Examples of a spoken form beside its written answer, in the triage's
// JSON: the German one for a German conversation, the English one for
// every other.
const (
	triageBeispielEN = `For example, written: "Done — PROJ-12, MR !88, pipeline green, waiting for Ada's review." Spoken: "Yes, it's in. The merge request is ready, the tests are green, and it's waiting for Ada's review." — {"action":"answer","text":"Done — PROJ-12, MR !88, pipeline green, waiting for Ada's review.","spoken":"Yes, it's in. The merge request is ready, the tests are green, and it's waiting for Ada's review.","details_in_chat":true}`
	triageBeispielDE = `For example, in a German conversation, written: "Ist drin — PROJ-12, MR !88, Pipeline grün, wartet auf Adas Review." Spoken: "Ja, ist drin. Der Merge Request ist fertig, die Tests sind grün, und er wartet auf Adas Review." — {"action":"answer","text":"Ist drin — PROJ-12, MR !88, Pipeline grün, wartet auf Adas Review.","spoken":"Ja, ist drin. Der Merge Request ist fertig, die Tests sind grün, und er wartet auf Adas Review.","details_in_chat":true}`
)

// triageAnruf is appended to the triage's system prompt when the message
// was said in a call, and only then; lang is the conversation's language
// (conversationLanguage), or empty.
func triageAnruf(lang string) string {
	beispiel := triageBeispielEN
	if baseLanguage(lang) == "de" {
		beispiel = triageBeispielDE
	}
	return `

The new message was said aloud in a call, not typed: the person is listening, and what you say is read out to them by a voice. Everything above stays as it is — the written form is what the conversation shows. Add "spoken": the same thing said for the ear:
` + SpokenRules + `
` + spokenLanguageRule(lang, "written form") + `
Add "details_in_chat": true when the spoken form leaves out something the written one carries (an id, a link, a list, a figure), false when it says all of it.
"spoken" is what the person hears: for "answer" it says "text", for "note" it says "reply", for "task" the acknowledgement in "text", for "config" the "text" (write ` + ApproversPlatzhalter + ` there too). A "search" needs none.
` + beispiel
}

// fuerAnruf keeps the spoken form of a decision only where there is a call:
// a written message gets none, whatever the model returned, and a search
// says nothing. A spoken form in another language than what it says aloud
// is dropped (#511); lang is the conversation's language, for a written
// answer too short to tell.
func fuerAnruf(e Entscheidung, anruf bool, lang string) Entscheidung {
	if !anruf || e.Aktion == AktionSuche {
		e.Gesprochen, e.DetailsImChat = "", false
		return e
	}
	e.Gesprochen = Sprechbar(e.Gesprochen)
	geschrieben := e.Text
	if e.Aktion == AktionNotiz {
		geschrieben = e.Antwort
	}
	if !SpokenFits(geschrieben, e.Gesprochen, lang) {
		e.Gesprochen = ""
	}
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
// A result without the tag is returned as it stands; a spoken form in another
// language than the reply is dropped (#511).
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
	// In another language than the reply (#511): not said.
	if !SpokenFits(written, spoken, "") {
		spoken = ""
	}
	if spoken == "" {
		details = false
	}
	return written, spoken, details
}

// SprechMaxTokens: a JSON object with three short sentences in it.
const SprechMaxTokens = 300

const sprechSystem = `You are part of covey, a platform that runs AI agents as employees. An AI colleague is in a call with a person, and a message of theirs has just been posted into the conversation. A voice reads it out. Write what the voice says — the same message for the ear:
` + SpokenRules + `
%s

Answer with ONE JSON object and nothing else: {"spoken":"…","details_in_chat":true} — "details_in_chat" is true when the spoken form leaves out something the message carries (an id, a link, a list, a figure), false when it says all of it.`

// Sprechfassung is the spoken form of a message that was written without
// one — a task's result posted while the call it came from may still be on.
// One turn on the fast tier; an error leaves the message to be spoken as it
// is written — so does a spoken form in another language than the message
// (#511).
func Sprechfassung(ctx context.Context, p llm.Provider, text string) (string, bool, error) {
	lang := normalise.Language(text)
	roh, err := p.Complete(ctx, llm.Request{
		Tier:       llm.TierFast,
		MaxTokens:  SprechMaxTokens,
		NoThinking: true,
		System:     fmt.Sprintf(sprechSystem, spokenLanguageRule(lang, "message")),
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
	if !SpokenFits(text, spoken, "") {
		return "", false, fmt.Errorf("spoken form: in %s, the message in %s", normalise.Language(spoken), lang)
	}
	return spoken, out.Details, nil
}
