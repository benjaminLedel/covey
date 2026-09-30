package normalise

import "strings"

// Number words for German and English, written out here rather than taken
// from a library: the rules are few, and what a voice says has to be exactly
// what the tests say.

// maxCardinal is the largest number read as a word; beyond it the digits
// are read one by one.
const maxCardinal = 999_999_999_999

var deOnes = [20]string{"null", "eins", "zwei", "drei", "vier", "fünf", "sechs", "sieben", "acht", "neun",
	"zehn", "elf", "zwölf", "dreizehn", "vierzehn", "fünfzehn", "sechzehn", "siebzehn", "achtzehn", "neunzehn"}

var deTens = [10]string{"", "", "zwanzig", "dreißig", "vierzig", "fünfzig", "sechzig", "siebzig", "achtzig", "neunzig"}

// deOrdinalStems are the ordinals below twenty without their ending.
var deOrdinalStems = [20]string{"nullt", "erst", "zweit", "dritt", "viert", "fünft", "sechst", "siebt", "acht", "neunt",
	"zehnt", "elft", "zwölft", "dreizehnt", "vierzehnt", "fünfzehnt", "sechzehnt", "siebzehnt", "achtzehnt", "neunzehnt"}

// deUnder100 reads 0–99; final says the number ends here ("eins") rather
// than going on into a compound ("ein" in "einhundert", "einundzwanzig").
func deUnder100(n int64, final bool) string {
	switch {
	case n == 0:
		return ""
	case n == 1 && !final:
		return "ein"
	case n < 20:
		return deOnes[n]
	}
	t, o := n/10, n%10
	if o == 0 {
		return deTens[t]
	}
	one := deOnes[o]
	if o == 1 {
		one = "ein"
	}
	return one + "und" + deTens[t]
}

func deUnder1000(n int64, final bool) string {
	h, r := n/100, n%100
	s := ""
	if h > 0 {
		s = deUnder100(h, false) + "hundert"
	}
	return s + deUnder100(r, final)
}

// deCardinal: "eintausendzweihundertfünfzig", "eine Million
// zweihunderttausend". Million and Milliarde are nouns and stand apart.
func deCardinal(n int64) string {
	if n == 0 {
		return "null"
	}
	var parts []string
	big := func(v int64, one, many string) {
		switch {
		case v == 1:
			parts = append(parts, one)
		case v > 1:
			parts = append(parts, deUnder1000(v, false)+" "+many)
		}
	}
	big(n/1_000_000_000, "eine Milliarde", "Milliarden")
	big(n/1_000_000%1000, "eine Million", "Millionen")
	compound := ""
	if th := n / 1000 % 1000; th > 0 {
		compound = deUnder1000(th, false) + "tausend"
	}
	compound += deUnder1000(n%1000, true)
	if compound != "" {
		parts = append(parts, compound)
	}
	return strings.Join(parts, " ")
}

// deOrdinal: the ordinal with the ending the sentence asks for ("e",
// "en", "er", "es"): 3 → "dritten", 30 → "dreißigster".
func deOrdinal(n int64, ending string) string {
	if n < 20 {
		return deOrdinalStems[n] + ending
	}
	if r := n % 100; r > 0 && r < 20 {
		return deCardinal(n-r) + deOrdinalStems[r] + ending
	}
	return deCardinal(n) + "st" + ending
}

// deYear: 1100–1999 in hundreds ("neunzehnhundertneunundneunzig"), the
// rest as a number.
func deYear(n int64) string {
	if n >= 1100 && n < 2000 {
		return deUnder100(n/100, false) + "hundert" + deUnder100(n%100, true)
	}
	return deCardinal(n)
}

var enOnes = [20]string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine",
	"ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"}

var enTens = [10]string{"", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"}

func enUnder100(n int64) string {
	if n < 20 {
		return enOnes[n]
	}
	t, o := n/10, n%10
	if o == 0 {
		return enTens[t]
	}
	return enTens[t] + "-" + enOnes[o]
}

func enUnder1000(n int64) string {
	h, r := n/100, n%100
	var parts []string
	if h > 0 {
		parts = append(parts, enOnes[h]+" hundred")
	}
	if r > 0 {
		parts = append(parts, enUnder100(r))
	}
	return strings.Join(parts, " ")
}

// enCardinal: "one thousand two hundred fifty", "thirty-one thousand nine".
func enCardinal(n int64) string {
	if n == 0 {
		return "zero"
	}
	var parts []string
	for _, g := range []struct {
		div  int64
		name string
	}{{1_000_000_000, " billion"}, {1_000_000, " million"}, {1000, " thousand"}, {1, ""}} {
		if v := n / g.div % 1000; v > 0 {
			parts = append(parts, enUnder1000(v)+g.name)
		}
	}
	return strings.Join(parts, " ")
}

var enOrdinalWords = map[string]string{
	"one": "first", "two": "second", "three": "third", "five": "fifth", "eight": "eighth", "nine": "ninth", "twelve": "twelfth",
}

// enOrdinal turns the last word of the cardinal into an ordinal:
// "twenty-first", "thirtieth", "one hundredth".
func enOrdinal(n int64, _ string) string {
	s := enCardinal(n)
	cut := max(strings.LastIndexAny(s, " -")+1, 0)
	head, last := s[:cut], s[cut:]
	switch {
	case enOrdinalWords[last] != "":
		last = enOrdinalWords[last]
	case strings.HasSuffix(last, "y"):
		last = last[:len(last)-1] + "ieth"
	default:
		last += "th"
	}
	return head + last
}

// enYear: "twenty twenty-six", "nineteen oh five", "two thousand five".
func enYear(n int64) string {
	if n < 1100 || n > 2099 || (n >= 2000 && n < 2010) {
		return enCardinal(n)
	}
	hi, lo := n/100, n%100
	switch {
	case lo == 0:
		return enUnder100(hi) + " hundred"
	case lo < 10:
		return enUnder100(hi) + " oh " + enOnes[lo]
	}
	return enUnder100(hi) + " " + enUnder100(lo)
}
