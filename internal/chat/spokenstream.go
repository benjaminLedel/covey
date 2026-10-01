package chat

import (
	"strings"
	"unicode/utf8"

	"covey/internal/speech/normalise"
)

/* The spoken form as the model writes it (#529).
 *
 * In a call the triage's turn is streamed, and the person should hear the
 * first sentence of the spoken form while the model still writes the rest
 * of its JSON. SpokenStream reads the decision as it grows: the action, and
 * the "spoken" string, decoded as far as it has come, cut into sentences
 * where sentenceEnd cuts the stored one. A sentence is handed over once the
 * next has begun — only then is its end certain — and the last when the
 * string closes. At most SpokenSentences, as Sprechbar keeps.
 *
 * Only an answer, a note's reply and a task's acknowledgement are spoken as
 * they come: a config proposal's text carries a placeholder covey fills in
 * afterwards, and a search says nothing. The action is written first, so it
 * is known before the spoken form starts.
 */

// SpokenStream hands over the sentences of a streamed decision's spoken
// form to Emit. Feed it the model's text piece by piece.
type SpokenStream struct {
	// Emit gets each sentence, in order, as Sprechbar would store it. It may
	// return false to stop the stream: nothing more is handed over.
	Emit func(sentence string) bool
	// End, where set, is told once that the spoken form is complete — its
	// string closed, or as much of it handed over as is kept — when Emit
	// took at least one sentence (#533).
	End func()

	text      strings.Builder
	delivered int  // sentences Emit took
	ended     bool // End told
	sent      int  // sentences handed over
	runes     int  // and their length
	offset    int  // bytes of the decoded spoken form handed over
	stopped   bool
}

// Feed adds a piece of the model's text and hands over what became
// complete with it.
func (s *SpokenStream) Feed(piece string) {
	if s.stopped {
		return
	}
	s.text.WriteString(piece)
	action, spoken, closed := scanDecision(s.text.String())
	switch Aktion(action) {
	case "":
		return
	case AktionAntwort, AktionNotiz, AktionAufgabe:
	default:
		s.stopped = true
		return
	}
	// Line breaks are spaces, as Sprechbar makes them — byte for byte, so
	// the offsets hold as the text grows.
	spoken = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, spoken)
	for !s.stopped {
		if s.sent >= SpokenSentences {
			s.finish()
			return
		}
		rest := spoken[s.offset:]
		end := sentenceEnd(rest, 1)
		// Its end is certain only once the word after it has begun: "z. B."
		// is no end, and that is known at the "ü" of "z. B. über".
		if end > 0 && !closed && !utf8.FullRuneInString(rest[min(end+1, len(rest)):]) {
			return
		}
		if end == 0 {
			if !closed {
				return
			}
			if end = len(rest); end == 0 {
				s.finish()
				return
			}
		}
		s.offset += end
		sentence := Sprechbar(rest[:end])
		if sentence == "" {
			continue
		}
		// Within the bound of the whole, as Sprechbar keeps it.
		n := utf8.RuneCountInString(sentence)
		if s.runes+n > SpokenMax {
			s.finish()
			return
		}
		s.sent++
		s.runes += n
		if !s.Emit(sentence) {
			// Refused: nothing more, and no end to tell.
			s.stopped = true
			return
		}
		s.delivered++
	}
}

// finish stops the stream at the end of the spoken form, and tells End.
func (s *SpokenStream) finish() {
	s.stopped = true
	if s.delivered > 0 && !s.ended && s.End != nil {
		s.ended = true
		s.End()
	}
}

// Spoken is how much of the spoken form was handed over.
func (s *SpokenStream) Spoken() int { return s.sent }

// gesprochenAlsStrom is the stream of a call's triage turn: its sentences
// go to emit, unless the first is known to be in another language than the
// conversation's — fuerAnruf would drop that spoken form (#511), and the
// written answer is spoken instead, once it is there.
func gesprochenAlsStrom(emit func(string) bool, end func(), lang string) *SpokenStream {
	s := &SpokenStream{End: end}
	s.Emit = func(satz string) bool {
		if s.Spoken() == 0 {
			if l := normalise.Language(satz); l != "" && lang != "" && l != baseLanguage(lang) {
				return false
			}
		}
		return emit(satz)
	}
	return s
}

// scanDecision reads a decision's JSON as far as it has come: the value of
// "action" once it is complete, the "spoken" string decoded as far as it
// goes, and whether that string has closed. Only the object's own keys
// count — a "spoken" inside another string is text.
func scanDecision(raw string) (action, spoken string, closed bool) {
	i := strings.IndexByte(raw, '{')
	if i < 0 {
		return "", "", false
	}
	depth := 0
	expectKey := false
	key := ""
	var cur strings.Builder
	in := false
	isKey := false
	for i < len(raw) {
		c := raw[i]
		if !in {
			switch c {
			case '{', '[':
				depth++
				expectKey = c == '{' && depth == 1
			case '}', ']':
				depth--
				if depth <= 0 {
					return action, spoken, closed
				}
			case ',':
				expectKey = depth == 1
			case ':':
				expectKey = false
			case '"':
				in = true
				isKey = depth == 1 && expectKey
				cur.Reset()
			}
			i++
			continue
		}
		// Inside a string.
		if c == '"' {
			in = false
			v := cur.String()
			switch {
			case isKey:
				key = v
				expectKey = false
			case depth == 1 && key == "action":
				action = v
			case depth == 1 && key == "spoken":
				return action, v, true
			}
			i++
			continue
		}
		if c != '\\' {
			// A character cut by the end of what has come waits for the rest.
			if !utf8.FullRuneInString(raw[i:]) {
				break
			}
			_, n := utf8.DecodeRuneInString(raw[i:])
			cur.WriteString(raw[i : i+n])
			i += n
			continue
		}
		// An escape, complete or still to come.
		r, n, ok := unescape(raw[i:])
		if !ok {
			break
		}
		cur.WriteRune(r)
		i += n
	}
	if in && !isKey && depth == 1 && key == "spoken" {
		return action, cur.String(), false
	}
	return action, "", false
}

// unescape decodes the JSON escape at the start of s: the rune, the bytes
// it took, and false when s ends before the escape does.
func unescape(s string) (rune, int, bool) {
	if len(s) < 2 {
		return 0, 0, false
	}
	switch s[1] {
	case '"', '\\', '/':
		return rune(s[1]), 2, true
	case 'b':
		return '\b', 2, true
	case 'f':
		return '\f', 2, true
	case 'n':
		return '\n', 2, true
	case 'r':
		return '\r', 2, true
	case 't':
		return '\t', 2, true
	case 'u':
		if len(s) < 6 {
			return 0, 0, false
		}
		var r rune
		for _, h := range s[2:6] {
			r <<= 4
			switch {
			case h >= '0' && h <= '9':
				r |= h - '0'
			case h >= 'a' && h <= 'f':
				r |= h - 'a' + 10
			case h >= 'A' && h <= 'F':
				r |= h - 'A' + 10
			default:
				return utf8.RuneError, 6, true
			}
		}
		// A surrogate pair: the second half follows as its own escape.
		if r >= 0xD800 && r < 0xDC00 {
			if len(s) < 12 {
				return 0, 0, false
			}
			if lo, n, ok := unescape(s[6:]); ok && n == 6 && lo >= 0xDC00 && lo < 0xE000 {
				return (r-0xD800)<<10 + (lo - 0xDC00) + 0x10000, 12, true
			}
			return utf8.RuneError, 6, true
		}
		return r, 6, true
	}
	return utf8.RuneError, 2, true
}
