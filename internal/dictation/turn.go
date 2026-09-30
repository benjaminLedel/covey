package dictation

import (
	"context"
	"strings"
	"unicode"
	"unicode/utf8"

	"covey/internal/llm"
	"covey/internal/speech/normalise"
)

/* A turn said in a call (#498, #511) is cleaned with a lighter hand than a
 * dictation. A dictation is a text the person will read before it goes out;
 * a turn goes into the conversation at once, and the agent answers what it
 * says. In a German call a five-letter turn came back as "Ja.", and the
 * agent answered a yes nobody had said; other turns came back in English.
 * So a turn:
 *
 *   - under MinTurnWords is not cleaned at all — there is nothing to tidy in
 *     "Hallo", and the model had nothing to go on but the conversation;
 *   - is only corrected: spelling, punctuation, names and technical terms,
 *     every word kept, in its language;
 *   - comes back as recognised when the answer is in another language, or
 *     changed more words than a correction does (MaxTurnEdit). */

// MinTurnWords: a turn with fewer words is sent as recognised.
const MinTurnWords = 4

// MaxTurnEdit bounds how much of a turn the clean-up may change, as the
// share of its words (TurnEdit).
const MaxTurnEdit = 0.3

// Why a turn was sent as recognised.
const (
	KeptShort    = "short"
	KeptLanguage = "language"
	KeptEdited   = "edited"
)

const turnSystem = `You receive one turn a person said in a call, as a speech recogniser transcribed it. Correct the transcription; do not rewrite it.
- Fix only what the recogniser got wrong: spelling, punctuation, capitalisation, and names and technical terms it misheard — use the names and the conversation given for that.
- Keep every word the person said, in their order. Do not drop, add, merge or reword anything, do not answer or complete the turn, and do not replace it with what the conversation suggests they meant. If a word is unclear, keep what was recognised.
- Keep the input's language. Never translate: a German turn stays German, an English one English, whatever the language of the conversation, of the names or of these instructions.
- The text before the insertion point is the conversation so far. It helps you spell; it is never part of your answer.
Output only the corrected turn, without quotes, comments or a preamble.`

// ShortTurn says whether a turn is too short to be cleaned.
func ShortTurn(text string) bool { return len(words(text)) < MinTurnWords }

// CleanTurn returns a turn said in a call as corrected, and — when it is
// the turn as recognised — why (KeptShort, KeptLanguage, KeptEdited).
func CleanTurn(ctx context.Context, p llm.Provider, text string, where Context) (string, string, error) {
	if ShortTurn(text) {
		return text, KeptShort, nil
	}
	out, err := complete(ctx, p, turnSystem, text, "", where)
	if err != nil {
		return "", "", err
	}
	if !normalise.SameLanguage(text, out) {
		return text, KeptLanguage, nil
	}
	if TurnEdit(text, out) > MaxTurnEdit {
		return text, KeptEdited, nil
	}
	return out, "", nil
}

// words are a text's words in lower case, without punctuation: what a
// correction of punctuation and capitalisation does not change.
func words(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
}

// TurnEdit is how much of a turn the clean-up changed: the word-level edit
// distance between raw and cleaned, as a share of the raw turn's words. A
// word replaced by one spelled close to it (a misheard name, a term) counts
// half; a word dropped, added or replaced by another counts whole.
func TurnEdit(raw, cleaned string) float64 {
	a, b := words(raw), words(cleaned)
	if len(a) == 0 {
		if len(b) == 0 {
			return 0
		}
		return 1
	}
	prev := make([]float64, len(b)+1)
	cur := make([]float64, len(b)+1)
	for j := range prev {
		prev[j] = float64(j)
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = float64(i)
		for j := 1; j <= len(b); j++ {
			sub := prev[j-1]
			if a[i-1] != b[j-1] {
				sub += substitution(a[i-1], b[j-1])
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, sub)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)] / float64(len(a))
}

// substitution is the cost of replacing word a by b: half for a spelling
// fix (at most 40 % of the longer word's letters differ), whole otherwise.
func substitution(a, b string) float64 {
	n := max(utf8.RuneCountInString(a), utf8.RuneCountInString(b))
	if float64(letters(a, b)) <= 0.4*float64(n) {
		return 0.5
	}
	return 1
}

// letters is the edit distance between two words, in letters.
func letters(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	cur := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			c := 1
			if ra[i-1] == rb[j-1] {
				c = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+c)
		}
		prev, cur = cur, prev
	}
	return prev[len(rb)]
}
