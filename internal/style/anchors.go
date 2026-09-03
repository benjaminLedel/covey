package style

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Anchor is one concrete thing a paragraph holds on to: a number, a name, a
// quote, an example marker, a link.
type Anchor struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

var (
	reURL        = regexp.MustCompile(`(?:https?://|www\.)\S+|\b[\w.-]+@[\w.-]+\.\w+\b`)
	reNumberCore = regexp.MustCompile(`\d[\d.,:]*`)
	reQuoted     = regexp.MustCompile(`„[^“”"]{3,}[“”]|"[^"\n]{3,}"|«[^»\n]{3,}»|“[^”\n]{3,}”`)
	reUnitWord   = regexp.MustCompile(`^[\pL\pN_]+`)
	// The four token shapes: letters+digit (H5P, IPv6), camelCase, CamelCase,
	// all-caps acronym (PDF, DSGVO). Anchored; the tail is trimmed to a boundary.
	reTokenShapes = []*regexp.Regexp{
		regexp.MustCompile(`^[A-Za-z]+\d[\pL\pN_-]*`),
		regexp.MustCompile(`^[a-z]+[A-Z][\pL\pN_-]*`),
		regexp.MustCompile(`^[A-Z][a-z]+[A-Z][\pL\pN_-]*`),
		regexp.MustCompile(`^[A-ZÄÖÜ]{2,}[\pL\pN_-]*`),
	}
	reNameRunDE  = regexp.MustCompile(`^[A-ZÄÖÜ][a-zäöüß]{2,}(?:[ \t]+[A-ZÄÖÜ][a-zäöüß]{2,})+`)
	reNameRunEN  = regexp.MustCompile(`^[A-Z][a-z]{2,}(?:[ \t]+[A-Z][a-z]{2,})*`)
	reNameWordDE = regexp.MustCompile(`^[A-ZÄÖÜ][a-zäöüß]{2,}`)
	reNameWordEN = regexp.MustCompile(`^[A-Z][a-z]{2,}`)

	exampleRegexDE = compileAll(examplePatternsDE, "(?i)")
	exampleRegexEN = compileAll(examplePatternsEN, "(?i)")
)

var unitSymbols = "%€$£"
var unitWords = set(`km kg m cm mm h min s ms GB MB TB kB Mio Mrd Prozent percent`)

func compileAll(patterns []string, flags string) []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		out = append(out, regexp.MustCompile(flags+p))
	}
	return out
}

// findAnchors returns the anchors of one paragraph, in the reference order:
// links, numbers, quotes, example markers, named things.
func findAnchors(paragraph, lang string) []Anchor {
	var found []Anchor
	for _, m := range reURL.FindAllString(paragraph, -1) {
		found = append(found, Anchor{"url", m})
	}
	for _, n := range findNumbers(paragraph) {
		found = append(found, Anchor{"number", strings.TrimSpace(n)})
	}
	for _, m := range reQuoted.FindAllString(paragraph, -1) {
		found = append(found, Anchor{"quote", m})
	}
	patterns := append(append([]*regexp.Regexp{}, exampleRegexDE...), exampleRegexEN...)
	if lang != "de" {
		patterns = append(append([]*regexp.Regexp{}, exampleRegexEN...), exampleRegexDE...)
	}
	for _, re := range patterns {
		for _, m := range re.FindAllString(paragraph, -1) {
			found = append(found, Anchor{"example", m})
		}
	}
	for _, t := range findTokens(paragraph) {
		if t != "CODE" {
			found = append(found, Anchor{"name", t})
		}
	}
	if lang == "en" {
		for _, n := range findNameRuns(paragraph, reNameRunEN, reNameWordEN, true) {
			found = append(found, Anchor{"name", n})
		}
	} else {
		// German capitalises every noun; take only runs of two or more
		// capitalised words (Max Mustermann, Deutsche Bahn), which common nouns
		// rarely form unless an article opens the run, and runStarters filters those.
		for _, n := range findNameRuns(paragraph, reNameRunDE, reNameWordDE, false) {
			found = append(found, Anchor{"name", n})
		}
	}
	return found
}

// findNumbers: a digit run with its punctuation, not glued to a word or a
// period before it, optionally followed by a unit that is a whole word.
func findNumbers(s string) []string {
	var out []string
	for _, m := range reNumberCore.FindAllStringIndex(s, -1) {
		if p := prevRune(s, m[0]); p == '.' || isWordRune(p) {
			continue
		}
		end := m[1]
		rest := s[end:]
		if rest != "" {
			cut := 0
			if r, size := utf8.DecodeRuneInString(rest); unicode.IsSpace(r) {
				cut = size
			}
			after := rest[cut:]
			if r, size := utf8.DecodeRuneInString(after); after != "" && strings.ContainsRune(unitSymbols, r) {
				end += cut + size
			} else if w := reUnitWord.FindString(after); w != "" && unitWords[w] {
				end += cut + len(w)
				if (w == "Mio" || w == "Mrd") && strings.HasPrefix(after[len(w):], ".") {
					end++
				}
			}
		}
		out = append(out, s[m[0]:end])
	}
	return out
}

// findTokens: named things by shape at word starts (see reTokenShapes).
func findTokens(s string) []string {
	var out []string
	i := 0
	for i < len(s) {
		if !wordStartAt(s, i) {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		matched := ""
		for _, re := range reTokenShapes {
			if m := re.FindString(s[i:]); m != "" {
				matched = m
				break
			}
		}
		if matched == "" {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		// The greedy tail may end on a hyphen; a word boundary has to close it.
		for len(matched) > 0 && !wordEndAt(s, i+len(matched)) {
			matched = matched[:len(matched)-1]
		}
		if matched != "" {
			out = append(out, matched)
			i += len(matched)
			continue
		}
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
	}
	return out
}

// findNameRuns: capitalised words at word starts, one or more (English, where a
// capital mid-sentence is a name) or two or more (German). English skips a run
// that opens the text or follows sentence punctuation.
func findNameRuns(s string, run, word *regexp.Regexp, english bool) []string {
	var out []string
	i := 0
	for i < len(s) {
		if !wordStartAt(s, i) {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		if english && (i == 0 || sentenceStartBefore(s, i)) {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		m := run.FindString(s[i:])
		// Drop trailing words until the run ends at a word boundary.
		for m != "" && !wordEndAt(s, i+len(m)) {
			if idx := strings.LastIndexAny(m, " \t"); idx >= 0 {
				m = strings.TrimRight(m[:idx], " \t")
			} else {
				m = ""
			}
		}
		if m == "" || (!english && !strings.ContainsAny(m, " \t")) || word.FindString(m) == "" {
			_, size := utf8.DecodeRuneInString(s[i:])
			i += size
			continue
		}
		if isNameRun(m) {
			out = append(out, m)
		}
		i += len(m)
	}
	return out
}

// sentenceStartBefore: the two characters before i are sentence punctuation
// and white space.
func sentenceStartBefore(s string, i int) bool {
	p := prevRune(s, i)
	if !unicode.IsSpace(p) {
		return false
	}
	pp := prevRune(s, i-utf8.RuneLen(p))
	return pp == '.' || pp == '!' || pp == '?'
}
