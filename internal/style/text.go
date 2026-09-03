// Package style measures a text against a style profile: how concrete it is
// (anchors per paragraph, nominalisations, light verbs, hedges), how it reads
// (sentence and paragraph shape, clause depth) and how it sounds (address,
// punctuation), each against the band an author's own corpus set. It is the Go
// counterpart of the covey-style skill's scripts and reads the same
// `covey-style/1` profile; the two are kept in step by a parity test over the
// skill's fixtures (testdata/python_metrics.json).
//
// Every metric is a heuristic over surface form: there is no tagger and no
// parser. The numbers are for comparing a draft with a corpus measured the same
// way, which is the only use the style gate puts them to.
package style

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Schema is the profile schema this package reads and writes.
const Schema = "covey-style/1"

// protect stands in for a period that must not end a sentence (ONE DOT LEADER).
const protect = '․'

var (
	reFrontMatter = regexp.MustCompile(`(?s)\A---\n.*?\n---\n`)
	reHeading     = regexp.MustCompile(`(?m)^(#{1,6})\s+(.*?)\s*#*\s*$`)
	reInlineCode  = regexp.MustCompile("`[^`\n]+`")
	reLink        = regexp.MustCompile(`!?\[([^\]]*)\]\([^)]*\)`)
	reHTML        = regexp.MustCompile(`<[^>\n]+>`)
	reTableLine   = regexp.MustCompile(`(?m)^\s*\|.*\|\s*$`)
	reListMark    = regexp.MustCompile(`(?m)^\s*(?:[-*+]|\d+[.)])\s+`)
	reQuoteMark   = regexp.MustCompile(`(?m)^\s*>\s?`)
	// Emphasis: the shortest span from a marker to the same marker, starting
	// and ending on a non-space. The lazy optional group (??) matters: with a
	// greedy one, "a**.** b**.**" would be one span from the first to the last.
	reEmphasis = []*regexp.Regexp{
		regexp.MustCompile(`\*\*(\S(?:.*?\S)??)\*\*`),
		regexp.MustCompile(`__(\S(?:.*?\S)??)__`),
		regexp.MustCompile(`\*(\S(?:.*?\S)??)\*`),
		regexp.MustCompile(`_(\S(?:.*?\S)??)_`),
	}
	reParaSplit   = regexp.MustCompile(`\n\s*\n`)
	reSpaces      = regexp.MustCompile(`\s+`)
	reWord        = regexp.MustCompile(`\pL+(?:[-'’]\pL+)*`)
	reSentenceCut = regexp.MustCompile(`[.!?]["“”»)]?\s+["„«(]?[A-ZÄÖÜ0-9]`)
	reAbbrevCand  = regexp.MustCompile(`[\pL\pN_.]{1,5}\.`)
	reEllipsis    = regexp.MustCompile(`\.{3}|…`)
	reMonthDot    = regexp.MustCompile(`(\d)\.(\s+(?:` + strings.Join(monthsDE, "|") + `)\b)`)
)

// stripMarkdown returns the prose of a Markdown text with paragraph breaks
// kept, the headings set aside (for the colon check) and the number of fenced
// code blocks. Inline code becomes the token CODE for measuring (a path is not
// a fourteen-letter word); keepInlineCode leaves it in place for reading.
func stripMarkdown(text string, keepInlineCode bool) (prose string, headings []string, codeBlocks int) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = reFrontMatter.ReplaceAllString(text, "")
	text, codeBlocks = stripFences(text)
	for _, m := range reHeading.FindAllStringSubmatch(text, -1) {
		headings = append(headings, m[2])
	}
	text = reHeading.ReplaceAllString(text, "\n\n")
	text = reTableLine.ReplaceAllString(text, "")
	if !keepInlineCode {
		text = reInlineCode.ReplaceAllString(text, " CODE ")
	}
	text = reLink.ReplaceAllString(text, "$1")
	text = reHTML.ReplaceAllString(text, " ")
	text = reQuoteMark.ReplaceAllString(text, "")
	// Every list item becomes a paragraph of its own; otherwise a list without
	// blank lines between its items measures as one paragraph and one sentence.
	text = reListMark.ReplaceAllString(text, "\n\n")
	for _, re := range reEmphasis {
		text = re.ReplaceAllString(text, "$1")
	}
	return text, headings, codeBlocks
}

// stripFences removes ``` and ~~~ blocks (opening and closing marker at a line
// start, the same marker for both) and counts them. An unclosed fence stays.
func stripFences(text string) (string, int) {
	lines := strings.Split(text, "\n")
	var out []string
	count := 0
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		marker := ""
		if strings.HasPrefix(line, "```") {
			marker = "```"
		} else if strings.HasPrefix(line, "~~~") {
			marker = "~~~"
		}
		if marker == "" {
			out = append(out, line)
			continue
		}
		closing := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.HasPrefix(lines[j], marker) && strings.TrimSpace(lines[j][len(marker):]) == "" {
				closing = j
				break
			}
		}
		if closing < 0 {
			out = append(out, line)
			continue
		}
		count++
		out = append(out, "", "")
		i = closing
	}
	return strings.Join(out, "\n"), count
}

// splitParagraphs cuts prose at blank lines; a paragraph needs three words.
func splitParagraphs(prose string) []string {
	var out []string
	for _, p := range reParaSplit.Split(prose, -1) {
		p = strings.TrimSpace(reSpaces.ReplaceAllString(p, " "))
		if len(wordsOf(p)) >= 3 {
			out = append(out, p)
		}
	}
	return out
}

// wordsOf returns the letter runs of a string; digits and symbols are not words.
func wordsOf(s string) []string {
	return reWord.FindAllString(s, -1)
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// prevRune returns the rune before byte offset i (utf8.RuneError at the start).
func prevRune(s string, i int) rune {
	if i <= 0 {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeLastRuneInString(s[:i])
	return r
}

func nextRune(s string, i int) rune {
	if i >= len(s) {
		return utf8.RuneError
	}
	r, _ := utf8.DecodeRuneInString(s[i:])
	return r
}

// wordStartAt: a word boundary sits before byte offset i and a word starts there.
func wordStartAt(s string, i int) bool {
	return isWordRune(nextRune(s, i)) && !isWordRune(prevRune(s, i))
}

// wordEndAt: a word boundary sits at byte offset i and a word ends there.
func wordEndAt(s string, i int) bool {
	return isWordRune(prevRune(s, i)) && !isWordRune(nextRune(s, i))
}

// protectPeriods swaps the periods that do not end a sentence for the protect
// rune: known abbreviations, decimals, ordinals before a month, letter-dot
// sequences (U.S., z. B.), ellipses.
func protectPeriods(s string) string {
	// Abbreviations: a short token ending in a period, at a word start.
	var b strings.Builder
	last := 0
	for _, m := range reAbbrevCand.FindAllStringIndex(s, -1) {
		if isWordRune(prevRune(s, m[0])) {
			continue
		}
		token := s[m[0]:m[1]]
		key := strings.ToLower(token[:len(token)-1])
		if !abbreviations[key] {
			continue
		}
		b.WriteString(s[last:m[0]])
		b.WriteString(strings.ReplaceAll(token, ".", string(protect)))
		last = m[1]
	}
	b.WriteString(s[last:])
	s = b.String()

	// Decimals and thousands: a period between two digits.
	rs := []rune(s)
	for i := 1; i+1 < len(rs); i++ {
		if rs[i] == '.' && unicode.IsDigit(rs[i-1]) && unicode.IsDigit(rs[i+1]) {
			rs[i] = protect
		}
	}
	s = string(rs)

	// Ordinals before a month or a counted noun: "3. Mai".
	s = reMonthDot.ReplaceAllString(s, "${1}"+string(protect)+"${2}")

	// Single letters with periods: U.S., z. B. — a period after a one-letter
	// word when another one-letter word with a period follows.
	rs = []rune(s)
	for i := 1; i < len(rs); i++ {
		if rs[i] != '.' || !isLatinLetter(rs[i-1]) || (i >= 2 && isWordRune(rs[i-2])) {
			continue
		}
		j := i + 1
		if j < len(rs) && unicode.IsSpace(rs[j]) {
			j++
		}
		if j+1 < len(rs) && isLatinLetter(rs[j]) && rs[j+1] == '.' {
			rs[i] = protect
		}
	}
	s = string(rs)

	return reEllipsis.ReplaceAllString(s, " "+strings.Repeat(string(protect), 3)+" ")
}

func isLatinLetter(r rune) bool {
	return (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || strings.ContainsRune("ÄÖÜ", r)
}

// splitSentences cuts a paragraph at sentence punctuation followed by white
// space and a capital or digit. A closing quote directly after the punctuation
// goes with the separator, as in the reference implementation.
func splitSentences(paragraph string) []string {
	s := protectPeriods(paragraph)
	var pieces []string
	last := 0
	for _, m := range reSentenceCut.FindAllStringIndex(s, -1) {
		// m[0] is the punctuation; the piece ends right after it.
		end := m[0] + 1
		// The separator runs to the start of the optional opening quote / letter:
		// the match ends after the first character of the next sentence.
		_, size := utf8.DecodeLastRuneInString(s[:m[1]])
		start := m[1] - size
		if r := prevRune(s, start); strings.ContainsRune(`"„«(`, r) {
			start -= utf8.RuneLen(r)
		}
		pieces = append(pieces, s[last:end])
		last = start
	}
	pieces = append(pieces, s[last:])

	var out []string
	for _, p := range pieces {
		p = strings.TrimSpace(strings.ReplaceAll(p, string(protect), "."))
		if len(wordsOf(p)) >= 2 {
			out = append(out, p)
		}
	}
	return out
}

// DetectLanguage says whether a text reads as German or English ("de"/"en").
func DetectLanguage(text string) string {
	prose, _, _ := stripMarkdown(text, false)
	return detectLanguage(wordsOf(prose))
}

// detectLanguage counts stopwords of the first 4000 words.
func detectLanguage(words []string) string {
	if len(words) > 4000 {
		words = words[:4000]
	}
	de, en := 0, 0
	for _, w := range words {
		lw := strings.ToLower(w)
		if stopwordsDE[lw] {
			de++
		}
		if stopwordsEN[lw] {
			en++
		}
	}
	if en > de {
		return "en"
	}
	return "de"
}
