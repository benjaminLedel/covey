package normalise

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reISODate     = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})$`)
	reISOStamp    = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[T_](\d{2}):(\d{2})(?::\d{2}(?:\.\d{1,9})?)?(?:Z|[+-]\d{2}:?\d{2})?$`)
	reDotDate     = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})\.(\d{4}|\d{2})$`)
	reDayMonth    = regexp.MustCompile(`^(\d{1,2})\.(\d{1,2})$`)
	reSlashDate   = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})$`)
	reTime        = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	reTimeRange   = regexp.MustCompile(`^(\d{1,2}):(\d{2})[–-](\d{1,2}):(\d{2})$`)
	reScore       = regexp.MustCompile(`^(\d{1,3}):(\d{1,3})$`)
	reDotTime     = regexp.MustCompile(`^(\d{1,2})\.(\d{2})$`)
	reVersionV    = regexp.MustCompile(`^[vV](\d{1,6}(?:\.\d{1,6})*)$`)
	reVersion     = regexp.MustCompile(`^\d{1,6}\.\d{1,6}\.\d{1,6}(?:\.\d{1,6})?$`)
	rePhone       = regexp.MustCompile(`^\+\d{2,15}$`)
	reGroupedDots = regexp.MustCompile(`^\d{1,3}(?:\.\d{3})+$`)
	reOrdinalEn   = regexp.MustCompile(`^(\d{1,6})(st|nd|rd|th)$`)
)

func atoi(s string) int64 {
	n, _ := strconv.ParseInt(s, 10, 64)
	return n
}

// number speaks a token with a digit in it.
func (sp *speaker) number(i int) int {
	t := sp.toks[i]
	c := t.core
	l := sp.l
	if m := reISODate.FindStringSubmatch(c); m != nil {
		if s, ok := sp.date(i, atoi(m[3]), atoi(m[2]), atoi(m[1])); ok {
			sp.emit(t, s)
			return 1
		}
	}
	if m := reISOStamp.FindStringSubmatch(c); m != nil {
		d, ok1 := sp.date(i, atoi(m[3]), atoi(m[2]), atoi(m[1]))
		h, ok2 := sp.clock(atoi(m[4]), atoi(m[5]), "")
		if ok1 && ok2 {
			sp.emit(t, d+", "+h)
			return 1
		}
	}
	if m := reDotDate.FindStringSubmatch(c); m != nil && (len(m[3]) == 4 || len(m[1]) == 2 && len(m[2]) == 2) {
		y := atoi(m[3])
		if len(m[3]) == 2 {
			y += 2000
		}
		if s, ok := sp.date(i, atoi(m[1]), atoi(m[2]), y); ok {
			sp.emit(t, s)
			return 1
		}
	}
	if m := reDayMonth.FindStringSubmatch(c); m != nil && strings.HasPrefix(t.trail, ".") {
		if s, ok := sp.date(i, atoi(m[1]), atoi(m[2]), 0); ok {
			sp.emit(token{lead: t.lead, trail: sp.keepStop(i, t.trail[1:], true)}, s)
			return 1
		}
	}
	if m := reSlashDate.FindStringSubmatch(c); m != nil {
		// English writes the month first, unless the first cannot be one.
		d, mo := atoi(m[1]), atoi(m[2])
		if l == english && d <= 12 {
			d, mo = mo, d
		}
		if s, ok := sp.date(i, d, mo, atoi(m[3])); ok {
			sp.emit(t, s)
			return 1
		}
	}
	if m := reTimeRange.FindStringSubmatch(c); m != nil {
		a, ok1 := sp.clock(atoi(m[1]), atoi(m[2]), "")
		b, ok2 := sp.clock(atoi(m[3]), atoi(m[4]), "")
		if ok1 && ok2 {
			return sp.emitTime(i, a+" "+l.to+" "+b)
		}
	}
	if m := reTime.FindStringSubmatch(c); m != nil {
		if s, ok := sp.clock(atoi(m[1]), atoi(m[2]), strings.ToLower(sp.next(i))); ok {
			return sp.emitTime(i, s)
		}
	}
	if m := reScore.FindStringSubmatch(c); m != nil {
		sp.emit(t, l.cardinal(atoi(m[1]))+" "+l.score+" "+l.cardinal(atoi(m[2])))
		return 1
	}
	if m := reDotTime.FindStringSubmatch(c); m != nil && l == german && sp.next(i) == "Uhr" {
		if s, ok := sp.clock(atoi(m[1]), atoi(m[2]), ""); ok {
			return sp.emitTime(i, s)
		}
	}
	if m := reVersionV.FindStringSubmatch(c); m != nil {
		sp.emit(t, sp.version(i, m[1], true))
		return 1
	}
	if reVersion.MatchString(c) && !(l == german && reGroupedDots.MatchString(c)) {
		parts := strings.Split(c, ".")
		ip := len(parts) == 4 && !strings.EqualFold(sp.prev(i), l.version)
		for _, p := range parts {
			ip = ip && atoi(p) <= 255
		}
		sp.emit(t, sp.version(i, c, !ip))
		return 1
	}
	if rePhone.MatchString(c) {
		sp.phone = true
		sp.emit(t, l.plus+" "+sp.digits(c[1:]))
		return 1
	}
	if n := sp.ordinal(i); n > 0 {
		return n
	}
	if isAllDigits(c) && len(c) == 4 {
		y := atoi(c)
		prev := sp.prev(i)
		dayBefore := i >= 2 && sp.toks[i-1].lead == "" && len(sp.toks[i-1].core) <= 2 && isAllDigits(sp.toks[i-1].core) &&
			sp.toks[i-2].trail == "" && monthOf(sp.toks[i-2].core) > 0
		if y >= 1000 && y <= 2099 && (monthOf(prev) > 0 || yearWords[strings.ToLower(prev)] || dayBefore) && !sp.unitNext(i) {
			sp.emit(t, l.year(y))
			return 1
		}
	}
	if l == english && isAllDigits(c) && len(c) <= 2 {
		// September 30, 30 September: the day is an ordinal.
		if d := atoi(c); d >= 1 && d <= 31 && (monthOf(sp.prev(i)) > 0 || monthOf(sp.next(i)) > 0) {
			sp.emit(t, enOrdinal(d, ""))
			return 1
		}
	}
	if n := sp.amount(i, nil, t.lead); n > 0 {
		return n
	}
	sp.emit(t, sp.mixed(c))
	return 1
}

func (sp *speaker) unitNext(i int) bool {
	nx := sp.next(i)
	_, u := units[nx]
	_, c := currencies[nx]
	return u || c || nx == "%"
}

// date reads a day, month and year (0: none) as the language says them.
func (sp *speaker) date(i int, d, m, y int64) (string, bool) {
	if d < 1 || d > 31 || m < 1 || m > 12 {
		return "", false
	}
	var s string
	if sp.l == german {
		ending, _ := deEnding(sp.prev(i))
		s = deOrdinal(d, ending) + " " + german.months[m]
	} else {
		s = english.months[m] + " " + enOrdinal(d, "")
	}
	if y > 0 {
		s += " " + sp.l.year(y)
	}
	return s, true
}

// clock reads a time of day. after is the word following it, for "am"
// and "pm" in English.
func (sp *speaker) clock(h, m int64, after string) (string, bool) {
	if h > 24 || m > 59 {
		return "", false
	}
	if sp.l == german {
		hw := deCardinal(h)
		if h == 1 {
			hw = "ein"
		}
		s := hw + " Uhr"
		if m > 0 {
			s += " " + deCardinal(m)
		}
		return s, true
	}
	suffix := ""
	switch after {
	case "am", "pm", "a.m", "p.m":
		// The reply says it; the hour stays as written.
	default:
		switch {
		case h == 0 || h == 24:
			h, suffix = 12, "AM"
		case h > 12:
			h, suffix = h-12, "PM"
		}
	}
	s := enCardinal(h)
	switch {
	case m == 0 && suffix == "" && after != "am" && after != "pm" && after != "a.m" && after != "p.m":
		s += " o'clock"
	case m > 0 && m < 10:
		s += " oh " + enOnes[m]
	case m >= 10:
		s += " " + enCardinal(m)
	}
	if suffix != "" {
		s += " " + suffix
	}
	return s, true
}

// emitTime emits a time and takes a following "Uhr" with it.
func (sp *speaker) emitTime(i int, s string) int {
	t := sp.toks[i]
	if nx := sp.next(i); nx == "Uhr" || (sp.l == english && strings.EqualFold(nx, "o'clock")) {
		sp.emit(token{lead: t.lead, trail: sp.toks[i+1].trail}, s)
		return 2
	}
	sp.emit(t, s)
	return 1
}

// version reads "0.12.0" part by part; say adds the word "version" unless
// the reply already has it.
func (sp *speaker) version(i int, v string, say bool) string {
	parts := strings.Split(v, ".")
	words := make([]string, len(parts))
	for k, p := range parts {
		words[k] = sp.idNumber(p)
		if p == "0" || (len(p) > 1 && p[0] == '0') {
			words[k] = sp.digits(p)
		}
	}
	s := strings.Join(words, " "+sp.l.dot+" ")
	if say && !strings.EqualFold(sp.prev(i), sp.l.version) {
		s = sp.l.version + " " + s
	}
	return s
}

// ordinal: German "3." before a month, or before a noun after a word that
// fixes the ending ("am 3. Oktober", "zum 2. Mal"); English "3rd".
func (sp *speaker) ordinal(i int) int {
	t := sp.toks[i]
	c := t.core
	if m := reOrdinalEn.FindStringSubmatch(c); m != nil && sp.l == english {
		sp.emit(t, enOrdinal(atoi(m[1]), ""))
		return 1
	}
	if sp.l != german || !isAllDigits(c) || len(c) > 3 || t.trail != "." || i+1 >= len(sp.toks) || sp.toks[i+1].lead != "" {
		return 0
	}
	n := atoi(c)
	nx := sp.toks[i+1].core
	ending, fixed := deEnding(sp.prev(i))
	if n == 0 || !(monthOf(nx) > 0 || (fixed && startsUpper(nx))) {
		return 0
	}
	sp.emit(token{lead: t.lead}, deOrdinal(n, ending))
	return 1
}

// num is a number as written: its integer digits and its decimals.
type num struct {
	int, frac string
	grouped   bool
}

func (n num) one() bool { return n.int == "1" && n.frac == "" }

// parseNum reads "1.250", "1,250", "2,5", "2.5", "1'250": the language's
// own separators first, the other's where they cannot mean anything else.
func parseNum(s string, l *language) (num, bool) {
	if s == "" || !isDigit(rune(s[0])) || !isDigit(rune(s[len(s)-1])) {
		return num{}, false
	}
	var parts []string
	var seps []byte
	start := 0
	for k := 0; k < len(s); k++ {
		switch ch := s[k]; {
		case ch == '.' || ch == ',' || ch == '\'':
			if k == start {
				return num{}, false
			}
			parts, seps = append(parts, s[start:k]), append(seps, ch)
			start = k + 1
		case !isDigit(rune(ch)):
			return num{}, false
		}
	}
	parts = append(parts, s[start:])
	if len(seps) == 0 {
		return num{int: s}, true
	}
	for _, conv := range [][2]byte{{l.thousands, l.decimal}, {l.decimal, l.thousands}} {
		if n, ok := parseWith(parts, seps, conv[0], conv[1]); ok {
			return n, true
		}
	}
	if len(seps) == 1 && seps[0] != '\'' {
		return num{int: parts[0], frac: parts[1]}, true
	}
	return num{}, false
}

func parseWith(parts []string, seps []byte, thousands, decimal byte) (num, bool) {
	grouped := func(ps []string, ss []byte) bool {
		if len(ps[0]) > 3 {
			return false
		}
		for k, p := range ps[1:] {
			if len(p) != 3 || (ss[k] != thousands && ss[k] != '\'') {
				return false
			}
		}
		return true
	}
	last := len(seps) - 1
	if seps[last] == decimal && strings.Count(string(seps), string(decimal)) == 1 {
		ip := parts[:len(parts)-1]
		if len(ip) == 1 {
			return num{int: ip[0], frac: parts[len(parts)-1]}, true
		}
		if grouped(ip, seps[:last]) {
			return num{int: strings.Join(ip, ""), frac: parts[len(parts)-1], grouped: true}, true
		}
		return num{}, false
	}
	if grouped(parts, seps) {
		return num{int: strings.Join(parts, ""), grouped: true}, true
	}
	return num{}, false
}

// Multipliers written as a word after a number.
var multipliers = map[string]string{
	"Mio": "mio", "Million": "mio", "Millionen": "mio", "Mrd": "mrd", "Milliarde": "mrd", "Milliarden": "mrd", "Tsd": "k",
	"million": "mio", "millions": "mio", "billion": "mrd", "billions": "mrd", "bn": "mrd", "thousand": "k",
}

func (l *language) multiplier(kind string, one bool) string {
	switch {
	case l == english && kind == "mio":
		return "million"
	case l == english && kind == "mrd":
		return "billion"
	case kind == "k":
		return l.thousandWord
	case kind == "mio" && one:
		return "Million"
	case kind == "mio":
		return "Millionen"
	case one:
		return "Milliarde"
	}
	return "Milliarden"
}

// amount speaks a number with what belongs to it: a sign, a currency, a
// range, a percentage, a unit, a multiplier — attached or in the next
// tokens. cur is a currency the token before named ("USD 5").
func (sp *speaker) amount(i int, cur *currency, lead string) int {
	t := sp.toks[i]
	l := sp.l
	c := t.core
	var pre []string
	neg, plus := false, false
	for range 4 {
		switch {
		case strings.HasPrefix(c, "-"):
			neg, c = true, c[1:]
		case strings.HasPrefix(c, "−"):
			neg, c = true, c[len("−"):]
		case strings.HasPrefix(c, "+"):
			plus, c = true, c[1:]
		case strings.HasPrefix(c, "~"):
			pre, c = append(pre, l.about), c[1:]
		case strings.HasPrefix(c, "≈"):
			pre, c = append(pre, l.about), c[len("≈"):]
		case strings.HasPrefix(c, "<="), strings.HasPrefix(c, "≤"):
			pre, c = append(pre, l.atMost), strings.TrimPrefix(strings.TrimPrefix(c, "<="), "≤")
		case strings.HasPrefix(c, ">="), strings.HasPrefix(c, "≥"):
			pre, c = append(pre, l.atLeast), strings.TrimPrefix(strings.TrimPrefix(c, ">="), "≥")
		case strings.HasPrefix(c, "<"):
			pre, c = append(pre, l.lessThan), c[1:]
		case strings.HasPrefix(c, ">"):
			pre, c = append(pre, l.moreThan), c[1:]
		default:
			for _, sym := range []string{"US$", "€", "$", "£"} {
				if cur == nil && strings.HasPrefix(c, sym) {
					cc := currencies[sym]
					cur, c = &cc, c[len(sym):]
				}
			}
		}
	}
	end := strings.IndexFunc(c, func(r rune) bool { return !isDigit(r) && r != '.' && r != ',' && r != '\'' })
	if end < 0 {
		end = len(c)
	}
	a, ok := parseNum(c[:end], l)
	if !ok {
		return 0
	}
	rest := c[end:]
	var b *num
	for _, dash := range []string{"-", "–"} {
		if strings.HasPrefix(rest, dash) {
			r := strings.TrimPrefix(rest[len(dash):], "€")
			e := strings.IndexFunc(r, func(r rune) bool { return !isDigit(r) && r != '.' && r != ',' && r != '\'' })
			if e < 0 {
				e = len(r)
			}
			bn, ok := parseNum(r[:e], l)
			if !ok || !plainRangeEnd(a) || !plainRangeEnd(bn) {
				return 0
			}
			b, rest = &bn, r[e:]
		}
	}
	suffix := rest
	_, isUnit := units[suffix]
	switch {
	case suffix == "" || suffix == "%" || suffix == "k" || suffix == "x" || isUnit:
	case currencies[suffix] != (currency{}) && cur == nil:
		cc := currencies[suffix]
		cur, suffix = &cc, ""
	default:
		return 0
	}

	// What follows in the next tokens.
	last, trail := i, t.trail
	mult := ""
	for range 3 {
		if trail != "" || last+1 >= len(sp.toks) || sp.toks[last+1].lead != "" {
			break
		}
		nx := sp.toks[last+1]
		dotted := strings.HasPrefix(nx.trail, ".")
		if k, ok := multipliers[nx.core]; ok && mult == "" && suffix != "k" {
			mult, last, trail = k, last+1, nx.trail
			if dotted && (nx.core == "Mio" || nx.core == "Mrd" || nx.core == "Tsd") {
				trail = sp.keepStop(last, nx.trail[1:], false)
			}
			continue
		}
		if cc, ok := currencies[nx.core]; ok && cur == nil && suffix == "" {
			cur, last, trail = &cc, last+1, nx.trail
			continue
		}
		if nx.core == "%" && cur == nil && suffix == "" {
			suffix, last, trail = "%", last+1, nx.trail
			continue
		}
		if _, ok := units[nx.core]; ok && cur == nil && suffix == "" && mult == "" {
			suffix, last, trail = nx.core, last+1, nx.trail
			if dotted && (nx.core == "Min" || nx.core == "Std" || nx.core == "Sek") {
				trail = sp.keepStop(last, nx.trail[1:], false)
			}
		}
		break
	}

	_, isUnit = units[suffix]
	amountCtx := cur != nil || suffix != "" || mult != ""
	one := b == nil && a.one() && suffix != "k"
	var words []string
	words = append(words, pre...)
	if neg {
		words = append(words, l.minus)
	}
	if plus {
		words = append(words, l.plus)
	}
	say := func(n num, form string) string { return sp.sayNum(n, form, amountCtx) }

	val := ""
	switch {
	case suffix == "k" && a.frac == "" && b == nil && len(a.int) <= 9:
		val = l.cardinal(atoi(a.int) * 1000)
		suffix = ""
	case suffix == "k":
		val = say(a, "") + " " + l.thousandWord
		suffix = ""
	}
	oneForm := ""
	if l == german && one {
		switch {
		case mult == "mio" || mult == "mrd":
			oneForm = "eine"
		case isUnit:
			oneForm = units[suffix].deOne
		case suffix != "":
			oneForm = "ein"
		case cur == nil && last == i && startsUpper(sp.next(i)):
			oneForm = "ein"
			if deFeminine(sp.next(i)) {
				oneForm = "eine"
			}
		}
	}
	cents := cur != nil && b == nil && mult == "" && a.frac != "" && len(a.frac) <= 2
	switch {
	case val != "":
	case cents:
		frac := a.frac
		if len(frac) == 1 {
			frac += "0"
		}
		euros, ct := atoi(a.int), atoi(frac)
		var parts []string
		if euros > 0 || ct == 0 {
			if euros == 1 {
				parts = append(parts, cur.say(l, true))
			} else {
				parts = append(parts, say(num{int: a.int, grouped: a.grouped}, "")+" "+cur.say(l, false))
			}
		}
		if ct > 0 {
			if l == german && euros > 0 {
				parts = append(parts, deCardinal(ct))
			} else {
				if l == english && euros > 0 {
					parts = append(parts, "and")
				}
				parts = append(parts, cur.sayCents(l, ct))
			}
		}
		words = append(words, parts...)
		sp.emit(token{lead: lead, trail: trail}, strings.Join(words, " "))
		return last - i + 1
	case cur != nil && one && mult == "":
		words = append(words, cur.say(l, true))
		sp.emit(token{lead: lead, trail: trail}, strings.Join(words, " "))
		return last - i + 1
	default:
		val = say(a, oneForm)
		if b != nil {
			val += " " + l.to + " " + say(*b, "")
		}
	}
	if mult != "" {
		val += " " + l.multiplier(mult, one)
	}
	switch {
	case cur != nil:
		val += " " + cur.say(l, false)
	case suffix == "%":
		val += " " + l.percent
	case suffix == "x" && l == german:
		val += "mal"
	case suffix == "x" && one:
		val = "once"
	case suffix == "x":
		val += " times"
	case isUnit:
		val += " " + units[suffix].say(l, one && mult == "")
	}
	words = append(words, val)
	sp.emit(token{lead: lead, trail: trail}, strings.Join(words, " "))
	return last - i + 1
}

// plainRangeEnd: a range's end is a short number, not a phone number's part.
func plainRangeEnd(n num) bool {
	return len(n.int) <= 6 && (len(n.int) == 1 || n.int[0] != '0')
}

// sayNum reads a number; form replaces "eins" for a lone one ("ein",
// "eine"). amount says it is a quantity, so a long run of digits is still
// a number rather than a code.
func (sp *speaker) sayNum(n num, form string, amount bool) string {
	var s string
	switch {
	case len(n.int) > 12 || (len(n.int) > 1 && n.int[0] == '0'):
		s = sp.digits(n.int)
	case !n.grouped && !amount && len(n.int) > 6:
		s = sp.digits(n.int)
	case form != "" && n.one():
		s = form
	default:
		s = sp.l.cardinal(atoi(n.int))
	}
	if n.frac != "" {
		s += " " + sp.l.comma + " " + sp.digits(n.frac)
	}
	return s
}

// mixed reads what is left: digit runs as numbers, the letters as they are.
func (sp *speaker) mixed(c string) string {
	var words []string
	rs := []rune(c)
	for k := 0; k < len(rs); {
		r := rs[k]
		switch {
		case isDigit(r):
			e := k
			for e < len(rs) && isDigit(rs[e]) {
				e++
			}
			d := string(rs[k:e])
			if len(d) > 6 || (len(d) > 1 && d[0] == '0') {
				words = append(words, sp.digits(d))
			} else {
				words = append(words, sp.l.cardinal(atoi(d)))
			}
			k = e
		case r == '.' && k > 0 && k+1 < len(rs) && isDigit(rs[k-1]) && isDigit(rs[k+1]):
			words = append(words, sp.l.dot)
			k++
		case r == ',' && k > 0 && k+1 < len(rs) && isDigit(rs[k-1]) && isDigit(rs[k+1]):
			words = append(words, sp.l.comma)
			k++
		case strings.ContainsRune("/:_-–.,'", r):
			k++
		default:
			e := k
			for e < len(rs) && !isDigit(rs[e]) && !strings.ContainsRune("/:_-–", rs[e]) {
				e++
			}
			words = append(words, string(rs[k:e]))
			k = e
		}
	}
	return strings.Join(words, " ")
}
