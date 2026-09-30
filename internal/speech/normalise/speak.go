package normalise

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// token is one whitespace-separated piece of the text: the word or number
// in the middle, with the punctuation before and after it apart.
type token struct{ lead, core, trail string }

const (
	leadPunct  = "([{\"'„“‚‘«‹¿¡"
	trailPunct = ".,;:!?)]}\"'“”‘’»›…"
	// maxTokenRunes: a longer piece is no number, code or abbreviation and
	// is passed through as it is, so no pattern ever looks at more.
	maxTokenRunes = 64
)

func tokenize(s string) []token {
	fields := strings.Fields(s)
	toks := make([]token, 0, len(fields))
	for _, f := range fields {
		core := strings.TrimRight(f, trailPunct)
		trail := f[len(core):]
		trimmed := strings.TrimLeft(core, leadPunct)
		lead := core[:len(core)-len(trimmed)]
		toks = append(toks, token{lead: lead, core: trimmed, trail: trail})
	}
	return toks
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isDash(s string) bool { return s == "-" || s == "–" || s == "—" || s == "―" || s == "--" }

func hasDigit(s string) bool { return strings.IndexFunc(s, isDigit) >= 0 }

func startsUpper(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return unicode.IsUpper(r)
}

func capitalise(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// speaker walks the tokens once; every handler looks at a bounded number
// of tokens around the current one, so the walk is linear.
type speaker struct {
	l    *language
	toks []token
	out  []string
	// phone: the digit groups after "+49" are read digit by digit.
	phone bool
}

func speak(s string, l *language) string {
	sp := &speaker{l: l, toks: tokenize(s)}
	for i := 0; i < len(sp.toks); {
		i += sp.one(i)
	}
	return strings.Join(sp.out, " ")
}

// prev is the word before i when nothing stands between them.
func (sp *speaker) prev(i int) string {
	if i == 0 || sp.toks[i].lead != "" || sp.toks[i-1].trail != "" {
		return ""
	}
	return sp.toks[i-1].core
}

// next is the word after i when nothing stands between them.
func (sp *speaker) next(i int) string {
	if i+1 >= len(sp.toks) || sp.toks[i].trail != "" || sp.toks[i+1].lead != "" {
		return ""
	}
	return sp.toks[i+1].core
}

func (sp *speaker) emit(t token, words string) { sp.out = append(sp.out, t.lead+words+t.trail) }

// one speaks the token at i and says how many tokens it took.
func (sp *speaker) one(i int) int {
	t := sp.toks[i]
	c := t.core
	if c == "" || utf8.RuneCountInString(c) > maxTokenRunes {
		sp.phone = false
		sp.emit(t, c)
		return 1
	}
	if sp.phone && t.lead == "" && isAllDigits(c) {
		sp.emit(t, sp.digits(c))
		return 1
	}
	sp.phone = false
	if isDash(c) && t.lead == "" {
		// Between two numbers a range, elsewhere a pause.
		if i > 0 && i+1 < len(sp.toks) && sp.toks[i-1].trail == "" && hasDigit(sp.toks[i-1].core) &&
			strings.IndexFunc(sp.toks[i+1].core, isDigit) == 0 {
			sp.emit(t, sp.l.to)
		} else {
			sp.out = append(sp.out, ","+t.trail)
		}
		return 1
	}
	// USD 5, € 20: the currency before the amount.
	if cur, ok := currencies[c]; ok && t.trail == "" && i+1 < len(sp.toks) && sp.toks[i+1].lead == "" &&
		strings.IndexFunc(sp.toks[i+1].core, isDigit) == 0 {
		if n := sp.amount(i+1, &cur, t.lead); n > 0 {
			return n + 1
		}
	}
	for _, h := range []func(int) int{sp.abbreviation, sp.code} {
		if n := h(i); n > 0 {
			return n
		}
	}
	if hasDigit(c) {
		return sp.number(i)
	}
	if n := sp.symbol(i); n > 0 {
		return n
	}
	sp.emit(t, c)
	return 1
}

// abbreviation: spelled acronyms, dev words, "z. B.".
func (sp *speaker) abbreviation(i int) int {
	t := sp.toks[i]
	c := t.core
	// MR !475, PR #480: one "merge request", not two.
	if c == "MR" || c == "PR" {
		if nx := sp.next(i); reRef.MatchString(nx) {
			word := sp.l.mergeRequest
			if c == "PR" {
				word = sp.l.pullRequest
			}
			sp.emit(token{lead: t.lead, trail: sp.toks[i+1].trail}, word+" "+sp.idNumber(nx[1:]))
			return 2
		}
	}
	if w, ok := acronymsBy[sp.l.code][c]; ok {
		sp.emit(t, w)
		return 1
	}
	if w, ok := acronyms[c]; ok {
		sp.emit(t, w)
		return 1
	}
	// API-Key, CI-Pipeline: the acronym in a compound.
	if strings.Contains(c, "-") && !hasDigit(c) {
		parts := strings.Split(c, "-")
		changed := false
		for k, p := range parts {
			if w, ok := acronyms[p]; ok {
				parts[k], changed = w, true
			}
		}
		if changed {
			sp.emit(t, strings.Join(parts, "-"))
			return 1
		}
	}
	if !strings.HasPrefix(t.trail, ".") {
		return 0
	}
	// z. B.
	if i+1 < len(sp.toks) && t.trail == "." && strings.HasPrefix(sp.toks[i+1].trail, ".") && sp.toks[i+1].lead == "" {
		key := strings.ToLower(c + " " + sp.toks[i+1].core)
		if w, ok := pairAbbrevsBy[sp.l.code][key]; ok {
			last := sp.toks[i+1]
			sp.emit(token{lead: t.lead, trail: sp.keepStop(i+1, last.trail[1:], false)}, sp.caseLike(c, w))
			return 2
		}
	}
	key := strings.ToLower(c)
	a, ok := abbrevsBy[sp.l.code][key]
	if !ok {
		return 0
	}
	if (key == "no" || key == "nr") && strings.IndexFunc(sp.nextAny(i), isDigit) != 0 {
		return 0
	}
	sp.emit(token{lead: t.lead, trail: sp.keepStop(i, t.trail[1:], a.ends)}, sp.caseLike(c, a.words))
	return 1
}

func (sp *speaker) nextAny(i int) string {
	if i+1 < len(sp.toks) {
		return sp.toks[i+1].core
	}
	return ""
}

// keepStop: an abbreviation's dot that also ends the sentence stays.
func (sp *speaker) keepStop(i int, rest string, ends bool) string {
	last := i+1 >= len(sp.toks)
	if strings.TrimLeft(rest, ".") == rest && (last || (ends && startsUpper(sp.toks[i+1].lead+sp.toks[i+1].core))) {
		return "." + rest
	}
	return rest
}

// caseLike capitalises words where the abbreviation was capitalised at a
// sentence's start ("Z. B." → "Zum Beispiel").
func (sp *speaker) caseLike(abbr, words string) string {
	if startsUpper(abbr) && !startsUpper(words) && strings.ToLower(abbr) != abbr {
		return capitalise(words)
	}
	return words
}

var (
	reRef      = regexp.MustCompile(`^[!#]\d{1,12}$`)
	reHashtag  = regexp.MustCompile(`^#\pL[\pL\pN_-]*$`)
	rePrefixed = regexp.MustCompile(`^([A-Z][A-Z0-9]{1,9})-(\d{1,9})$`)
	reHex      = regexp.MustCompile(`^(?:0x[0-9a-fA-F]+|[0-9a-fA-F]{7,}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)
)

// code: ticket and merge-request references, prefixed ids, hashes.
func (sp *speaker) code(i int) int {
	t := sp.toks[i]
	c := t.core
	switch {
	case reRef.MatchString(c):
		n := sp.idNumber(c[1:])
		prev := strings.ToLower(sp.prev(i))
		switch {
		case c[0] == '#' && numberNouns[prev]:
			sp.emit(t, n)
		case c[0] == '#':
			sp.emit(t, sp.l.ticket+" "+n)
		case prev == "request" || prev == "mr":
			sp.emit(t, n)
		default:
			sp.emit(t, sp.l.mergeRequest+" "+n)
		}
		return 1
	case reHashtag.MatchString(c):
		sp.emit(t, c[1:])
		return 1
	case rePrefixed.MatchString(c):
		m := rePrefixed.FindStringSubmatch(c)
		if letters := strings.IndexFunc(m[1][1:], unicode.IsUpper); letters < 0 {
			return 0
		}
		spelled := make([]string, 0, len(m[1]))
		for _, r := range m[1] {
			if isDigit(r) {
				spelled = append(spelled, sp.l.digits[r-'0'])
			} else {
				spelled = append(spelled, string(r))
			}
		}
		sp.emit(t, strings.Join(spelled, " ")+" "+sp.idNumber(m[2]))
		return 1
	case reHex.MatchString(c) && hasDigit(c) && strings.ContainsAny(strings.ToLower(c), "abcdef"):
		// A hash or UUID is read in the chat, not aloud.
		sp.emit(token{lead: t.lead, trail: t.trail}, "")
		return 1
	}
	return 0
}

// idNumber reads a ticket or id number: as a number up to six digits,
// digit by digit beyond.
func (sp *speaker) idNumber(d string) string {
	if len(d) > 6 || (len(d) > 1 && d[0] == '0') {
		return sp.digits(d)
	}
	n, _ := strconv.ParseInt(d, 10, 64)
	return sp.l.cardinal(n)
}

func (sp *speaker) digits(d string) string {
	words := make([]string, 0, len(d))
	for _, r := range d {
		if isDigit(r) {
			words = append(words, sp.l.digits[r-'0'])
		}
	}
	return strings.Join(words, " ")
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !isDigit(r) {
			return false
		}
	}
	return true
}

// symbol: a sign standing on its own.
func (sp *speaker) symbol(i int) int {
	t := sp.toks[i]
	w := ""
	switch t.core {
	case "&":
		w = sp.l.and
	case "+":
		w = sp.l.plus
	case "=":
		w = sp.l.equals
	case "×":
		w = sp.l.times
	case "~", "≈":
		w = sp.l.about
	case "§", "§§":
		w = sp.l.section
	case "%":
		w = sp.l.percent
	case "#", "*", "_", "/", "\\", "|", "^", "`":
		w = ""
	default:
		cur, ok := currencies[t.core]
		if !ok {
			return 0
		}
		w = cur.say(sp.l, false)
	}
	sp.emit(t, w)
	return 1
}
