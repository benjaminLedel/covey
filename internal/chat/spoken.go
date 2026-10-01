package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
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
	// MetaEndCall = "true" marks the agent's goodbye to a person who closed
	// the call (#517): the call speaks it and hangs up.
	MetaEndCall = "end_call"
)

// SaidInCall says whether a person's message was said aloud in a call.
func SaidInCall(m Message) bool { return m.Meta[ViaMeta] == ViaCall }

// SpokenMax bounds a spoken form: two short sentences are well under it.
// What is longer is cut at its last sentence end within the bound — a model
// that wrote a paragraph for the ear still must not keep a caller listening.
const SpokenMax = 240

// SpokenSentences is how many sentences a spoken form keeps (#511): in a
// call, a third sentence was an offer nobody asked for or a second topic.
const SpokenSentences = 2

// Sprechbar is a spoken form as it is stored: on one line, without the
// markdown a model may still have put in, at most SpokenSentences
// sentences, and bounded by SpokenMax.
func Sprechbar(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	if i := sentenceEnd(s, SpokenSentences); i > 0 {
		s = strings.TrimSpace(s[:i])
	}
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

// sentenceEnd is the byte offset after the n-th sentence of s when more
// follows, else 0. A sentence ends at . ! ? or … before a space and a word
// that starts with a capital or no letter; "z. B." and "d. h." end none.
func sentenceEnd(s string, n int) int {
	count := 0
	for i, r := range s {
		if !strings.ContainsRune(".!?…", r) {
			continue
		}
		after := i + utf8.RuneLen(r)
		rest := s[after:]
		if !strings.HasPrefix(rest, " ") {
			continue
		}
		next, _ := utf8.DecodeRuneInString(rest[1:])
		if unicode.IsLower(next) {
			continue
		}
		if r == '.' {
			word := s[strings.LastIndex(s[:i], " ")+1 : i]
			if utf8.RuneCountInString(word) == 1 && unicode.IsLower([]rune(word)[0]) {
				continue
			}
		}
		if count++; count == n {
			return after
		}
	}
	return 0
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
const SpokenRules = `- at most two short sentences, as you would say them on the phone, that answer what was asked and nothing else: no preamble, no second topic, no offer of more help — whatever is left over stays in the written form;
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
	triageBeispielEN = `For example, written: "Done — PROJ-12, MR !88, pipeline green, waiting for Ada's review." Spoken: "Yes, it's in. The merge request is ready, the tests are green, and it's waiting for Ada's review." — {"action":"answer","spoken":"Yes, it's in. The merge request is ready, the tests are green, and it's waiting for Ada's review.","text":"Done — PROJ-12, MR !88, pipeline green, waiting for Ada's review.","details_in_chat":true}`
	triageBeispielDE = `For example, in a German conversation, written: "Ist drin — PROJ-12, MR !88, Pipeline grün, wartet auf Adas Review." Spoken: "Ja, ist drin. Der Merge Request ist fertig, die Tests sind grün, und er wartet auf Adas Review." — {"action":"answer","spoken":"Ja, ist drin. Der Merge Request ist fertig, die Tests sind grün, und er wartet auf Adas Review.","text":"Ist drin — PROJ-12, MR !88, Pipeline grün, wartet auf Adas Review.","details_in_chat":true}`
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
Write "spoken" right after "action", before every other field: it is read out to the person while you write the rest.
` + beispiel + `
` + triageSchluss
}

/* triageSchluss is the closing of a call (#517): the person says goodbye and
 * the agent hangs up after its own, instead of leaving the line open until
 * they press the button. The line is narrow on purpose — a thank-you in the
 * middle of a request is no goodbye, and a call ended too early costs more
 * than one the person has to hang up themselves. */
const triageSchluss = `Add "end_call": true only when the person is ending the call: they take their leave or say they are done ("Danke, das war's", "Tschüss", "Bis später", "Danke, schönen Tag noch", "That's all, bye", "Thanks, talk later") and want nothing more from you now. Then "text" and "spoken" are a short goodbye in one sentence, as a colleague says it on the phone ("Gern, bis später!", "Anytime — bye!"). If they hand you a job while they take their leave, it is a "task" as ever, and its acknowledgement is the goodbye and says you will report back in the chat ("Mach ich, ich meld mich im Chat. Tschüss!"). A thank-you with a request or a question after it ("Danke, und kannst du noch …", "Thanks — one more thing …"), a question, or anything they want answered now is not an ending: leave "end_call" out. When in doubt, leave it out — the person can still hang up themselves.`

// endetAnruf says whether a decision may close the call it was said in
// (#517): only an answer, a note's reply or a task's acknowledgement — a
// search says nothing, a config proposal waits for somebody — and never an
// answer to a message that asks something: a question wants its answer, not
// a goodbye.
func endetAnruf(e Entscheidung, nachricht string) bool {
	/* The person has to have said goodbye in words (#521): a misheard
	   short turn ("Tandu.") once closed a call because the model read it
	   as a farewell. */
	if !SaysGoodbye(nachricht) {
		return false
	}
	switch e.Aktion {
	case AktionAntwort:
		return !strings.ContainsAny(nachricht, "?¿？")
	case AktionNotiz, AktionAufgabe:
		return true
	}
	return false
}

// fuerAnruf keeps the spoken form of a decision only where there is a call:
// a written message gets none, whatever the model returned, and a search
// says nothing. A spoken form in another language than what it says aloud
// is dropped (#511); lang is the conversation's language, for a written
// answer too short to tell.
//
// The closing of the call (#517) stands only where endetAnruf allows it for
// nachricht, the message the decision answers.
func fuerAnruf(e Entscheidung, anruf bool, lang, nachricht string) Entscheidung {
	if !anruf || e.Aktion == AktionSuche {
		e.Gesprochen, e.DetailsImChat, e.Schluss = "", false, false
		return e
	}
	e.Schluss = e.Schluss && endetAnruf(e, nachricht)
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
// With the closing of a call (#517) it carries MetaEndCall.
func (e Entscheidung) AnswerMeta() map[string]string {
	return WithEndCall(WithSpoken(e.Meta, e.Gesprochen, e.DetailsImChat), e.Schluss)
}

// WithEndCall is meta marked as the goodbye that ends a call (#517): a copy,
// as WithSpoken makes one. Without end it is meta unchanged.
func WithEndCall(meta map[string]string, end bool) map[string]string {
	if !end {
		return meta
	}
	out := make(map[string]string, len(meta)+1)
	for k, v := range meta {
		out[k] = v
	}
	out[MetaEndCall] = "true"
	return out
}

// spokenTag is the trailing section a chat answer's run writes in a call
// (agents.ChatAnswerCallDoc): its attributes, and what is inside.
var (
	spokenTag  = regexp.MustCompile(`(?is)\s*<spoken((?:\s+[a-z_]+\s*=\s*["']?[a-z]*["']?)*)\s*>(.*?)</spoken>\s*`)
	spokenAttr = regexp.MustCompile(`(?i)([a-z_]+)\s*=\s*["']?([a-z]*)["']?`)
)

// SplitSpoken takes the spoken form out of a run's result: the written reply
// without the tag, the spoken form, and whether it left details for the chat.
// A result without the tag is returned as it stands; a spoken form in another
// language than the reply is dropped (#511).
func SplitSpoken(result string) (written, spoken string, details bool) {
	written, spoken, details, _ = SplitSpokenCall(result)
	return written, spoken, details
}

// SplitSpokenCall is SplitSpoken with the tag's end_call="true" (#517): the
// reply is the goodbye to a person who closed the call. It stands only with
// a spoken form — a goodbye nobody hears ends nothing.
func SplitSpokenCall(result string) (written, spoken string, details, end bool) {
	m := spokenTag.FindStringSubmatchIndex(result)
	if m == nil {
		return result, "", false, false
	}
	spoken = Sprechbar(result[m[4]:m[5]])
	if m[2] >= 0 {
		for _, a := range spokenAttr.FindAllStringSubmatch(result[m[2]:m[3]], -1) {
			on := strings.EqualFold(a[2], "true")
			switch strings.ToLower(a[1]) {
			case "details_in_chat":
				details = on
			case "end_call":
				end = on
			}
		}
	}
	written = strings.TrimSpace(result[:m[0]] + "\n\n" + result[m[1]:])
	// In another language than the reply (#511): not said.
	if !SpokenFits(written, spoken, "") {
		spoken = ""
	}
	if spoken == "" {
		details, end = false, false
	}
	return written, spoken, details, end
}

// SprechMaxTokens: a JSON object with two short sentences in it.
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

// farewells are the words a person closes a call with, lower-case and
// without punctuation, matched as whole words or phrases (#521).
var farewells = []string{
	// German
	"tschüss", "tschüs", "tschau", "ciao", "servus", "ade", "adieu",
	"bis später", "bis dann", "bis morgen", "bis bald", "bis gleich", "bis nachher",
	"auf wiederhören", "auf wiedersehen", "mach's gut", "machs gut", "macht's gut",
	"schönen abend", "schönen tag", "schönes wochenende", "gute nacht", "schönen feierabend",
	"das war's", "das wars", "das wär's", "das wärs", "das war alles", "das wäre alles",
	"leg auf", "ich leg auf", "ich lege auf",
	// English
	"bye", "goodbye", "good bye", "bye bye", "see you", "see ya", "talk later", "talk to you later",
	"that's all", "thats all", "that's it", "thats it", "have a good", "have a nice", "take care",
	"i'll hang up", "hanging up", "cheers",
}

// SaysGoodbye reports whether text contains a farewell (#521): the only
// words a call may be closed on.
func SaysGoodbye(text string) bool {
	t := " " + strings.Map(func(r rune) rune {
		switch {
		case r == '\'' || r == '’':
			return '\''
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			return unicode.ToLower(r)
		}
		return ' '
	}, text) + " "
	t = strings.Join(strings.Fields(t), " ")
	t = " " + t + " "
	for _, f := range farewells {
		if strings.Contains(t, " "+f+" ") {
			return true
		}
	}
	return false
}
