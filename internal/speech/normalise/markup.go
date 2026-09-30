package normalise

import (
	"regexp"
	"strings"
)

// The markdown the chat renders, taken out of what is heard: emphasis,
// headings, lists, quotes and tables lose their marks, a link keeps its
// words, and a bare address or code is replaced — once — by a short phrase
// that says where it is. The app does much the same before it sends a
// reply (mobile/lib/call/speech_text.dart); on text it has already cleaned
// this changes nothing.

var (
	reImage      = regexp.MustCompile(`!\[([^\]\n]*)\]\([^)\n]*\)`)
	reLink       = regexp.MustCompile(`\[([^\]\n]+)\]\(([^)\n]*)\)`)
	reAutolink   = regexp.MustCompile(`<(?:https?://|mailto:)[^>\s]+>`)
	reURL        = regexp.MustCompile(`(?:https?://|www\.)[^\s<>()\[\]]*[^\s<>()\[\].,;:!?'"]`)
	reInlineCode = regexp.MustCompile("``[^`\n]+``|`[^`\n]+`")
	reHTMLTag    = regexp.MustCompile(`</?[a-zA-Z][a-zA-Z0-9-]*(?:\s[^<>\n]*)?/?>`)
	reHeading    = regexp.MustCompile(`^#{1,6}\s+`)
	reHeadingEnd = regexp.MustCompile(`\s+#+$`)
	reQuote      = regexp.MustCompile(`^(?:>\s?)+`)
	reBullet     = regexp.MustCompile(`^[-*+•·‣▪◦]\s+`)
	reNumbered   = regexp.MustCompile(`^\d{1,3}[.)]\s+`)
	reCheckbox   = regexp.MustCompile(`^\[[ xX]\]\s+`)
	reRule       = regexp.MustCompile(`^[\s|:*_=+~-]+$`)
	reUnderscore = regexp.MustCompile(`(^|[\s(])_{1,2}([^_\s](?:[^_]*[^_\s])?)_{1,2}($|[\s.,;:!?)])`)
	reLineEnd    = regexp.MustCompile(`[.!?…:;,]$`)
)

var htmlEntities = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&nbsp;", " ")

// phrases says, once per text, where a left-out link or code is.
type phrases struct {
	link, code         string
	saidLink, saidCode bool
}

func (p *phrases) linkOnce() string {
	if p.saidLink {
		return " "
	}
	p.saidLink = true
	return " " + p.link + " "
}

func (p *phrases) codeOnce() string {
	if p.saidCode {
		return " "
	}
	p.saidCode = true
	return " " + p.code + " "
}

// stripMarkup returns the text as one line of prose.
func stripMarkup(s string, p *phrases) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	raw := strings.Split(s, "\n")
	// Numbered list items are told apart from "3. Oktober" at a line's
	// start only by there being more than one line.
	multi := 0
	for _, l := range raw {
		if strings.TrimSpace(l) != "" {
			multi++
		}
	}
	var lines []string
	fence := ""
	for _, line := range raw {
		t := strings.TrimSpace(line)
		if fence != "" {
			if strings.HasPrefix(t, fence) {
				fence = ""
			}
			continue
		}
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = t[:3]
			lines = append(lines, strings.TrimSpace(p.codeOnce()))
			continue
		}
		if t = cleanLine(t, p, multi > 1); t != "" {
			lines = append(lines, t)
		}
	}
	for i, l := range lines {
		if multi > 1 && !reLineEnd.MatchString(l) {
			// A line without its own stop is a heading or a list item: it
			// ends where it ends, heard as a pause.
			lines[i] = l + "."
		}
	}
	return strings.Join(lines, " ")
}

func cleanLine(t string, p *phrases, multi bool) string {
	if t == "" || reRule.MatchString(t) {
		return ""
	}
	if reHeading.MatchString(t) {
		t = reHeadingEnd.ReplaceAllString(reHeading.ReplaceAllString(t, ""), "")
	}
	t = reQuote.ReplaceAllString(t, "")
	t = reCheckbox.ReplaceAllString(reBullet.ReplaceAllString(t, ""), "")
	if multi {
		t = reNumbered.ReplaceAllString(t, "")
	}
	// Code first: a URL inside code is code.
	t = reInlineCode.ReplaceAllStringFunc(t, func(string) string { return p.codeOnce() })
	t = reImage.ReplaceAllString(t, "$1")
	t = reLink.ReplaceAllStringFunc(t, func(m string) string {
		words := reLink.FindStringSubmatch(m)[1]
		if reURL.MatchString(words) {
			return p.linkOnce()
		}
		return words
	})
	t = reAutolink.ReplaceAllStringFunc(t, func(string) string { return p.linkOnce() })
	t = reURL.ReplaceAllStringFunc(t, func(string) string { return p.linkOnce() })
	t = reHTMLTag.ReplaceAllString(t, " ")
	t = htmlEntities.Replace(t)
	if strings.Count(t, "|") >= 2 || strings.HasPrefix(t, "|") {
		var cells []string
		for c := range strings.SplitSeq(t, "|") {
			if c = strings.TrimSpace(c); c != "" {
				cells = append(cells, c)
			}
		}
		t = strings.Join(cells, ", ")
	}
	// Emphasis: asterisks carry nothing heard; underscores only around
	// words, so snake_case stays.
	t = strings.NewReplacer("**", "", "*", "", "~~", "").Replace(t)
	t = reUnderscore.ReplaceAllString(t, "$1$2$3")
	t = strings.NewReplacer(" • ", ", ", " · ", ", ", "•", " ", "·", " ").Replace(t)
	return strings.TrimSpace(t)
}
