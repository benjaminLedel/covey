// Package normalise turns a reply into text a voice can read (#501).
//
// Synthesis models do not reliably normalise text themselves: an emoji is
// read out or comes out as noise, "1.250 €" as "52 Euro", "10:30" as two
// numbers. The control plane therefore hands the voice provider words:
// emojis and symbols taken out, numbers, amounts, times, dates and codes
// written out in the reply's language, links and code replaced by a short
// phrase, markdown stripped.
//
// Normalise is a pure function over a bounded input, linear in its length,
// and idempotent: text it has produced, or text the app has already
// cleaned, comes back unchanged.
package normalise

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// MaxRunes bounds the input; the rest is cut off.
const MaxRunes = 4000

// Normalise returns text as it is to be spoken. lang is the request's
// BCP 47 tag; empty, German or English is guessed from the words. For any
// other language only what needs no language is done: markdown, links,
// code and emojis.
func Normalise(text, lang string) string {
	if utf8.RuneCountInString(text) > MaxRunes {
		text = string([]rune(text)[:MaxRunes])
	}
	text = strings.ToValidUTF8(text, "")
	l := pick(lang, text)
	p := &phrases{}
	if l != nil {
		p.link, p.code = l.linkPhrase, l.codePhrase
	}
	s := stripMarkup(text, p)
	s = dropPictographs(s)
	s = spaceDashes(s)
	if l != nil {
		s = speak(s, l)
	} else {
		s = pausesOnly(s)
	}
	return tidy(s)
}

// spaceDashes stands a dash between words apart, so it can become a pause;
// between digits it is a range and stays.
func spaceDashes(s string) string {
	if !strings.ContainsAny(s, "–—―") {
		return s
	}
	rs := []rune(s)
	var b strings.Builder
	for i, r := range rs {
		if r == '–' || r == '—' || r == '―' {
			if i > 0 && i+1 < len(rs) && isDigit(rs[i-1]) && isDigit(rs[i+1]) {
				b.WriteRune(r)
				continue
			}
			b.WriteString(" – ")
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pausesOnly is speak for a language without number words: dashes between
// clauses become a pause.
func pausesOnly(s string) string {
	toks := tokenize(s)
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if isDash(t.core) && t.lead == "" {
			out = append(out, ","+t.trail)
			continue
		}
		out = append(out, t.lead+t.core+t.trail)
	}
	return strings.Join(out, " ")
}

var (
	reSpaces      = regexp.MustCompile(`\s+`)
	reSpaceBefore = regexp.MustCompile(` ([.,;:!?…)\]])`)
	reSpaceAfter  = regexp.MustCompile(`([(\[]) `)
	reEmptyParens = regexp.MustCompile(`\([\s.,;:]*\)|\[[\s.,;:]*\]`)
	reCommaRun    = regexp.MustCompile(`,[\s,;]*,`)
	reStopComma   = regexp.MustCompile(`([.!?;:…])\s*,`)
	reCommaStop   = regexp.MustCompile(`,\s*([.!?])`)
	reLeading     = regexp.MustCompile(`^[\s,;:.!?)\]]+`)
	reTrailing    = regexp.MustCompile(`[\s,;(\[]+$`)
	reParenComma  = regexp.MustCompile(`\(\s*[,;]\s*`)
)

// tidy collapses what the steps before left: runs of spaces, spaces before
// punctuation, doubled pauses, empty brackets.
func tidy(s string) string {
	for range 2 {
		s = reSpaces.ReplaceAllString(s, " ")
		s = reSpaceBefore.ReplaceAllString(s, "$1")
		s = reSpaceAfter.ReplaceAllString(s, "$1")
		s = reEmptyParens.ReplaceAllString(s, "")
		s = reParenComma.ReplaceAllString(s, "(")
		s = reCommaRun.ReplaceAllString(s, ",")
		s = reStopComma.ReplaceAllString(s, "$1")
		s = reCommaStop.ReplaceAllString(s, "$1")
		s = reLeading.ReplaceAllString(s, "")
		s = reTrailing.ReplaceAllString(s, "")
	}
	return strings.TrimSpace(s)
}
